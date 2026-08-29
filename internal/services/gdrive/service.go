package gdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/rcloneprofile"
	"github.com/IamYGT/heyserver/internal/services/backup"
	"github.com/IamYGT/heyserver/internal/services/notify"
	"github.com/IamYGT/heyserver/internal/store"
)

// JobTracker receives live progress from Drive uploads/restores.
type JobTracker interface {
	StartJob(jobType, source, message string) string
	UpdateJobProgress(id string, phase backup.JobPhase, progress int, message string, bytesDone, bytesTotal int64, speed string)
	AppendJobLog(id, line string)
	SetJobCommand(id, cmd string)
	CompleteJob(id string, success bool, message, outputFile string)
}

// Service manages Google Drive offsite backups via rclone + OAuth.
type Service struct {
	dataDir            string
	port               int
	configuredRedirect string
	envClientID        string
	envClientSecret    string
	rcloneBin          string
	settingsRepo       *store.SettingsRepository
	channelRepo        *store.NotificationChannelRepository
	receiptRepo        notify.ReceiptRecorder
	oauth              *oauthManager
	rclone             *rcloneRunner
	jobs               JobTracker
	statusRefresh      func(*tokenData, string) (*tokenData, error)
	statusFetchAbout   func(string) (string, string, *StorageQuota, error)
	// Readiness dependencies are narrow, context-aware seams. They are kept
	// separate from the legacy Status hooks because a readiness observation is
	// read-only and must never refresh/persist OAuth state.
	readinessRcloneCheck func(context.Context) error
	readinessLoadToken   func(context.Context) (*tokenData, error)
	readinessFetchAbout  func(context.Context, string) error
}

// New creates a Google Drive backup service.
// OAuth app credentials may come from env (override) or panel settings DB.
func New(dataDir string, port int, clientID, clientSecret, redirectURI, rcloneBin string,
	settingsRepo *store.SettingsRepository,
	channelRepo *store.NotificationChannelRepository,
) *Service {
	s := &Service{
		dataDir:            dataDir,
		port:               port,
		configuredRedirect: strings.TrimSpace(redirectURI),
		envClientID:        clientID,
		envClientSecret:    clientSecret,
		rcloneBin:          rcloneBin,
		settingsRepo:       settingsRepo,
		channelRepo:        channelRepo,
		oauth:              newOAuthManager(dataDir),
		rclone:             newRcloneRunner(dataDir, rcloneBin),
	}
	s.statusRefresh = s.oauth.refreshIfNeeded
	s.statusFetchAbout = s.oauth.fetchAbout
	s.syncOAuthCredentials()
	return s
}

// SetJobTracker wires live job progress into the backup job hub.
func (s *Service) SetJobTracker(t JobTracker) { s.jobs = t }

// SetReceiptRecorder wires the bounded delivery receipt sink used by backup
// notifications. It is optional so existing callers can continue to create a
// service without notification receipt persistence.
func (s *Service) SetReceiptRecorder(recorder notify.ReceiptRecorder) { s.receiptRepo = recorder }

