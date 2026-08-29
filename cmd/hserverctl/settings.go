package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	settingsEndpoint            = "/api/settings"
	settingsPortableEndpoint    = "/api/settings/portable"
	settingsPortablePreviewPath = "/api/settings/portable/preview"
	settingsPortableImportPath  = "/api/settings/portable/import"
	settingsPortableSchema      = 1
	maxSettingsPortableBytes    = 128 << 10
	maxSettingsPortableKeys     = 12
	maxSettingsPortableWarnings = 32
)

// cliPortableSettingsBundle mirrors the public schema-v1 bundle. It is kept in
// the CLI package so the file boundary can reject malformed or out-of-contract
// input before it reaches the authenticated API.
type cliPortableSettingsBundle struct {
	SchemaVersion int               `json:"schema_version"`
	ExportedAt    time.Time         `json:"exported_at"`
	SourceVersion string            `json:"source_version"`
	Settings      map[string]string `json:"settings"`
	Warnings      []string          `json:"warnings,omitempty"`
}

type cliPortableSettingsImportRequest struct {
	Bundle    json.RawMessage `json:"bundle"`
	Confirmed bool            `json:"confirmed"`
}

var cliEditableSettingValidators = map[string]func(string) bool{
	"hostnameDisplay":         cliValidPortableLabel,
	"adminEmail":              cliValidPortableEmail,
	"notifyOnLogin":           cliValidPortableBool,
	"notifyOnError":           cliValidPortableBool,
	"notifyOnDeployment":      cliValidPortableBool,
	"webmail_url":             cliValidPortableHTTPURL,
	"mail_admin_url":          cliValidPortableHTTPURL,
	"mail_server_host":        cliValidPortableHostname,
	"mail_imap_port":          cliValidPortablePort,
	"mail_smtp_starttls_port": cliValidPortablePort,
	"mail_smtp_ssl_port":      cliValidPortablePort,
	"timezone":                cliValidPortableTimezone,
}

var cliEditableSettingKeys = []string{
	"hostnameDisplay",
	"adminEmail",
	"notifyOnLogin",
	"notifyOnError",
	"notifyOnDeployment",
	"webmail_url",
	"mail_admin_url",
	"mail_server_host",
	"mail_imap_port",
	"mail_smtp_starttls_port",
	"mail_smtp_ssl_port",
	"timezone",
}

func runSettings(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl settings list|get|set|delete|export|preview|import")
	}
	switch args[0] {
	case "list":
		return runSettingsList(ctx, client, args[1:], out)
	case "get":
		return runSettingsGet(ctx, client, args[1:], out)
	case "set":
		return runSettingsSet(ctx, client, args[1:], out)
	case "delete":
		return runSettingsDelete(ctx, client, args[1:], out)
	case "export":
		return runSettingsExport(ctx, client, args[1:], out)
	case "preview":
		return runSettingsPreview(ctx, client, args[1:], out)
	case "import":
		return runSettingsImport(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown settings command %q", args[0])
	}
}

func runSettingsList(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) != 0 {
		return errors.New("usage: hserverctl settings list")
	}
	return printRequest(ctx, client, out, http.MethodGet, settingsEndpoint, nil, true)
}

func runSettingsGet(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: hserverctl settings get KEY")
	}
	key, err := validateCLIEditableSettingKey(args[0])
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodGet, settingsEndpoint+"/"+url.PathEscape(key), nil, true)
}

func runSettingsSet(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	pairs := args
	if len(pairs) == 0 || len(pairs) > maxSettingsPortableKeys {
		return errors.New("usage: hserverctl settings set KEY=VALUE...")
	}
	payload := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		if strings.HasPrefix(pair, "-") {
			return errors.New("settings set accepts only KEY=VALUE arguments")
		}
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" || key != strings.TrimSpace(key) {
			return errors.New("invalid settings input; expected KEY=VALUE")
		}
		if _, exists := payload[key]; exists {
			return fmt.Errorf("duplicate editable setting %q", key)
		}
		if err := validateCLIEditableSetting(key, value); err != nil {
			return err
		}
		payload[key] = value
	}
	return printRequest(ctx, client, out, http.MethodPut, settingsEndpoint, payload, true)
}

func runSettingsDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("settings delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm editable setting deletion")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl settings delete --confirm KEY")
	}
	if !*confirmed {
		return errors.New("settings deletion requires explicit --confirm")
	}
	key, err := validateCLIEditableSettingKey(flags.Args()[0])
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodDelete, settingsEndpoint+"/"+url.PathEscape(key), nil, true)
}

