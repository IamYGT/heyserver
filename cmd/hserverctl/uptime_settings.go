package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	uptimeSettingsEndpoint           = "/api/uptime/settings"
	minimumUptimeRetentionDays       = 2
	maximumUptimeRetentionDays       = 3650
	minimumUptimeCompactAfterDays    = 1
	maximumUptimeCompactAfterDays    = 365
	minimumUptimeDefaultIntervalSecs = 10
	maximumUptimeDefaultIntervalSecs = 86400
	minimumUptimeDefaultTimeoutSecs  = 1
	maximumUptimeDefaultTimeoutSecs  = 300
	maximumUptimeDefaultChannels     = 128
)

type uptimeSettingsUpdateOptions struct {
	Confirm              bool
	RetentionDays        int
	CompactAfterDays     int
	DefaultIntervalSecs  int
	DefaultTimeoutSecs   int
	DefaultChannelIDs    stringValues
	ClearDefaultChannels bool
	visited              map[string]bool
}

// runUptimeSettings handles the settings read and update subcommands. The
// endpoint and authentication mode are deliberately fixed to the uptime API
// settings contract.
func runUptimeSettings(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return printRequest(ctx, client, out, http.MethodGet, uptimeSettingsEndpoint, nil, true)
	}
	if args[0] != "update" {
		return fmt.Errorf("unknown uptime settings command %q", args[0])
	}
	return runUptimeSettingsUpdate(ctx, client, args[1:], out)
}

func runUptimeSettingsUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags, options := newUptimeSettingsUpdateFlags()
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl uptime settings update --confirm [--retention-days N] [--compact-after-days N] [--default-interval-secs N] [--default-timeout-secs N] [--default-channel ID ...] [--clear-default-channels]")
	}
	options.visited = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { options.visited[item.Name] = true })
	if !options.Confirm {
		return errors.New("uptime settings update requires explicit --confirm")
	}
	payload, err := buildUptimeSettingsUpdatePayload(options)
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodPut, uptimeSettingsEndpoint, payload, true)
}

func newUptimeSettingsUpdateFlags() (*flag.FlagSet, *uptimeSettingsUpdateOptions) {
	flags := flag.NewFlagSet("uptime settings update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := &uptimeSettingsUpdateOptions{}
	flags.BoolVar(&options.Confirm, "confirm", false, "confirm uptime settings update")
	flags.IntVar(&options.RetentionDays, "retention-days", 0, "retain uptime aggregate data for 2-3650 days")
	flags.IntVar(&options.CompactAfterDays, "compact-after-days", 0, "compact uptime heartbeats after 1-365 days")
	flags.IntVar(&options.DefaultIntervalSecs, "default-interval-secs", 0, "default monitor interval in seconds")
	flags.IntVar(&options.DefaultTimeoutSecs, "default-timeout-secs", 0, "default monitor timeout in seconds")
	flags.Var(&options.DefaultChannelIDs, "default-channel", "default notification channel ID; repeatable")
	flags.BoolVar(&options.ClearDefaultChannels, "clear-default-channels", false, "clear all default notification channels")
	return flags, options
}

func buildUptimeSettingsUpdatePayload(options *uptimeSettingsUpdateOptions) (map[string]string, error) {
	if options == nil {
		return nil, errors.New("uptime settings update options are required")
	}
	if !options.Confirm {
		return nil, errors.New("uptime settings update requires explicit --confirm")
	}

	if options.visited == nil {
		options.visited = make(map[string]bool)
	}
	hasMutation := options.visited["retention-days"] ||
		options.visited["compact-after-days"] ||
		options.visited["default-interval-secs"] ||
		options.visited["default-timeout-secs"] ||
		options.visited["default-channel"] ||
		(options.visited["clear-default-channels"] && options.ClearDefaultChannels)
	if !hasMutation {
		return nil, errors.New("uptime settings update requires at least one changed field")
	}

	if options.visited["retention-days"] {
		if err := validateUptimeSettingsRange("uptime_retention_days", options.RetentionDays, minimumUptimeRetentionDays, maximumUptimeRetentionDays); err != nil {
			return nil, err
		}
	}
	if options.visited["compact-after-days"] {
		if err := validateUptimeSettingsRange("uptime_compact_after_days", options.CompactAfterDays, minimumUptimeCompactAfterDays, maximumUptimeCompactAfterDays); err != nil {
			return nil, err
		}
	}
	if options.visited["default-interval-secs"] {
		if err := validateUptimeSettingsRange("uptime_default_interval", options.DefaultIntervalSecs, minimumUptimeDefaultIntervalSecs, maximumUptimeDefaultIntervalSecs); err != nil {
			return nil, err
		}
	}
	if options.visited["default-timeout-secs"] {
		if err := validateUptimeSettingsRange("uptime_default_timeout", options.DefaultTimeoutSecs, minimumUptimeDefaultTimeoutSecs, maximumUptimeDefaultTimeoutSecs); err != nil {
			return nil, err
		}
	}
	if options.visited["retention-days"] && options.visited["compact-after-days"] && options.CompactAfterDays >= options.RetentionDays {
		return nil, errors.New("uptime_compact_after_days must be less than uptime_retention_days")
	}
	if options.visited["default-interval-secs"] && options.visited["default-timeout-secs"] && options.DefaultTimeoutSecs > options.DefaultIntervalSecs {
		return nil, errors.New("uptime_default_timeout must not exceed uptime_default_interval")
	}

	payload := make(map[string]string, 5)
	if options.visited["retention-days"] {
		payload["uptime_retention_days"] = strconv.Itoa(options.RetentionDays)
	}
	if options.visited["compact-after-days"] {
		payload["uptime_compact_after_days"] = strconv.Itoa(options.CompactAfterDays)
	}
	if options.visited["default-interval-secs"] {
		payload["uptime_default_interval"] = strconv.Itoa(options.DefaultIntervalSecs)
	}
	if options.visited["default-timeout-secs"] {
		payload["uptime_default_timeout"] = strconv.Itoa(options.DefaultTimeoutSecs)
	}

	if options.ClearDefaultChannels {
		if options.visited["default-channel"] {
			return nil, errors.New("--clear-default-channels cannot be combined with --default-channel")
		}
		payload["uptime_default_channels"] = "[]"
	} else if options.visited["default-channel"] {
		ids, err := normalizeUptimeSettingsChannelIDs([]string(options.DefaultChannelIDs))
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(ids)
		if err != nil {
			return nil, fmt.Errorf("encode uptime default channels: %w", err)
		}
		payload["uptime_default_channels"] = string(encoded)
	}

	return payload, nil
}

func validateUptimeSettingsRange(name string, value, minimum, maximum int) error {
	if value < minimum || value > maximum {
		return fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return nil
}

func normalizeUptimeSettingsChannelIDs(values []string) ([]int64, error) {
	ids := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("uptime_default_channels must contain only positive channel IDs")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) > maximumUptimeDefaultChannels {
			return nil, fmt.Errorf("uptime_default_channels accepts at most %d channel IDs", maximumUptimeDefaultChannels)
		}
	}
	return ids, nil
}
