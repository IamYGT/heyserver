package api

import (
	"net/http"
	"os"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/services/backup"
	"github.com/IamYGT/heyserver/internal/services/gdrive"
	"github.com/IamYGT/heyserver/internal/services/snapshot"
	"github.com/IamYGT/heyserver/internal/store"
)

var gdriveSvc *gdrive.Service
var snapshotSvc *snapshot.Service

func gdriveServiceUnavailable(w http.ResponseWriter) bool {
	if gdriveSvc != nil {
		return false
	}
	jsonError(w, http.StatusServiceUnavailable, "Google Drive service not initialized")
	return true
}

func snapshotServiceUnavailable(w http.ResponseWriter) bool {
	if snapshotSvc != nil {
		return false
	}
	jsonError(w, http.StatusServiceUnavailable, "snapshot service not initialized")
	return true
}

// InitBackupServices wires backup + Google Drive integration.
func InitBackupServices(cfg *config.Config, settingsRepo *store.SettingsRepository, channelRepo *store.NotificationChannelRepository) (*gdrive.Service, *snapshot.Service) {
	localDir := os.Getenv("BACKUP_DIR")
	if localDir == "" {
		localDir = cfg.DataDir + "/backups"
	}
	backupSvc = backup.NewConfigured(localDir, cfg.VhostsRoot)

	gdriveSvc = gdrive.New(
		cfg.DataDir,
		cfg.Port,
		cfg.GDriveClientID,
		cfg.GDriveClientSecret,
		cfg.GDriveRedirectURI,
		cfg.RcloneBin,
		settingsRepo,
		channelRepo,
	)

	if gdriveSvc != nil {
		gdrive.WarnConfig(cfg.GDriveClientID, cfg.GDriveRedirectURI)
		gdriveSvc.SetJobTracker(backupSvc)
		backupSvc.SetAfterBackupHook(func(localPath string) {
			gdriveSvc.UploadIfEnabled(localPath)
		})
		backupSvc.SetNotifyHook(func(success bool, backupType, fileName, errMsg string) {
			gdriveSvc.NotifyBackupResult(success, backupType, fileName, errMsg)
		})
		gdriveSvc.StartHealthProbe(gdriveServerRedirectURI(cfg), 0)
	}

	snapshotSvc = snapshot.NewWithS3(
		cfg.DataDir,
		cfg.VhostsRoot,
		localDir,
		cfg.Port,
		cfg.ResticBin,
		cfg.RcloneBin,
		cfg.ResticPassword,
		gdriveSvc,
		snapshot.S3Config{
			Endpoint:      cfg.S3Endpoint,
			Bucket:        cfg.S3Bucket,
			Region:        cfg.S3Region,
			AccessKeyFile: cfg.S3AccessKeyFile,
			SecretKeyFile: cfg.S3SecretKeyFile,
			BucketLookup:  cfg.S3BucketLookup,
		},
		backupSvc,
	)
	snapshotSvc.WarmRepoCache(gdriveServerRedirectURI(cfg))
	return gdriveSvc, snapshotSvc
}