// Status returns connection info, quota, and settings for the UI.
func (s *Service) Status(redirectURI string) (*Status, error) {
	redirectURI = s.effectiveRedirectURI(redirectURI)
	s.syncOAuthCredentials()
	settings, err := s.loadSettings()
	if err != nil {
		return nil, err
	}
	_, _, source := s.resolveCredentials()
	appInfo, _ := s.GetOAuthAppInfo(redirectURI)
	st := &Status{
		Connected:         false,
		Configured:        s.oauth.configured(),
		Settings:          settings,
		RcloneFound:       s.rclone.found(),
		RedirectURI:       redirectURI,
		CredentialsSource: source,
	}
	if appInfo != nil {
		st.OAuthApp = appInfo
	}

	if !st.Configured || !st.RcloneFound {
		st.State = gdriveAvailabilityState(st.Configured, st.RcloneFound, false, false)
		st.Message = gdriveNotConfiguredMessage(st)
		return st, nil
	}

	token, err := s.oauth.loadToken(context.Background())
	if err != nil || token == nil {
		st.State = gdriveAvailabilityState(st.Configured, st.RcloneFound, false, false)
		st.Message = gdriveTokenMessage(err)
		return st, nil
	}

	refresh := s.statusRefresh
	if refresh == nil {
		refresh = s.oauth.refreshIfNeeded
	}
	token, err = refresh(token, redirectURI)
	if err != nil {
		st.Connected = false
		st.State = gdriveAvailabilityState(st.Configured, st.RcloneFound, true, false)
		st.Message = err.Error()
		st.Settings.LastError = err.Error()
		st.ReconnectRequired = isAuthFailure(err)
		return st, nil
	}

	fetchAbout := s.statusFetchAbout
	if fetchAbout == nil {
		fetchAbout = s.oauth.fetchAbout
	}
	email, name, quota, err := fetchAbout(token.AccessToken)
	if err != nil {
		st.Connected = false
		st.State = gdriveAvailabilityState(st.Configured, st.RcloneFound, true, false)
		st.Message = err.Error()
		st.Settings.LastError = err.Error()
		st.ReconnectRequired = isAuthFailure(err)
		return st, nil
	}

	st.State = gdriveAvailabilityState(st.Configured, st.RcloneFound, true, true)
	st.Connected = true
	st.Email = email
	st.DisplayName = name
	st.Quota = quota
	return st, nil
}

func gdriveAvailabilityState(configured, rcloneFound, tokenAvailable, probeSuccessful bool) integrationstate.State {
	return integrationstate.FromObservation(integrationstate.Observation{
		Configured: configured && rcloneFound && tokenAvailable,
		Successful: probeSuccessful,
	})
}

func gdriveNotConfiguredMessage(st *Status) string {
	if !st.Configured {
		return "Google Drive OAuth client is not configured"
	}
	if !st.RcloneFound {
		return "rclone is not installed or unavailable"
	}
	return "Google Drive OAuth token is not configured"
}

func gdriveTokenMessage(err error) string {
	if err != nil {
		return "Google Drive token could not be loaded: " + err.Error()
	}
	return "Google Drive OAuth token is not configured"
}

// OAuthStart begins the Google OAuth flow (bound to initiating admin userID).
func (s *Service) OAuthStart(redirectURI string, userID int64) (*OAuthStartResponse, error) {
	redirectURI = s.effectiveRedirectURI(redirectURI)
	s.syncOAuthCredentials()
	if !s.oauth.configured() {
		return nil, errClientIDRequired
	}
	return s.oauth.start(redirectURI, userID)
}

// OAuthCallback stores the authorization code from Google's redirect (public endpoint).
func (s *Service) OAuthCallback(code, state string) error {
	return s.oauth.storeCallbackCode(code, state)
}

// OAuthComplete exchanges the stored code — requires authenticated admin who started the flow.
func (s *Service) OAuthComplete(state string, userID int64) error {
	token, err := s.oauth.complete(state, userID)
	if err != nil {
		return err
	}
	if err := s.oauth.saveToken(token); err != nil {
		return err
	}
	if err := s.rclone.writeConfig(token); err != nil {
		return err
	}
	s.notify("Google Drive bağlandı", fmt.Sprintf("Yedekleme klasörü: %s", s.effectiveFolder(nil)))
	return nil
}

// Disconnect removes stored credentials.
func (s *Service) Disconnect() error {
	_ = s.oauth.deleteToken()
	confPath := filepath.Join(s.dataDir, rcloneConfName)
	_ = os.Remove(confPath)
	return nil
}

// UpdateSettings atomically replaces the operator-controlled preferences while
// preserving server-owned upload result fields.
func (s *Service) UpdateSettings(in SettingsUpdate) error {
	folder, err := validateRemoteFolder(in.Folder)
	if err != nil {
		return err
	}
	if in.RemoteRetentionDays < 0 || in.RemoteRetentionDays > 365 {
		return fmt.Errorf("%w: remoteRetentionDays must be between 0 and 365", ErrInvalidSettings)
	}
	current, err := s.loadSettings()
	if err != nil {
		return err
	}
	current.Folder = folder
	current.AutoUpload = in.AutoUpload
	// 0 = remote retention disabled
	current.RemoteRetentionDays = in.RemoteRetentionDays
	current.NotifyOnSuccess = in.NotifyOnSuccess
	current.NotifyOnFailure = in.NotifyOnFailure
	return s.saveSettings(current)
}

