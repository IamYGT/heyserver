package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
)

// runDiskOverview exposes the read-only disk inventory already available in
// the panel API. The local endpoint performs a fresh host observation, while
// the managed endpoint returns the selected agent's bounded heartbeat mounts.
// Keep these paths separate: a managed node must never be represented by a
// local filesystem probe.
func runDiskOverview(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	node, err := parseOptionalNode("disk overview", args)
	if err != nil {
		return err
	}
	endpoint := "/api/disk/overview"
	if node != "" {
		endpoint = "/api/nodes/" + url.PathEscape(node) + "/disk"
	}
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}
