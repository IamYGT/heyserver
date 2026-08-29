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
	"os"
	"path/filepath"
	"strings"
	"time"

	bindsvc "github.com/IamYGT/heyserver/internal/services/bind"
)

func runDNS(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl dns status|zones|zone|records|soa|lookup|check|export|zone-create|zone-delete|record-add|record-update|record-delete|soa-update|reload")
	}
	switch args[0] {
	case "status":
		return runDNSFixedRead(ctx, client, args, out, "status", "/api/dns/status")
	case "zones":
		return runDNSFixedRead(ctx, client, args, out, "zones", "/api/dns/zones")
	case "zone", "records", "soa":
		if len(args) != 2 {
			return fmt.Errorf("usage: hserverctl dns %s DOMAIN", args[0])
		}
		domain, err := bindsvc.NormalizeZoneDomain(args[1])
		if err != nil {
			return err
		}
		endpoint := dnsZonePath(domain)
		if args[0] != "zone" {
			endpoint += "/" + args[0]
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "lookup":
		return runDNSLookup(ctx, client, args[1:], out)
	case "check":
		if len(args) != 1 {
			return errors.New("usage: hserverctl dns check")
		}
		return printRequest(ctx, client, out, http.MethodPost, "/api/dns/check", nil, true)
	case "export":
		return runDNSExport(ctx, client, args[1:], out)
	case "zone-create":
		return runDNSZoneCreate(ctx, client, args[1:], out)
	case "zone-delete":
		return runDNSZoneDelete(ctx, client, args[1:], out)
	case "record-add":
		return runDNSRecordAdd(ctx, client, args[1:], out)
	case "record-update":
		return runDNSRecordUpdate(ctx, client, args[1:], out)
	case "record-delete":
		return runDNSRecordDelete(ctx, client, args[1:], out)
	case "soa-update":
		return runDNSSOAUpdate(ctx, client, args[1:], out)
	case "reload":
		return runDNSReload(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown dns command %q", args[0])
	}
}

func runDNSFixedRead(ctx context.Context, client *apiClient, args []string, out io.Writer, command, endpoint string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: hserverctl dns %s", command)
	}
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}

func runDNSLookup(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns lookup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	recordType := flags.String("type", "A", "DNS record type")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl dns lookup [--type TYPE] DOMAIN")
	}
	domain, err := bindsvc.NormalizeLookupDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	normalizedType, err := bindsvc.NormalizeRecordType(*recordType)
	if err != nil {
		return err
	}
	query := url.Values{"domain": {domain}, "type": {normalizedType}}
	return printRequest(ctx, client, out, http.MethodGet, "/api/dns/lookup?"+query.Encode(), nil, true)
}

func runDNSExport(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outputPath := flags.String("output", "", "write the zone to a new file instead of stdout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl dns export [--output FILE] DOMAIN")
	}
	domain, err := bindsvc.NormalizeZoneDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	raw, err := client.request(ctx, http.MethodGet, dnsZonePath(domain)+"/export", nil, true)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*outputPath) == "" || *outputPath == "-" {
		_, err = out.Write(raw)
		return err
	}
	path := filepath.Clean(*outputPath)
	if err := writeExclusiveFile(path, raw); err != nil {
		return fmt.Errorf("write zone export: %w", err)
	}
	receipt, _ := json.Marshal(map[string]any{"bytes": len(raw), "output": path})
	_, err = fmt.Fprintf(out, "%s\n", receipt)
	return err
}

func runDNSZoneCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns zone-create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm local zone creation")
	ip := flags.String("ip", "", "initial apex and www IPv4 address")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl dns zone-create --confirm --ip IPV4 [--wait DURATION] DOMAIN")
	}
	if !*confirmed {
		return errors.New("BIND zone creation requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("zone creation wait must be greater than zero")
	}
	request, err := bindsvc.ValidateAndNormalizeCreateZone(bindsvc.CreateZoneRequest{Domain: flags.Args()[0], IP: *ip})
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/dns/zones", request, true)
}

func runDNSZoneDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns zone-delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm local zone deletion")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl dns zone-delete --confirm [--wait DURATION] DOMAIN")
	}
	if !*confirmed {
		return errors.New("BIND zone deletion requires explicit --confirm")
	}
	domain, err := bindsvc.NormalizeZoneDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	if *wait <= 0 {
		return errors.New("zone deletion wait must be greater than zero")
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, dnsZonePath(domain), nil, true)
}

func runDNSRecordAdd(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns record-add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm local DNS record creation")
	name := flags.String("name", "@", "record owner name")
	recordType := flags.String("type", "", "DNS record type")
	value := flags.String("value", "", "DNS record value")
	ttl := flags.String("ttl", "3600", "DNS TTL in seconds")
	priority := flags.Int("priority", 0, "MX or SRV priority")
	autoReload := flags.Bool("auto-reload", true, "reload the zone after validation")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl dns record-add --confirm --type TYPE --value VALUE [OPTIONS] DOMAIN")
	}
	if !*confirmed {
		return errors.New("BIND record creation requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("record creation wait must be greater than zero")
	}
	domain, err := bindsvc.NormalizeZoneDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	request, err := bindsvc.ValidateAndNormalizeAddRecord(bindsvc.AddRecordRequest{
		Name: *name, Type: *recordType, Value: *value, TTL: *ttl,
		Priority: *priority, AutoReload: *autoReload,
	})
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, dnsZonePath(domain)+"/records", request, true)
}

func runDNSRecordUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns record-update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm local DNS record replacement")
	name := flags.String("name", "", "existing record owner name")
	recordType := flags.String("type", "", "existing DNS record type")
	oldValue := flags.String("old-value", "", "existing record value")
	newValue := flags.String("new-value", "", "replacement record value")
	newTTL := flags.String("new-ttl", "", "replacement TTL; empty preserves current TTL")
	priority := flags.Int("priority", 0, "replacement MX or SRV priority")
	autoReload := flags.Bool("auto-reload", true, "reload the zone after validation")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl dns record-update --confirm --name NAME --type TYPE --old-value VALUE --new-value VALUE [OPTIONS] DOMAIN")
	}
	if !*confirmed {
		return errors.New("BIND record update requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("record update wait must be greater than zero")
	}
	domain, err := bindsvc.NormalizeZoneDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	request, err := bindsvc.ValidateAndNormalizeUpdateRecord(bindsvc.UpdateRecordRequest{
		Name: *name, Type: *recordType, OldValue: *oldValue, NewValue: *newValue,
		NewTTL: *newTTL, Priority: *priority, AutoReload: *autoReload,
	})
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, dnsZonePath(domain)+"/records", request, true)
}

func runDNSRecordDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns record-delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm local DNS record deletion")
	name := flags.String("name", "", "record owner name")
	recordType := flags.String("type", "", "DNS record type")
	value := flags.String("value", "", "exact record value")
	autoReload := flags.Bool("auto-reload", true, "reload the zone after validation")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl dns record-delete --confirm --name NAME --type TYPE --value VALUE [OPTIONS] DOMAIN")
	}
	if !*confirmed {
		return errors.New("BIND record deletion requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("record deletion wait must be greater than zero")
	}
	domain, err := bindsvc.NormalizeZoneDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	request, err := bindsvc.ValidateAndNormalizeDeleteRecord(bindsvc.DeleteRecordRequest{
		Name: *name, Type: *recordType, Value: *value, AutoReload: *autoReload,
	})
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, dnsZonePath(domain)+"/records", request, true)
}

func runDNSSOAUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns soa-update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm local SOA replacement")
	primaryNS := flags.String("primary-ns", "", "primary nameserver")
	hostmaster := flags.String("hostmaster", "", "responsible mailbox in DNS form")
	refresh := flags.Uint64("refresh", 0, "refresh interval in seconds")
	retry := flags.Uint64("retry", 0, "retry interval in seconds")
	expire := flags.Uint64("expire", 0, "expiry interval in seconds")
	minimum := flags.Uint64("minimum", 0, "negative-cache TTL in seconds")
	wait := flags.Duration("wait", 30*time.Second, "maximum mutation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl dns soa-update --confirm [OPTIONS] DOMAIN")
	}
	if !*confirmed {
		return errors.New("BIND SOA update requires explicit --confirm")
	}
	visited := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { visited[item.Name] = true })
	if !visited["primary-ns"] && !visited["hostmaster"] && !visited["refresh"] && !visited["retry"] && !visited["expire"] && !visited["minimum"] {
		return errors.New("BIND SOA update requires at least one changed field")
	}
	if *wait <= 0 {
		return errors.New("SOA update wait must be greater than zero")
	}
	domain, err := bindsvc.NormalizeZoneDomain(flags.Args()[0])
	if err != nil {
		return err
	}
	current, err := requestJSON[bindsvc.SOARecord](ctx, client, http.MethodGet, dnsZonePath(domain)+"/soa", nil, true)
	if err != nil {
		return err
	}
	request := bindsvc.UpdateSOARequest{
		PrimaryNs: current.PrimaryNs, Hostmaster: current.Hostmaster,
		Refresh: current.Refresh, Retry: current.Retry, Expire: current.Expire, Minimum: current.Minimum,
	}
	if visited["primary-ns"] {
		request.PrimaryNs = *primaryNS
	}
	if visited["hostmaster"] {
		request.Hostmaster = *hostmaster
	}
	for name, value := range map[string]uint64{"refresh": *refresh, "retry": *retry, "expire": *expire, "minimum": *minimum} {
		if visited[name] && value > uint64(^uint32(0)) {
			return fmt.Errorf("%s is too large", name)
		}
		switch name {
		case "refresh":
			if visited[name] {
				request.Refresh = uint32(value)
			}
		case "retry":
			if visited[name] {
				request.Retry = uint32(value)
			}
		case "expire":
			if visited[name] {
				request.Expire = uint32(value)
			}
		case "minimum":
			if visited[name] {
				request.Minimum = uint32(value)
			}
		}
	}
	request, err = bindsvc.ValidateAndNormalizeSOA(request)
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, dnsZonePath(domain)+"/soa", request, true)
}

func runDNSReload(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("dns reload", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm BIND reload")
	wait := flags.Duration("wait", 30*time.Second, "maximum reload wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl dns reload --confirm [--wait DURATION]")
	}
	if !*confirmed {
		return errors.New("BIND reload requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("BIND reload wait must be greater than zero")
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/dns/reload", nil, true)
}

func dnsZonePath(domain string) string {
	return "/api/dns/zones/" + url.PathEscape(domain)
}

func writeExclusiveFile(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		_ = file.Close()
		if !completed {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	completed = true
	return nil
}