// ClearLastError removes the persisted last-error banner after user acknowledgement.
func (s *Service) ClearLastError() error {
	current, err := s.loadSettings()
	if err != nil {
		return err
	}
	current.LastError = ""
	return s.saveSettings(current)
}

// TestConnection verifies rclone can access the remote folder.
func (s *Service) TestConnection(redirectURI string) error {
	if err := s.ensureReady(redirectURI); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	settings, _ := s.loadSettings()
	return s.rclone.test(ctx, s.effectiveFolder(&settings))
}

// UploadAsync starts a Drive upload in the background and returns a job ID when tracker is wired.
func (s *Service) UploadAsync(localPath, redirectURI, source string) (string, error) {
	if err := s.ensureReady(redirectURI); err != nil {
		return "", err
	}
	fileName := filepath.Base(localPath)
	jobID := ""
	if s.jobs != nil {
		jobID = s.jobs.StartJob("gdrive_upload", source, "Google Drive yüklemesi: "+fileName)
	}
	go func() {
		if err := s.uploadTracked(jobID, localPath, redirectURI); err != nil {
			slog.Warn("gdrive upload failed", "file", fileName, "error", err)
		}
	}()
	return jobID, nil
}

// Upload copies a local backup file to Google Drive and verifies checksum (blocking).
func (s *Service) Upload(localPath, redirectURI string) error {
	return s.uploadTracked("", localPath, redirectURI)
}

func (s *Service) uploadTracked(jobID, localPath, redirectURI string) error {
	if err := s.RefreshSession(redirectURI); err != nil {
		if err2 := s.ensureReady(redirectURI); err2 != nil {
			if jobID != "" && s.jobs != nil {
				s.jobs.CompleteJob(jobID, false, err2.Error(), "")
			}
			return err2
		}
	}
	info, err := os.Stat(localPath)
	if err != nil {
		err = fmt.Errorf("local file: %w", err)
		if jobID != "" && s.jobs != nil {
			s.jobs.CompleteJob(jobID, false, err.Error(), "")
		}
		return err
	}
	const minUploadBytes = 1024
	if info.Size() < minUploadBytes {
		err = fmt.Errorf("local backup too small to upload (%d bytes) — wait for backup to finish or retry", info.Size())
		if jobID != "" && s.jobs != nil {
			s.jobs.CompleteJob(jobID, false, err.Error(), "")
		}
		return err
	}
	settings, _ := s.loadSettings()
	folder := s.effectiveFolder(&settings)
	fileName := filepath.Base(localPath)

	ctx, cancel := context.WithTimeout(context.Background(), uploadTimeout)
	defer cancel()

	rcloneCmd := rcloneprofile.JoinCLI(append([]string{"rclone"}, append(rcloneprofile.CLICopyFlags(),
		"copy", localPath, rcloneprofile.RemoteName+":"+folder, "--checksum", "--stats", "1s", "--stats-one-line", "-v")...)...)
	progressFn := func(bytesDone, bytesTotal int64, percent int, speed, eta, rawLine string) {
		if jobID == "" || s.jobs == nil {
			return
		}
		if rawLine != "" {
			s.jobs.AppendJobLog(jobID, "rclone: "+rawLine)
		}
		msg := fmt.Sprintf("Drive'a yükleniyor: %s", fileName)
		if speed != "" {
			msg += " — " + speed
		}
		if eta != "" && eta != "0s" {
			msg += ", ETA " + eta
		}
		prog := 10 + percent*75/100
		if prog > 85 {
			prog = 85
		}
		s.jobs.UpdateJobProgress(jobID, backup.PhaseGDriveUpload, prog, msg, bytesDone, bytesTotal, speed)
	}
	logFn := func(line string) {
		if jobID != "" && s.jobs != nil && line != "" {
			s.jobs.AppendJobLog(jobID, "rclone: "+line)
		}
	}

	if jobID != "" && s.jobs != nil {
		s.jobs.SetJobCommand(jobID, rcloneCmd)
		s.jobs.AppendJobLog(jobID, fmt.Sprintf("local=%s size=%d bytes folder=%s", localPath, info.Size(), folder))
		s.jobs.AppendJobLog(jobID, "EXEC: "+rcloneCmd)
		s.jobs.UpdateJobProgress(jobID, backup.PhaseGDriveUpload, 8, "Yükleme başlıyor…", 0, info.Size(), "")
	}
	if err := s.rclone.copyWithProgress(ctx, localPath, folder, progressFn, logFn); err != nil {
		s.recordError(err)
		s.notifyFailure("Google Drive yükleme başarısız", fileName, err)
		if jobID != "" && s.jobs != nil {
			s.jobs.CompleteJob(jobID, false, err.Error(), "")
		}
		return err
	}

	if jobID != "" && s.jobs != nil {
		verifyCmd := fmt.Sprintf("verify size local=%d remote via lsjson folder=%s file=%s", info.Size(), folder, fileName)
		s.jobs.SetJobCommand(jobID, verifyCmd)
		s.jobs.AppendJobLog(jobID, "EXEC: "+verifyCmd)
		s.jobs.UpdateJobProgress(jobID, backup.PhaseVerify, 90, "Boyut doğrulanıyor (Drive lsjson)…", info.Size(), info.Size(), "")
	}
	if err := s.rclone.verify(ctx, localPath, folder, fileName); err != nil {
		s.recordError(err)
		s.notifyFailure("Google Drive doğrulama başarısız", fileName, err)
		err = fmt.Errorf("upload verify failed: %w", err)
		if jobID != "" && s.jobs != nil {
			s.jobs.CompleteJob(jobID, false, err.Error(), "")
		}
		return err
	}

	settings.LastUploadAt = time.Now().UTC().Format(time.RFC3339)
	settings.LastUploadFile = fileName
	settings.LastError = ""
	_ = s.saveSettings(settings)

	if settings.RemoteRetentionDays > 0 {
		if jobID != "" && s.jobs != nil {
			s.jobs.UpdateJobProgress(jobID, backup.PhaseRetention, 95, "Uzak retention uygulanıyor…", info.Size(), info.Size(), "")
		}
		s.applyRetention(folder, settings.RemoteRetentionDays)
	}

	if settings.NotifyOnSuccess {
		s.notify("Yedek Google Drive'a yüklendi",
			fmt.Sprintf("Dosya: %s\nBoyut: %d bayt", fileName, info.Size()))
	}
	if jobID != "" && s.jobs != nil {
		s.jobs.CompleteJob(jobID, true, "Drive yüklemesi tamamlandı", fileName)
	}
	return nil
}

