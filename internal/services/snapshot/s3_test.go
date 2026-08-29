package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCredential(t *testing.T, dir, name, value string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(value+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func validS3Config(t *testing.T) S3Config {
	t.Helper()
	dir := t.TempDir()
	return S3Config{
		Endpoint:      "https://objects.example.com",
		Bucket:        "hserver-backups",
		Region:        "eu-central-1",
		AccessKeyFile: writeCredential(t, dir, "access-key", "ACCESS_EXAMPLE", 0o600),
		SecretKeyFile: writeCredential(t, dir, "secret-key", "SECRET_EXAMPLE", 0o600),
		BucketLookup:  "path",
	}
}

func TestS3ConfigBuildsProviderSpecificResticRunner(t *testing.T) {
	s3 := validS3Config(t)
	service := NewWithS3(t.TempDir(), "/srv/www", t.TempDir(), 0, "restic", "rclone", "restic-password", nil, s3, nil)
	runner, err := service.runner(Settings{Destination: DestinationS3, RepoFolder: "tenant/snapshots"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := runner.repository(), "s3:https://objects.example.com/hserver-backups/tenant/snapshots"; got != want {
		t.Fatalf("repository=%q want=%q", got, want)
	}
	env := strings.Join(runner.env(), "\n")
	for _, required := range []string{
		"RESTIC_PASSWORD=restic-password",
		"AWS_ACCESS_KEY_ID=ACCESS_EXAMPLE",
		"AWS_SECRET_ACCESS_KEY=SECRET_EXAMPLE",
		"AWS_DEFAULT_REGION=eu-central-1",
	} {
		if !strings.Contains(env, required) {
			t.Fatalf("missing environment entry %q", required)
		}
	}
	if strings.Contains(env, "RCLONE_CONFIG=") {
		t.Fatal("S3 runner inherited Google Drive rclone configuration")
	}
	args := runner.withGlobalOpts("snapshots", "--json")
	if strings.Join(args, " ") != "-o s3.bucket-lookup=path snapshots --json" {
		t.Fatalf("args=%v", args)
	}
}

func TestS3DestinationStatesDistinguishConfigurationFailures(t *testing.T) {
	settings := Settings{Destination: DestinationS3, RepoFolder: defaultRepoFolder}
	service := NewWithS3(t.TempDir(), "", "", 0, "", "", "password", nil, S3Config{}, nil)
	state, _ := service.destinationState(settings, "")
	if state != DestinationNotConfigured {
		t.Fatalf("empty configuration state=%q", state)
	}

	service.s3 = S3Config{Endpoint: "https://objects.example.com"}
	state, message := service.destinationState(settings, "")
	if state != DestinationUnavailable || !strings.Contains(message, "HSERVER_S3_BUCKET") {
		t.Fatalf("partial configuration state=%q message=%q", state, message)
	}

	service.s3 = validS3Config(t).normalized()
	state, message = service.destinationState(settings, "")
	if state != DestinationHealthy || !strings.Contains(message, "credential files are ready") {
		t.Fatalf("valid configuration state=%q message=%q", state, message)
	}
}

func TestS3ConfigRequiresEncryptedTransportExceptLoopback(t *testing.T) {
	config := validS3Config(t)
	config.Endpoint = "http://objects.example.com"
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "requires HTTPS") {
		t.Fatalf("remote HTTP error=%v", err)
	}
	config.Endpoint = "http://127.0.0.1:9000"
	if err := config.validate(); err != nil {
		t.Fatalf("loopback MinIO rejected: %v", err)
	}
}

func TestS3CredentialFilesRejectSymlinksAndBroadPermissions(t *testing.T) {
	config := validS3Config(t)
	if err := os.Chmod(config.AccessKeyFile, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "group or other") {
		t.Fatalf("broad permission error=%v", err)
	}

	dir := t.TempDir()
	target := writeCredential(t, dir, "target", "ACCESS_EXAMPLE", 0o600)
	symlink := filepath.Join(dir, "access-link")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	config = validS3Config(t)
	config.AccessKeyFile = symlink
	if err := config.validate(); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("symlink error=%v", err)
	}
}

func TestS3CredentialReadersRequireOwnerReadBit(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      os.FileMode
		wantError bool
	}{
		{name: "owner read only", mode: 0o400},
		{name: "owner read write", mode: 0o600},
		{name: "no permissions", mode: 0o000, wantError: true},
		{name: "owner write only", mode: 0o200, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validS3Config(t)
			if err := os.Chmod(config.AccessKeyFile, test.mode); err != nil {
				t.Fatal(err)
			}

			_, directErr := readProtectedCredential(config.AccessKeyFile, "HSERVER_S3_ACCESS_KEY_FILE")
			_, contextErr := readProtectedCredentialContext(context.Background(), osSnapshotReadinessFileReader{}, config.AccessKeyFile, "HSERVER_S3_ACCESS_KEY_FILE")
			if test.wantError {
				if directErr == nil || contextErr == nil {
					t.Fatalf("mode=%#o directErr=%v contextErr=%v; both readers must reject missing owner-read", test.mode, directErr, contextErr)
				}
				if !strings.Contains(directErr.Error(), "owner") || !strings.Contains(contextErr.Error(), "owner") {
					t.Fatalf("mode=%#o directErr=%v contextErr=%v; want owner-read error", test.mode, directErr, contextErr)
				}
				return
			}
			if directErr != nil || contextErr != nil {
				t.Fatalf("mode=%#o directErr=%v contextErr=%v; owner-readable file must be accepted", test.mode, directErr, contextErr)
			}
		})
	}
}

