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
	"strings"
	"time"
)

var pm2ProcessIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)

func runPM2(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl pm2 list|get|logs|action|save")
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("pm2 list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl pm2 list [--node NODE]")
		}
		endpoint := "/api/pm2/processes"
		if strings.TrimSpace(*node) != "" {
			endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/pm2"
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "get":
		if len(args) != 2 {
			return errors.New("usage: hserverctl pm2 get PROCESS")
		}
		process, err := validatePM2ProcessIdentity(args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/pm2/processes/"+url.PathEscape(process), nil, true)
	case "logs":
		flags := flag.NewFlagSet("pm2 logs", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		lines := flags.Int("lines", 200, "number of latest lines")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 1 {
			return errors.New("usage: hserverctl pm2 logs [--node NODE] [--lines N] PROCESS")
		}
		process, err := validatePM2ProcessIdentity(flags.Args()[0])
		if err != nil {
			return err
		}
		maximum := 5000
		if strings.TrimSpace(*node) != "" {
			maximum = 500
		}
		if *lines < 1 || *lines > maximum {
			return fmt.Errorf("PM2 log line count must be between 1 and %d", maximum)
		}
		endpoint := "/api/pm2/processes/" + url.PathEscape(process) + "/logs"
		if strings.TrimSpace(*node) != "" {
			endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/pm2/" + url.PathEscape(process) + "/logs"
		}
		query := url.Values{"lines": []string{fmt.Sprint(*lines)}}
		return printRequest(ctx, client, out, http.MethodGet, endpoint+"?"+query.Encode(), nil, true)
	case "action":
		flags := flag.NewFlagSet("pm2 action", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		confirmed := flags.Bool("confirm", false, "confirm the PM2 mutation")
		wait := flags.Duration("wait", 7*time.Minute, "maximum action wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 2 {
			return errors.New("usage: hserverctl pm2 action --confirm [--node NODE] [--wait DURATION] PROCESS ACTION")
		}
		if !*confirmed {
			return errors.New("PM2 action requires explicit --confirm")
		}
		if *wait <= 0 {
			return errors.New("PM2 action wait must be greater than zero")
		}
		process, err := validatePM2ProcessIdentity(flags.Args()[0])
		if err != nil {
			return err
		}
		remote := strings.TrimSpace(*node) != ""
		action := flags.Args()[1]
		if err := validatePM2Action(action, remote); err != nil {
			return err
		}
		endpoint := "/api/pm2/processes/" + url.PathEscape(process) + "/" + url.PathEscape(action)
		if remote {
			endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/pm2/" +
				url.PathEscape(process) + "/actions/" + url.PathEscape(action)
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
	case "save":
		flags := flag.NewFlagSet("pm2 save", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm PM2 process-list persistence")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl pm2 save --confirm")
		}
		if !*confirmed {
			return errors.New("PM2 save requires explicit --confirm")
		}
		return printRequest(ctx, client, out, http.MethodPost, "/api/pm2/save", nil, true)
	default:
		return fmt.Errorf("unknown pm2 command %q", args[0])
	}
}

func validatePM2ProcessIdentity(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !pm2ProcessIdentityPattern.MatchString(value) {
		return "", errors.New("PM2 process identity must be a numeric ID or portable process name")
	}
	return value, nil
}

func validatePM2Action(action string, remote bool) error {
	switch action {
	case "start", "stop", "restart", "reload":
		return nil
	case "delete":
		if !remote {
			return nil
		}
	}
	if remote {
		return fmt.Errorf("unsupported managed-node PM2 action %q", action)
	}
	return fmt.Errorf("unsupported local PM2 action %q", action)
}