// UploadIfEnabled uploads when auto-upload is enabled (non-blocking).
func (s *Service) UploadIfEnabled(localPath string) {
	settings, err := s.loadSettings()
	if err != nil || !settings.AutoUpload {
		return
	}
	path := localPath
	redirectURI := BuildInternalRedirectURI(s.port)
	if _, err := s.UploadAsync(path, redirectURI, "auto"); err != nil {
		slog.Warn("gdrive auto-upload start failed", "file", filepath.Base(path), "error", err)
	}
}

// ListRemote returns backup files stored on Google Drive.
func (s *Service) ListRemote(redirectURI string) ([]RemoteBackup, error) {
	if err := s.ensureReady(redirectURI); err != nil {
		return nil, err
	}
	settings, _ := s.loadSettings()
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	backups, err := s.rclone.listJSON(ctx, s.effectiveFolder(&settings))
	if err != nil {
		return nil, err
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].ModTime.After(backups[j].ModTime)
	})
	return backups, nil
}

// RestoreFromRemoteAsync downloads a remote backup in the background.
func (s *Service) RestoreFromRemoteAsync(remoteFileName, localBackupDir, redirectURI string) (string, error) {
	var err error
	remoteFileName, err = validateRemoteBackupName(remoteFileName)
	if err != nil {
		return "", err
	}
	if err := s.ensureReady(redirectURI); err != nil {
		return "", err
	}
	jobID := ""
	if s.jobs != nil {
		jobID = s.jobs.StartJob("gdrive_restore", "manual", "Google Drive indirme: "+remoteFileName)
	}
	go func() {
		if _, err := s.restoreTracked(jobID, remoteFileName, localBackupDir, redirectURI); err != nil {
			slog.Warn("gdrive restore failed", "file", remoteFileName, "error", err)
		}
	}()
	return jobID, nil
}