func TestS3RunnerDoesNotReturnCredentialValuesInErrors(t *testing.T) {
	config := validS3Config(t)
	secret, err := readProtectedCredential(config.SecretKeyFile, "HSERVER_S3_SECRET_KEY_FILE")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(config.SecretKeyFile, 0o644); err != nil {
		t.Fatal(err)
	}
	service := NewWithS3(t.TempDir(), "", "", 0, "", "", "password", nil, config, nil)
	_, err = service.runner(Settings{Destination: DestinationS3, RepoFolder: defaultRepoFolder})
	if !errors.Is(err, ErrDestinationUnavailable) {
		t.Fatalf("error=%v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("credential value leaked through error")
	}
}

func TestS3StatusDoesNotClaimHealthyFromConfigurationAlone(t *testing.T) {
	dir := t.TempDir()
	restic := filepath.Join(dir, "restic")
	script := `#!/bin/sh
if [ "$1" = "version" ]; then
  echo "restic 0.test"
  exit 0
fi
echo "dial tcp: connection refused" >&2
exit 1
`
	if err := os.WriteFile(restic, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	service := NewWithS3(dir, "", "", 0, restic, "", "password", nil, validS3Config(t), nil)
	if err := service.UpdateSettings(SettingsUpdate{
		Destination: DestinationS3, RepoFolder: defaultRepoFolder,
		KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 6,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status("", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if status.DestinationStatus != DestinationUnavailable || !strings.Contains(status.DestinationMessage, "remote repository probe failed") {
		t.Fatalf("status=%q message=%q", status.DestinationStatus, status.DestinationMessage)
	}
}

func TestS3StatusAcceptsObservedUninitializedRepository(t *testing.T) {
	dir := t.TempDir()
	restic := filepath.Join(dir, "restic")
	script := `#!/bin/sh
if [ "$1" = "version" ]; then exit 0; fi
echo "Fatal: unable to open config file: The specified key does not exist" >&2
exit 1
`
	if err := os.WriteFile(restic, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	service := NewWithS3(dir, "", "", 0, restic, "", "password", nil, validS3Config(t), nil)
	if err := service.UpdateSettings(SettingsUpdate{
		Destination: DestinationS3, RepoFolder: defaultRepoFolder,
		KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 6,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status("", false, true)
	if err != nil {
		t.Fatal(err)
	}
	if status.DestinationStatus != DestinationHealthy || status.RepoInitialized {
		t.Fatalf("status=%q initialized=%v message=%q", status.DestinationStatus, status.RepoInitialized, status.DestinationMessage)
	}
}

func TestRepositoryProbeDoesNotMistakeWrongPasswordForMissingRepository(t *testing.T) {
	err := errors.New("Fatal: unable to open config file: crypto: wrong password or no key found")
	if isRepositoryUninitializedError(err) {
		t.Fatal("wrong repository password was classified as an uninitialized repository")
	}
}
