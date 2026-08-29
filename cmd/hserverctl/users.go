package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const maxPanelUserPasswordBytes = 128

var panelUserEmailPattern = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

type userMutationOptions struct {
	Confirm      bool
	Email        string
	Name         string
	Role         string
	PasswordFile string
	visited      map[string]bool
}

func runUsers(ctx context.Context, client *apiClient, args []string, input io.Reader, out, promptOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl users list|create|update|delete")
	}
	switch args[0] {
	case "list":
		return runUsersList(ctx, client, args[1:], out)
	case "create":
		return runUsersCreate(ctx, client, args[1:], input, out, promptOut)
	case "update":
		return runUsersUpdate(ctx, client, args[1:], out)
	case "delete":
		return runUsersDelete(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown users command %q", args[0])
	}
}

func runUsersList(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("users list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	limit := flags.Int("limit", 20, "maximum users to return")
	offset := flags.Int("offset", 0, "result offset")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl users list [--limit 1-200] [--offset N]")
	}
	if *limit < 1 || *limit > 200 {
		return errors.New("user limit must be between 1 and 200")
	}
	if *offset < 0 {
		return errors.New("user offset must not be negative")
	}
	query := url.Values{}
	query.Set("limit", strconv.Itoa(*limit))
	query.Set("offset", strconv.Itoa(*offset))
	return printRequest(ctx, client, out, http.MethodGet, "/api/users?"+query.Encode(), nil, true)
}

func runUsersCreate(ctx context.Context, client *apiClient, args []string, input io.Reader, out, promptOut io.Writer) error {
	options, positional, err := parseUserMutationFlags("users create", args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return errors.New("usage: hserverctl users create --confirm --email EMAIL --name NAME [--role admin|manager|viewer] [--password-file PATH]")
	}
	if !options.Confirm {
		return errors.New("panel-user creation requires explicit --confirm")
	}
	email, err := validatePanelUserEmail(options.Email)
	if err != nil {
		return err
	}
	name, err := validatePanelUserText("name", options.Name, 100, true)
	if err != nil {
		return err
	}
	payload := map[string]any{"email": email, "name": name}
	if options.visited["role"] {
		role, roleErr := validatePanelUserRole(options.Role)
		if roleErr != nil {
			return roleErr
		}
		payload["role"] = role
	}
	password, err := readPanelUserPassword(options.PasswordFile, input, promptOut)
	if err != nil {
		return err
	}
	payload["password"] = password
	return printRequest(ctx, client, out, http.MethodPost, "/api/users", payload, true)
}

func runUsersUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseUserMutationFlags("users update", args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: hserverctl users update --confirm [--email EMAIL] [--name NAME] [--role admin|manager|viewer] [--password-file PATH] ID")
	}
	if !options.Confirm {
		return errors.New("panel-user update requires explicit --confirm")
	}
	id, err := positivePanelUserID(positional[0])
	if err != nil {
		return err
	}
	payload := make(map[string]any)
	if options.visited["email"] {
		payload["email"], err = validatePanelUserEmail(options.Email)
		if err != nil {
			return err
		}
	}
	if options.visited["name"] {
		payload["name"], err = validatePanelUserText("name", options.Name, 100, true)
		if err != nil {
			return err
		}
	}
	if options.visited["role"] {
		payload["role"], err = validatePanelUserRole(options.Role)
		if err != nil {
			return err
		}
	}
	if options.visited["password-file"] {
		if strings.TrimSpace(options.PasswordFile) == "" {
			return errors.New("password file path must not be empty")
		}
		password, passwordErr := readPanelUserPasswordFile(options.PasswordFile)
		if passwordErr != nil {
			return passwordErr
		}
		payload["password"] = password
	}
	if len(payload) == 0 {
		return errors.New("panel-user update requires at least one changed field")
	}
	endpoint := "/api/users/" + strconv.FormatInt(id, 10)
	return printRequest(ctx, client, out, http.MethodPut, endpoint, payload, true)
}

func runUsersDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("users delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm panel-user deletion")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl users delete --confirm ID")
	}
	if !*confirmed {
		return errors.New("panel-user deletion requires explicit --confirm")
	}
	id, err := positivePanelUserID(flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := "/api/users/" + strconv.FormatInt(id, 10)
	if _, err := client.request(ctx, http.MethodDelete, endpoint, nil, true); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "{\n  \"status\": \"deleted\",\n  \"user_id\": %d\n}\n", id)
	return err
}

func parseUserMutationFlags(name string, args []string) (*userMutationOptions, []string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := &userMutationOptions{}
	flags.BoolVar(&options.Confirm, "confirm", false, "confirm panel-user mutation")
	flags.StringVar(&options.Email, "email", "", "panel-user email")
	flags.StringVar(&options.Name, "name", "", "panel-user display name")
	flags.StringVar(&options.Role, "role", "", "admin, manager, or viewer")
	flags.StringVar(&options.PasswordFile, "password-file", "", "protected file containing the password")
	if err := flags.Parse(args); err != nil {
		return nil, nil, err
	}
	options.visited = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { options.visited[item.Name] = true })
	return options, flags.Args(), nil
}

func readPanelUserPassword(path string, input io.Reader, promptOut io.Writer) (string, error) {
	if strings.TrimSpace(path) != "" {
		return readPanelUserPasswordFile(path)
	}
	password, err := readInteractiveSecret(input, promptOut, "New panel-user password: ", "--password-file", maxPanelUserPasswordBytes)
	if err != nil {
		return "", err
	}
	return validatePanelUserPassword(password)
}

func readPanelUserPasswordFile(path string) (string, error) {
	password, err := readSecretFile(path, maxPanelUserPasswordBytes+2)
	if err != nil {
		return "", fmt.Errorf("read panel-user password file: %w", err)
	}
	return validatePanelUserPassword(password)
}

func validatePanelUserPassword(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("panel-user password must be at least 8 characters")
	}
	if len(password) > maxPanelUserPasswordBytes {
		return "", fmt.Errorf("panel-user password must be at most %d characters", maxPanelUserPasswordBytes)
	}
	return password, nil
}

func validatePanelUserEmail(email string) (string, error) {
	email, err := validatePanelUserText("email", email, 254, true)
	if err != nil {
		return "", err
	}
	if !panelUserEmailPattern.MatchString(email) {
		return "", errors.New("panel-user email has an invalid format")
	}
	return email, nil
}

func validatePanelUserText(name, value string, maximum int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("panel-user %s is required", name)
	}
	if len(value) > maximum {
		return "", fmt.Errorf("panel-user %s must be at most %d characters", name, maximum)
	}
	return value, nil
}

func validatePanelUserRole(role string) (string, error) {
	role = strings.TrimSpace(role)
	switch role {
	case "admin", "manager", "viewer":
		return role, nil
	default:
		return "", errors.New("panel-user role must be admin, manager, or viewer")
	}
}

func positivePanelUserID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
		return 0, errors.New("panel-user ID must be a positive canonical integer")
	}
	return id, nil
}