func (s *Service) restoreTracked(jobID, remoteFileName, localBackupDir, redirectURI string) (string, error) {
	if err := s.ensureReady(redirectURI); err != nil {
		if jobID != "" && s.jobs != nil {
			s.jobs.CompleteJob(jobID, false, err.Error(), "")
		}
		return "", err
	}
	if err := os.MkdirAll(localBackupDir, 0o750); err != nil {
		err = fmt.Errorf("mkdir backup dir: %w", err)
		if jobID != "" && s.jobs != nil {
			s.jobs.CompleteJob(jobID, false, err.Error(), "")
		}
		return "", err
	}
	settings, _ := s.loadSettings()
	folder := s.effectiveFolder(&settings)

	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()

	var remoteSize int64
	if size, err := s.rclone.remoteFileSize(ctx, folder, remoteFileName); err == nil {
		remoteSize = size
	}

	rcloneCmd := rcloneprofile.JoinCLI(append([]string{"rclone"}, append(rcloneprofile.CLICopyFlags(),
		"copy", rcloneprofile.RemoteName+":"+folder+"/"+remoteFileName, localBackupDir,
		"--checksum", "--stats", "1s", "--stats-one-line", "-v")...)...)
	logFn := func(line string) {
		if jobID != "" && s.jobs != nil && line != "" {
			s.jobs.AppendJobLog(jobID, "rclone: "+line)
		}
	}
	progressFn := func(bytesDone, bytesTotal int64, percent int, speed, eta, rawLine string) {
		if jobID == "" || s.jobs == nil {
			return
		}
		if rawLine != "" {
			s.jobs.AppendJobLog(jobID, "rclone: "+rawLine)
		}
		msg := "Drive'dan indiriliyor: " + remoteFileName
		if speed != "" {
			msg += " — " + speed
		}
		if eta != "" && eta != "0s" {
			msg += ", ETA " + eta
		}
		total := bytesTotal
		if total == 0 {
			total = remoteSize
		}
		prog := 10 + percent*80/100
		if prog > 92 {
			prog = 92
		}
		s.jobs.UpdateJobProgress(jobID, backup.PhaseGDriveRestore, prog, msg, bytesDone, total, speed)
	}

	if jobID != "" && s.jobs != nil {
		s.jobs.SetJobCommand(jobID, rcloneCmd)
		s.jobs.AppendJobLog(jobID, fmt.Sprintf("remote=%s/%s localDir=%s size=%d", folder, remoteFileName, localBackupDir, remoteSize))
		s.jobs.AppendJobLog(jobID, "EXEC: "+rcloneCmd)
		s.jobs.UpdateJobProgress(jobID, backup.PhaseGDriveRestore, 5, "İndirme başlıyor…", 0, remoteSize, "")
	}

	if err := s.rclone.copyFromRemoteWithProgress(ctx, remoteFileName, folder, localBackupDir, progressFn, logFn); err != nil {
		s.recordError(err)
		s.notifyFailure("Google Drive'dan indirme başarısız", remoteFileName, err)
		if jobID != "" && s.jobs != nil {
			s.jobs.CompleteJob(jobID, false, err.Error(), "")
		}
		return "", err
	}

	localPath := filepath.Join(localBackupDir, remoteFileName)
	info, err := os.Stat(localPath)
	if err != nil {
		err = fmt.Errorf("downloaded file not found: %w", err)
		if jobID != "" && s.jobs != nil {
			s.jobs.CompleteJob(jobID, false, err.Error(), "")
		}
		return "", err
	}

	if jobID != "" && s.jobs != nil {
		s.jobs.UpdateJobProgress(jobID, backup.PhaseVerify, 95, "İndirilen dosya doğrulanıyor…", info.Size(), info.Size(), "")
	}
	if remoteSize > 0 {
		if err := verifyFileSizes(info.Size(), remoteSize); err != nil {
			s.notifyFailure("Google Drive indirme doğrulama başarısız", remoteFileName, err)
			if jobID != "" && s.jobs != nil {
				s.jobs.CompleteJob(jobID, false, err.Error(), "")
			}
			return "", err
		}
	}

	s.notify("Google Drive'dan yedek indirildi", fmt.Sprintf("Dosya: %s\nKonum: %s", remoteFileName, localPath))
	if jobID != "" && s.jobs != nil {
		s.jobs.CompleteJob(jobID, true, "Drive indirmesi tamamlandı", remoteFileName)
	}
	return localPath, nil
}