func runSettingsExport(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("settings export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "", "write the portable configuration to a new protected file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || strings.TrimSpace(*output) == "" {
		return errors.New("usage: hserverctl settings export --output FILE")
	}
	outputPath, err := validateSettingsOutputPath(*output)
	if err != nil {
		return err
	}
	raw, err := client.request(ctx, http.MethodGet, settingsPortableEndpoint, nil, true)
	if err != nil {
		return err
	}
	if !json.Valid(raw) {
		return errors.New("server returned invalid portable configuration JSON")
	}
	if err := writeSettingsExportFile(outputPath, raw); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Wrote protected HServer portable settings export to %s\n", outputPath)
	return err
}

func runSettingsPreview(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("settings preview", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	filePath := flags.String("file", "", "portable configuration JSON file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || strings.TrimSpace(*filePath) == "" {
		return errors.New("usage: hserverctl settings preview --file FILE")
	}
	_, raw, err := readCLISettingsBundleFile(*filePath)
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodPost, settingsPortablePreviewPath, json.RawMessage(raw), true)
}

func runSettingsImport(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("settings import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	filePath := flags.String("file", "", "portable configuration JSON file")
	confirmed := flags.Bool("confirm", false, "confirm portable configuration import")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || strings.TrimSpace(*filePath) == "" {
		return errors.New("usage: hserverctl settings import --file FILE --confirm")
	}
	if !*confirmed {
		return errors.New("portable settings import requires explicit --confirm")
	}
	_, raw, err := readCLISettingsBundleFile(*filePath)
	if err != nil {
		return err
	}
	payload := cliPortableSettingsImportRequest{Bundle: json.RawMessage(raw), Confirmed: true}
	return printRequest(ctx, client, out, http.MethodPost, settingsPortableImportPath, payload, true)
}

func validateCLIEditableSettingKey(key string) (string, error) {
	if key != strings.TrimSpace(key) || key == "" {
		return "", errors.New("editable setting key must not be empty or contain surrounding whitespace")
	}
	if _, ok := cliEditableSettingValidators[key]; !ok {
		// Do not echo arbitrary key input: callers may accidentally place a
		// credential in a malformed KEY position, and setting errors must stay
		// non-secret even before the API client presentation layer runs.
		return "", errors.New("unknown editable setting")
	}
	return key, nil
}

func validateCLIEditableSetting(key, value string) error {
	if _, err := validateCLIEditableSettingKey(key); err != nil {
		return err
	}
	if !cliValidPortableValue(value) || !cliEditableSettingValidators[key](value) {
		return fmt.Errorf("invalid value for editable setting %q", key)
	}
	return nil
}

func cliValidPortableValue(value string) bool {
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

func cliValidPortableLabel(value string) bool {
	return len(value) <= 128
}

func cliValidPortableBool(value string) bool {
	return value == "true" || value == "false"
}

func cliValidPortableEmail(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 254 {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func cliValidPortableHTTPURL(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil
}

func cliValidPortableHostname(value string) bool {
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

func cliValidPortablePort(value string) bool {
	if value == "" {
		return true
	}
	port, err := strconv.Atoi(value)
	return err == nil && strconv.Itoa(port) == value && port >= 1 && port <= 65535
}

func cliValidPortableTimezone(value string) bool {
	if value == "" {
		return true
	}
	_, err := time.LoadLocation(value)
	return err == nil
}

func readCLISettingsBundleFile(path string) (cliPortableSettingsBundle, []byte, error) {
	data, err := readCLISettingsFile(path)
	if err != nil {
		return cliPortableSettingsBundle{}, nil, err
	}
	bundle, err := decodeCLISettingsBundle(data)
	if err != nil {
		return cliPortableSettingsBundle{}, nil, err
	}
	return bundle, data, nil
}

func readCLISettingsFile(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "-" {
		return nil, errors.New("portable settings file must be a named regular file")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect portable settings file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("portable settings file must be a regular file and not a symlink")
	}
	if info.Size() > maxSettingsPortableBytes {
		return nil, fmt.Errorf("portable settings file exceeds %d bytes", maxSettingsPortableBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open portable settings file: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect portable settings file: %w", err)
	}
	if !openedInfo.Mode().IsRegular() {
		return nil, errors.New("portable settings file must be a regular file and not a symlink")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSettingsPortableBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read portable settings file: %w", err)
	}
	if len(data) > maxSettingsPortableBytes {
		return nil, fmt.Errorf("portable settings file exceeds %d bytes", maxSettingsPortableBytes)
	}
	return data, nil
}

func decodeCLISettingsBundle(data []byte) (cliPortableSettingsBundle, error) {
	var bundle cliPortableSettingsBundle
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return cliPortableSettingsBundle{}, errors.New("invalid portable settings JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return cliPortableSettingsBundle{}, errors.New("portable settings JSON contains trailing data")
	}
	if err := validateCLISettingsBundle(bundle); err != nil {
		return cliPortableSettingsBundle{}, err
	}
	return bundle, nil
}

func validateCLISettingsBundle(bundle cliPortableSettingsBundle) error {
	if bundle.SchemaVersion != settingsPortableSchema || bundle.ExportedAt.IsZero() || strings.TrimSpace(bundle.SourceVersion) == "" || !cliValidPortableValue(bundle.SourceVersion) {
		return errors.New("unsupported portable settings schema")
	}
	if len(bundle.Settings) == 0 {
		return errors.New("portable settings file has no importable settings")
	}
	if len(bundle.Settings) > maxSettingsPortableKeys || len(bundle.Warnings) > maxSettingsPortableWarnings {
		return errors.New("invalid portable settings file")
	}
	for _, warning := range bundle.Warnings {
		if len(warning) > 256 || !cliValidPortableValue(warning) {
			return errors.New("invalid portable settings warning")
		}
	}
	for key, value := range bundle.Settings {
		if err := validateCLIEditableSetting(key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateSettingsOutputPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "-" {
		return "", errors.New("settings export requires a named output file; stdout output is disabled")
	}
	path = filepath.Clean(path)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("settings export output must be a new regular file and not a symlink")
		}
		return "", fmt.Errorf("settings export output file already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect settings export output %s: %w", path, err)
	}
	return path, nil
}

func writeSettingsExportFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create settings export directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".hserver-settings-export-*")
	if err != nil {
		return fmt.Errorf("create temporary settings export: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect temporary settings export: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary settings export: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary settings export: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary settings export: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if _, inspectErr := os.Lstat(path); inspectErr == nil {
			return fmt.Errorf("settings export output file already exists: %s", path)
		}
		return fmt.Errorf("publish settings export: %w", err)
	}
	return nil
}
