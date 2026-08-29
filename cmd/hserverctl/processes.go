package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const processSignalUsage = "usage: hserverctl processes signal --node NODE --pid PID --start-time START --signal term|kill --confirm [--wait DURATION]"

// processSignalRequest deliberately mirrors the managed-node API contract.
// The API expects startTime in camel case and does not accept the CLI-only
// confirmation flag in the request body.
type processSignalRequest struct {
	PID       int    `json:"pid"`
	StartTime uint64 `json:"startTime"`
	Signal    string `json:"signal"`
}

type processSignalTarget struct {
	node      string
	pid       int
	startTime uint64
	signal    string
}

func (target processSignalTarget) summary() string {
	return fmt.Sprintf("node %q PID %d start-time %d signal %q", target.node, target.pid, target.startTime, target.signal)
}

func processSignalRefusal(target processSignalTarget, message string) error {
	return fmt.Errorf("process signal %s: %s", target.summary(), message)
}

func runProcesses(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl processes signal")
	}
	if args[0] != "signal" {
		return fmt.Errorf("unknown processes command %q", args[0])
	}

	flags := flag.NewFlagSet("processes signal", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID (required)")
	pid := flags.Int("pid", 0, "stable process ID; must be greater than 1")
	startTime := flags.Uint64("start-time", 0, "stable process start-time identity; must be positive")
	signal := flags.String("signal", "", "process signal: term or kill")
	confirmed := flags.Bool("confirm", false, "confirm signaling the managed process")
	wait := flags.Duration("wait", 30*time.Second, "maximum signal request wait")
	if err := flags.Parse(args[1:]); err != nil {
		target := processSignalTarget{
			node: strings.TrimSpace(*node), pid: *pid, startTime: *startTime, signal: *signal,
		}
		return processSignalRefusal(target, err.Error())
	}
	target := processSignalTarget{
		node: strings.TrimSpace(*node), pid: *pid, startTime: *startTime, signal: *signal,
	}
	if len(flags.Args()) != 0 {
		return processSignalRefusal(target, fmt.Sprintf("unexpected argument %q; %s", flags.Args()[0], processSignalUsage))
	}
	if target.node == "" {
		return processSignalRefusal(target, "requires explicit --node NODE")
	}
	if target.pid <= 1 {
		return processSignalRefusal(target, "PID must be greater than 1")
	}
	if target.startTime == 0 {
		return processSignalRefusal(target, "start-time must be a positive process identity")
	}
	if target.signal != "term" && target.signal != "kill" {
		return processSignalRefusal(target, fmt.Sprintf("unsupported process signal %q; expected term or kill", target.signal))
	}
	if !*confirmed {
		return processSignalRefusal(target, "requires explicit --confirm")
	}
	if *wait <= 0 {
		return processSignalRefusal(target, "wait must be greater than zero")
	}

	endpoint := "/api/nodes/" + url.PathEscape(target.node) + "/processes/signal"
	payload := processSignalRequest{PID: target.pid, StartTime: target.startTime, Signal: target.signal}
	if err := printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, payload, true); err != nil {
		// Keep the target in the rendered failure as well as the API's safe,
		// actionable message. Do not unwrap here: presentCLIError intentionally
		// renders typed API errors on their own, which would otherwise hide the
		// process identity that the operator needs to recover the action.
		return fmt.Errorf("process signal %s failed: %s", target.summary(), clientErrorMessage(err))
	}
	return nil
}