// RestoreFromRemote downloads a remote backup into localBackupDir (blocking).
func (s *Service) RestoreFromRemote(remoteFileName, localBackupDir, redirectURI string) (string, error) {
	var err error
	remoteFileName, err = validateRemoteBackupName(remoteFileName)
	if err != nil {
		return "", err
	}
	return s.restoreTracked("", remoteFileName, localBackupDir, redirectURI)
}

// EnsureReady validates rclone + OAuth before backup/snapshot operations.
func (s *Service) EnsureReady(redirectURI string) error {
	return s.ensureReady(redirectURI)
}

// RefreshSession forces OAuth token refresh and rewrites rclone.conf (long snapshot uploads).
func (s *Service) RefreshSession(redirectURI string) error {
	redirectURI = s.effectiveRedirectURI(redirectURI)
	if !s.rclone.found() {
		return fmt.Errorf("rclone not found — install with: apt install rclone")
	}
	token, err := s.oauth.loadToken(context.Background())
	if err != nil || token == nil {
		return fmt.Errorf("Google Drive not connected")
	}
	token, err = s.oauth.forceRefresh(token, redirectURI)
	if err != nil {
		return err
	}
	return s.rclone.writeConfig(token)
}

// RcloneConfigPath returns the path to rclone.conf (shared with restic backend).
func (s *Service) RcloneConfigPath() string {
	return s.rclone.configPath
}

// InternalRedirectURI builds localhost OAuth callback for server-side refresh.
func (s *Service) InternalRedirectURI(port int) string {
	return BuildInternalRedirectURIFromPort(port)
}

func (s *Service) ensureReady(redirectURI string) error {
	redirectURI = s.effectiveRedirectURI(redirectURI)
	if !s.rclone.found() {
		return fmt.Errorf("rclone not found — install with: apt install rclone")
	}
	token, err := s.oauth.loadToken(context.Background())
	if err != nil || token == nil {
		return fmt.Errorf("Google Drive not connected")
	}
	token, err = s.oauth.refreshIfNeeded(token, redirectURI)
	if err != nil {
		return err
	}
	return s.rclone.writeConfig(token)
}

// effectiveRedirectURI keeps refreshes bound to the URI used during the
// original OAuth grant. Before a token exists, an explicitly configured public
// callback wins over a request-derived localhost/proxy address.
func (s *Service) effectiveRedirectURI(requestURI string) string {
	if s.oauth != nil {
		if td, err := s.oauth.loadToken(context.Background()); err == nil && td != nil && strings.TrimSpace(td.RedirectURI) != "" {
			return strings.TrimSpace(td.RedirectURI)
		}
	}
	if strings.TrimSpace(s.configuredRedirect) != "" {
		return strings.TrimSpace(s.configuredRedirect)
	}
	return strings.TrimSpace(requestURI)
}

func (s *Service) loadSettings() (Settings, error) {
	def := Settings{
		Folder:              defaultFolder,
		AutoUpload:          false,
		RemoteRetentionDays: 30,
		NotifyOnSuccess:     true,
		NotifyOnFailure:     true,
	}
	setting, err := s.settingsRepo.Get(settingsKey)
	if err != nil || setting == nil {
		return def, err
	}
	var st Settings
	if err := json.Unmarshal([]byte(setting.Value), &st); err != nil {
		return def, err
	}
	var persistedFields struct {
		RemoteRetentionDays *int `json:"remoteRetentionDays"`
	}
	if err := json.Unmarshal([]byte(setting.Value), &persistedFields); err != nil {
		return def, err
	}
	if st.Folder == "" {
		st.Folder = defaultFolder
	} else {
		folder, err := validateRemoteFolder(st.Folder)
		if err != nil {
			return def, err
		}
		st.Folder = folder
	}
	if persistedFields.RemoteRetentionDays == nil {
		st.RemoteRetentionDays = 30
	} else if st.RemoteRetentionDays < 0 || st.RemoteRetentionDays > 365 {
		return def, fmt.Errorf("%w: persisted remoteRetentionDays must be between 0 and 365", ErrInvalidSettings)
	}
	return st, nil
}

