package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	systemInfoEndpoint          = "/api/system/info"
	systemStatsEndpoint         = "/api/system/stats"
	systemProcessActionEndpoint = "/api/system/actions/process"
)

func runSystem(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 1 && args[0] == "info" {
		return printRequest(ctx, client, out, http.MethodGet, systemInfoEndpoint, nil, true)
	}
	if len(args) == 1 && args[0] == "stats" {
		return printRequest(ctx, client, out, http.MethodGet, systemStatsEndpoint, nil, true)
	}
	if len(args) >= 2 && args[0] == "actions" && args[1] == "process" {
		return runSystemProcessAction(ctx, client, args[2:], out)
	}
	return errors.New("usage: hserverctl system info | system stats | system actions process --pid PID --start-time START --signal term|kill --confirm [--wait DURATION]")
}

func runSystemProcessAction(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("system actions process", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	pid := flags.Int("pid", 0, "observed process ID")
	startTime := flags.Uint64("start-time", 0, "observed process start time")
	signal := flags.String("signal", "", "signal: term or kill")
	confirmed := flags.Bool("confirm", false, "confirm the local process mutation")
	wait := flags.Duration("wait", 30*time.Second, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("system process action does not accept positional arguments")
	}
	if !*confirmed {
		return errors.New("local process action requires explicit --confirm")
	}
	if *pid <= 1 {
		return errors.New("process PID must be greater than 1")
	}
	if *startTime == 0 {
		return errors.New("process start time must be greater than zero")
	}
	normalizedSignal := strings.ToLower(strings.TrimSpace(*signal))
	if normalizedSignal != "term" && normalizedSignal != "kill" {
		return fmt.Errorf("process signal must be term or kill, got %q", *signal)
	}
	if *wait <= 0 {
		return errors.New("process action wait must be greater than zero")
	}
	payload := map[string]any{
		"pid":       *pid,
		"startTime": *startTime,
		"signal":    normalizedSignal,
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, systemProcessActionEndpoint, payload, true)
}
