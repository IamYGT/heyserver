package settings

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const PortableSchemaVersion = 1

var (
	ErrPortableSchema       = errors.New("unsupported portable configuration schema")
	ErrPortableSetting      = errors.New("invalid portable configuration setting")
	ErrPortableEmpty        = errors.New("portable configuration has no importable settings")
	ErrPortableConfirmation = errors.New("explicit portable configuration import confirmation is required")
	ErrEditableSetting      = errors.New("invalid editable setting")
)

type PortableBundle struct {
	SchemaVersion int               `json:"schema_version"`
	ExportedAt    time.Time         `json:"exported_at"`
	SourceVersion string            `json:"source_version"`
	Settings      map[string]string `json:"settings"`
	Warnings      []string          `json:"warnings,omitempty"`
}

type PortableChange struct {
	Key      string `json:"key"`
	Current  string `json:"current"`
	Proposed string `json:"proposed"`
}

type PortablePreview struct {
	SchemaVersion int              `json:"schema_version"`
	ImportedKeys  int              `json:"imported_keys"`
	ChangedKeys   int              `json:"changed_keys"`
	UnchangedKeys int              `json:"unchanged_keys"`
	Changes       []PortableChange `json:"changes"`
}

var portableSettingValidators = map[string]func(string) bool{
	"hostnameDisplay":         validPortableLabel,
	"adminEmail":              validPortableEmail,
	"notifyOnLogin":           validPortableBool,
	"notifyOnError":           validPortableBool,
	"notifyOnDeployment":      validPortableBool,
	"webmail_url":             validPortableHTTPURL,
	"mail_admin_url":          validPortableHTTPURL,
	"mail_server_host":        validPortableHostname,
	"mail_imap_port":          validPortablePort,
	"mail_smtp_starttls_port": validPortablePort,
	"mail_smtp_ssl_port":      validPortablePort,
	"timezone":                validPortableTimezone,
}

// EditableSettings returns only the validated installation-portable settings
// that the generic panel settings form is allowed to read and mutate. Internal
// service records can contain credentials and must stay behind their dedicated
// masked APIs.
func (s *Service) EditableSettings() (map[string]string, error) {
	return s.EditableSettingsContext(context.Background())
}

func (s *Service) EditableSettingsContext(ctx context.Context) (map[string]string, error) {
	all, err := s.repo.GetAllContext(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	for _, setting := range all {
		validator, editable := portableSettingValidators[setting.Key]
		if editable && validPortableValue(setting.Value) && validator(setting.Value) {
			result[setting.Key] = setting.Value
		}
	}
	return result, nil
}

func IsEditableSetting(key string) bool {
	_, editable := portableSettingValidators[key]
	return editable
}

func ValidateEditableSettings(values map[string]string) error {
	if len(values) == 0 || len(values) > len(portableSettingValidators) {
		return ErrEditableSetting
	}
	for key, value := range values {
		validator, editable := portableSettingValidators[key]
		if !editable || !validPortableValue(value) || !validator(value) {
			return fmt.Errorf("%w: %s", ErrEditableSetting, key)
		}
	}
	return nil
}

func (s *Service) ExportPortable() (PortableBundle, error) {
	all, err := s.repo.GetAll()
	if err != nil {
		return PortableBundle{}, err
	}
	bundle := PortableBundle{
		SchemaVersion: PortableSchemaVersion,
		ExportedAt:    s.now().UTC(),
		SourceVersion: s.version,
		Settings:      make(map[string]string),
	}
	for _, setting := range all {
		validator, portable := portableSettingValidators[setting.Key]
		if !portable {
			continue
		}
		if !validPortableValue(setting.Value) || !validator(setting.Value) {
			bundle.Warnings = append(bundle.Warnings, fmt.Sprintf("Skipped invalid portable setting: %s", setting.Key))
			continue
		}
		bundle.Settings[setting.Key] = setting.Value
	}
	sort.Strings(bundle.Warnings)
	return bundle, nil
}

func (s *Service) PreviewPortable(bundle PortableBundle) (PortablePreview, error) {
	if err := validatePortableBundle(bundle); err != nil {
		return PortablePreview{}, err
	}
	all, err := s.repo.GetAll()
	if err != nil {
		return PortablePreview{}, err
	}
	current := make(map[string]string, len(all))
	for _, setting := range all {
		current[setting.Key] = setting.Value
	}
	keys := make([]string, 0, len(bundle.Settings))
	for key := range bundle.Settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	preview := PortablePreview{SchemaVersion: PortableSchemaVersion, ImportedKeys: len(keys), Changes: []PortableChange{}}
	for _, key := range keys {
		proposed := bundle.Settings[key]
		if current[key] == proposed {
			preview.UnchangedKeys++
			continue
		}
		preview.ChangedKeys++
		preview.Changes = append(preview.Changes, PortableChange{Key: key, Current: current[key], Proposed: proposed})
	}
	return preview, nil
}

func (s *Service) ImportPortable(bundle PortableBundle, confirmed bool) (PortablePreview, error) {
	if !confirmed {
		return PortablePreview{}, ErrPortableConfirmation
	}
	preview, err := s.PreviewPortable(bundle)
	if err != nil {
		return PortablePreview{}, err
	}
	if preview.ChangedKeys == 0 {
		return preview, nil
	}
	if err := s.repo.SetMany(bundle.Settings); err != nil {
		return PortablePreview{}, err
	}
	return preview, nil
}

func validatePortableBundle(bundle PortableBundle) error {
	if bundle.SchemaVersion != PortableSchemaVersion || bundle.ExportedAt.IsZero() || strings.TrimSpace(bundle.SourceVersion) == "" || !validPortableValue(bundle.SourceVersion) {
		return ErrPortableSchema
	}
	if len(bundle.Settings) == 0 {
		return ErrPortableEmpty
	}
	if len(bundle.Settings) > len(portableSettingValidators) || len(bundle.Warnings) > 32 {
		return ErrPortableSetting
	}
	for _, warning := range bundle.Warnings {
		if len(warning) > 256 || !validPortableValue(warning) {
			return ErrPortableSetting
		}
	}
	for key, value := range bundle.Settings {
		validator, allowed := portableSettingValidators[key]
		if !allowed || !validPortableValue(value) || !validator(value) {
			return fmt.Errorf("%w: %s", ErrPortableSetting, key)
		}
	}
	return nil
}

func validPortableValue(value string) bool {
	if len(value) > 2048 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validPortableLabel(value string) bool {
	return len(value) <= 128
}

func validPortableBool(value string) bool {
	return value == "true" || value == "false"
}

func validPortableEmail(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 254 {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func validPortableHTTPURL(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil
}

func validPortableHostname(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 253 || strings.Contains(value, "..") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validPortablePort(value string) bool {
	if value == "" {
		return true
	}
	port, err := strconv.Atoi(value)
	return err == nil && strconv.Itoa(port) == value && port >= 1 && port <= 65535
}

func validPortableTimezone(value string) bool {
	if value == "" {
		return true
	}
	_, err := time.LoadLocation(value)
	return err == nil
}
