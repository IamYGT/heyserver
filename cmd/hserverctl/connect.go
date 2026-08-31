package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runConnect(ctx context.Context, args []string, contextsPath, defaultServer, defaultTokenFile string, timeout time.Duration, input io.Reader, out, promptOut io.Writer) error {
	flags := flag.NewFlagSet("connect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("server", defaultServer, "Heyserver base URL")
	tokenFile := flags.String("token-file", defaultTokenFile, "protected bearer-token file")
	email := flags.String("email", "", "administrator email")
	passwordFile := flags.String("password-file", "", "file containing the password")
	totpFile := flags.String("totp-file", "", "optional file containing the current TOTP code")
	if err := flags.Parse(args); err != nil {
		return err
	}
	positional := flags.Args()
	if len(positional) != 1 || strings.TrimSpace(*server) == "" || strings.TrimSpace(*email) == "" {
		return errors.New("usage: hserverctl connect --server URL --email EMAIL [--password-file PATH] [--totp-file PATH] [--token-file PATH] NAME")
	}
	name := positional[0]
	if err := validateContextName(name); err != nil {
		return err
	}
	if strings.TrimSpace(contextsPath) == "" {
		return errors.New("context file path is unavailable; set HOME, XDG_CONFIG_HOME, or HSERVER_CONTEXT_FILE")
	}
	client, err := newAPIClient(*server, "", timeout)
	if err != nil {
		return err
	}
	selectedTokenFile := strings.TrimSpace(*tokenFile)
	if selectedTokenFile == "" {
		selectedTokenFile = filepath.Join(filepath.Dir(contextsPath), "tokens", name)
	}
	selectedTokenFile, err = filepath.Abs(selectedTokenFile)
	if err != nil {
		return fmt.Errorf("resolve context token file: %w", err)
	}
	config, _, err := readContextsConfig(contextsPath)
	if err != nil {
		return err
	}
	if _, exists := config.Contexts[name]; exists {
		return fmt.Errorf("Heyserver context %q already exists; use it or remove it before reconnecting", name)
	}
	if _, err := os.Lstat(selectedTokenFile); err == nil {
		return fmt.Errorf("token file already exists at %s; choose another context name or --token-file path", selectedTokenFile)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect token file %s: %w", selectedTokenFile, err)
	}

	token, err := authenticateWithInput(ctx, client, *email, *passwordFile, *totpFile, input, promptOut)
	if err != nil {
		return err
	}
	verifiedClient, err := newAPIClient(client.baseURL.String(), token, timeout)
	if err != nil {
		return err
	}
	raw, err := verifiedClient.request(ctx, http.MethodGet, "/api/auth/me", nil, true)
	if err != nil {
		return fmt.Errorf("verify authenticated account: %w", err)
	}
	var account struct {
		ID   int64  `json:"id"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(raw, &account); err != nil {
		return fmt.Errorf("decode authenticated account: %w", err)
	}
	if account.ID <= 0 || strings.TrimSpace(account.Role) == "" {
		return errors.New("authenticated account response is missing identity or role")
	}
	if err := writeTokenFile(selectedTokenFile, token, false); err != nil {
		return err
	}
	config.Contexts[name] = cliContext{Server: client.baseURL.String(), TokenFile: selectedTokenFile}
	config.Current = name
	if err := writeContextsConfig(contextsPath, config); err != nil {
		if removeErr := os.Remove(selectedTokenFile); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("write context: %v; remove unreferenced token: %w", err, removeErr)
		}
		return err
	}

	fmt.Fprintf(out, "Connected Heyserver context %q to %s\n", name, client.baseURL.String())
	fmt.Fprintf(out, "Current context: %s\n", name)
	fmt.Fprintf(out, "Authenticated role: %s\n", strings.TrimSpace(account.Role))
	fmt.Fprintf(out, "Token file: %s\n", selectedTokenFile)
	fmt.Fprintln(out, "Next: hserverctl doctor")
	fmt.Fprintln(out, "Open control center: hserverctl ui")
	return nil
}
