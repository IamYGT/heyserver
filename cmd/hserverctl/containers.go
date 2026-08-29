package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/services/dockerctl"
)

func runContainers(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl containers status|list|logs|action")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl containers status")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/docker/status", nil, true)
	case "list":
		flags := flag.NewFlagSet("containers list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl containers list [--node NODE]")
		}
		endpoint := "/api/docker/containers"
		if strings.TrimSpace(*node) != "" {
			endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/containers"
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "logs":
		flags := flag.NewFlagSet("containers logs", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		tail := flags.Int("tail", 200, "number of latest container log lines (1-1000)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 1 {
			return errors.New("usage: hserverctl containers logs [--tail N] CONTAINER")
		}
		container := strings.TrimSpace(flags.Args()[0])
		if !dockerctl.ValidObjectID(container) {
			return errors.New("invalid container id format")
		}
		if *tail < 1 || *tail > 1000 {
			return errors.New("container log tail must be between 1 and 1000")
		}
		query := url.Values{"tail": []string{strconv.Itoa(*tail)}}
		endpoint := "/api/docker/containers/" + url.PathEscape(container) + "/logs?" + query.Encode()
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "action":
		flags := flag.NewFlagSet("containers action", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		confirmed := flags.Bool("confirm", false, "confirm the container mutation")
		wait := flags.Duration("wait", 7*time.Minute, "maximum action wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		positional := flags.Args()
		if len(positional) != 2 {
			return errors.New("usage: hserverctl containers action --confirm [--node NODE] [--wait DURATION] CONTAINER ACTION")
		}
		if !*confirmed {
			return errors.New("container action requires explicit --confirm")
		}
		container := strings.TrimSpace(positional[0])
		if container == "" {
			return errors.New("container identity must not be empty")
		}
		if *wait <= 0 {
			return errors.New("container action wait must be greater than zero")
		}
		remote := strings.TrimSpace(*node) != ""
		if err := validateContainerAction(positional[1], remote); err != nil {
			return err
		}
		endpoint := "/api/docker/containers/" + url.PathEscape(container) + "/" + url.PathEscape(positional[1])
		if remote {
			endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/containers/" +
				url.PathEscape(container) + "/actions/" + url.PathEscape(positional[1])
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
	default:
		return fmt.Errorf("unknown containers command %q", args[0])
	}
}

func runImages(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl images list|pull|delete")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: hserverctl images list")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/docker/images", nil, true)
	case "pull":
		flags := flag.NewFlagSet("images pull", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm pulling the Docker image")
		wait := flags.Duration("wait", 10*time.Minute, "maximum image pull wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 1 {
			return errors.New("usage: hserverctl images pull --confirm [--wait DURATION] IMAGE")
		}
		if !*confirmed {
			return errors.New("image pull requires explicit --confirm")
		}
		if *wait <= 0 {
			return errors.New("image pull wait must be greater than zero")
		}
		image := strings.TrimSpace(flags.Args()[0])
		if !dockerctl.ValidObjectID(image) {
			return errors.New("invalid image reference")
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/docker/images/pull", map[string]string{"name": image}, true)
	case "delete":
		flags := flag.NewFlagSet("images delete", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm removing the Docker image")
		wait := flags.Duration("wait", 2*time.Minute, "maximum image removal wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 1 {
			return errors.New("usage: hserverctl images delete --confirm [--wait DURATION] IMAGE")
		}
		if !*confirmed {
			return errors.New("image removal requires explicit --confirm")
		}
		if *wait <= 0 {
			return errors.New("image removal wait must be greater than zero")
		}
		image := strings.TrimSpace(flags.Args()[0])
		if !dockerctl.ValidObjectID(image) {
			return errors.New("invalid image reference")
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, "/api/docker/images/"+url.PathEscape(image), nil, true)
	default:
		return fmt.Errorf("unknown images command %q", args[0])
	}
}

func validateContainerAction(action string, remote bool) error {
	switch action {
	case "start", "stop", "restart":
		return nil
	case "pause", "unpause", "remove":
		if !remote {
			return nil
		}
	}
	if remote {
		return fmt.Errorf("unsupported managed-node container action %q", action)
	}
	return fmt.Errorf("unsupported local container action %q", action)
}
