package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func runLogs(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl logs sources|read")
	}
	switch args[0] {
	case "sources":
		flags := flag.NewFlagSet("logs sources", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl logs sources [--node NODE]")
		}
		if strings.TrimSpace(*node) == "" {
			return printRequest(ctx, client, out, http.MethodGet, "/api/logs/sources", nil, true)
		}
		return printManagedLogSources(ctx, client, out, strings.TrimSpace(*node))
	case "read":
		flags := flag.NewFlagSet("logs read", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		source := flags.String("source", "", "source identity returned by logs sources")
		lines := flags.Int("lines", 200, "number of latest lines")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 || strings.TrimSpace(*source) == "" {
			return errors.New("usage: hserverctl logs read [--node NODE] --source SOURCE [--lines N]")
		}
		maximum := 5000
		if strings.TrimSpace(*node) != "" {
			maximum = 500
		}
		if *lines < 1 || *lines > maximum {
			return fmt.Errorf("log line count must be between 1 and %d", maximum)
		}
		query := url.Values{}
		query.Set("lines", fmt.Sprint(*lines))
		endpoint := "/api/logs/read"
		query.Set("path", strings.TrimSpace(*source))
		if strings.TrimSpace(*node) != "" {
			endpoint = "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/logs"
			query.Del("path")
			query.Set("source", strings.TrimSpace(*source))
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint+"?"+query.Encode(), nil, true)
	default:
		return fmt.Errorf("unknown logs command %q", args[0])
	}
}

func printManagedLogSources(ctx context.Context, client *apiClient, out io.Writer, nodeID string) error {
	raw, err := client.request(ctx, http.MethodGet, "/api/nodes/"+url.PathEscape(nodeID), nil, true)
	if err != nil {
		return err
	}
	var node struct {
		ID           string             `json:"id"`
		Online       bool               `json:"online"`
		Capabilities []string           `json:"capabilities"`
		Inventory    agenthub.Inventory `json:"inventory"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return fmt.Errorf("decode managed-node log sources: %w", err)
	}
	capable := false
	for _, capability := range node.Capabilities {
		if capability == agenthub.CapabilityLogsRead {
			capable = true
			break
		}
	}
	sources := node.Inventory.LogSources
	if sources == nil {
		sources = []string{}
	}
	response := struct {
		Node       string   `json:"node"`
		Online     bool     `json:"online"`
		Capability string   `json:"capability"`
		Available  bool     `json:"available"`
		Sources    []string `json:"sources"`
	}{
		Node: node.ID, Online: node.Online, Capability: agenthub.CapabilityLogsRead,
		Available: node.Online && capable, Sources: sources,
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode managed-node log sources: %w", err)
	}
	return prettyJSON(out, encoded)
}