func validateRemoteFolder(value string) (string, error) {
	folder := strings.TrimSpace(value)
	if folder == "" {
		return "", fmt.Errorf("%w: folder is required", ErrInvalidSettings)
	}
	if len(folder) > 255 {
		return "", fmt.Errorf("%w: folder must be at most 255 characters", ErrInvalidSettings)
	}
	if sanitizePath(folder) != folder {
		return "", fmt.Errorf("%w: folder must be a safe relative Drive path", ErrInvalidSettings)
	}
	for _, part := range strings.Split(folder, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("%w: folder must not contain empty or traversal segments", ErrInvalidSettings)
		}
	}
	return folder, nil
}

func validateRemoteBackupName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" || name != value || len(name) > 255 || filepath.Base(name) != name || sanitizePath(name) != name {
		return "", fmt.Errorf("invalid backup file name")
	}
	if first := name[0]; !((first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || (first >= '0' && first <= '9')) {
		return "", fmt.Errorf("invalid backup file name")
	}
	if !isBackupFile(name) {
		return "", fmt.Errorf("invalid backup file: only .tar.gz / .sql.gz allowed")
	}
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".tar.gz"), ".sql.gz")
	if stem == "" {
		return "", fmt.Errorf("invalid backup file name")
	}
	return name, nil
}

func (s *Service) saveSettings(st Settings) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.settingsRepo.Set(settingsKey, string(raw))
}

func (s *Service) recordError(err error) {
	st, _ := s.loadSettings()
	st.LastError = err.Error()
	_ = s.saveSettings(st)
}

func (s *Service) effectiveFolder(st *Settings) string {
	if st == nil {
		s2, _ := s.loadSettings()
		st = &s2
	}
	f := st.Folder
	if f == "" {
		f = defaultFolder
	}
	return f
}

func (s *Service) applyRetention(folder string, days int) {
	if days <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	n, err := s.rclone.deleteOlderThan(ctx, folder, cutoff)
	if err != nil {
		slog.Warn("gdrive retention failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("gdrive retention applied", "deleted", n, "older_than_days", days)
	}
}

func (s *Service) notify(subject, body string) {
	if s.channelRepo == nil {
		return
	}
	channels, err := s.channelRepo.List()
	if err != nil {
		slog.Warn("gdrive: failed to list notification channels", "error", err)
		return
	}
	results, sendErr := notify.NewDispatcher(channels).SendWithResults(subject, body)
	if sendErr != nil {
		slog.Warn("gdrive: notification dispatch failed", "error", sendErr)
	}
	if err := notify.PersistDeliveryResults(context.Background(), s.receiptRepo, models.NotificationDeliverySourceBackup, results, time.Now().UTC()); err != nil {
		slog.Warn("gdrive: notification delivery receipt persistence failed", "error", err)
	}
}

func (s *Service) notifyFailure(subject, file string, err error) {
	settings, _ := s.loadSettings()
	if !settings.NotifyOnFailure {
		return
	}
	body := fmt.Sprintf("Dosya: %s\nHata: %s\nNe yapmalı: Bağlantıyı kontrol edin, rclone kurulu olduğundan emin olun, tekrar deneyin.", file, err.Error())
	s.notify(subject, body)
}

// NotifyBackupResult sends Telegram/notification on local backup completion.
func (s *Service) NotifyBackupResult(success bool, backupType, fileName, errMsg string) {
	settings, _ := s.loadSettings()
	if success && !settings.NotifyOnSuccess {
		return
	}
	if !success && !settings.NotifyOnFailure {
		return
	}
	if success {
		s.notify("Yerel yedek tamamlandı", fmt.Sprintf("Tür: %s\nDosya: %s", backupType, fileName))
	} else {
		s.notify("Yerel yedek başarısız",
			fmt.Sprintf("Tür: %s\nHata: %s", backupType, strings.TrimSpace(errMsg)))
	}
}
