package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

type tuiWebResourceKind string

const (
	tuiWebNginx  tuiWebResourceKind = "nginx"
	tuiWebDomain tuiWebResourceKind = "domain"
	tuiWebSSL    tuiWebResourceKind = "ssl"
)

type tuiWebResource struct {
	Kind          tuiWebResourceKind
	ID            string
	Name          string
	State         string
	Detail        string
	Enabled       bool
	DaysRemaining int
}

type tuiWebMsg struct {
	TargetID string
	Items    []tuiWebResource
	Warnings []string
	Err      error
}

type tuiWebSourceResult struct {
	Source string
	Items  []tuiWebResource
	Err    error
}

func loadTUIWebCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		items, warnings, err := loadTUIWeb(ctx, client, target)
		return tuiWebMsg{TargetID: target.ID, Items: items, Warnings: warnings, Err: err}
	}
}

func loadTUIWeb(ctx context.Context, client *apiClient, target tuiTarget) ([]tuiWebResource, []string, error) {
	if !target.Local && !target.Online {
		return nil, nil, errors.New("managed node is offline")
	}

	loaders := []struct {
		source     string
		capability string
		load       func(context.Context, *apiClient, tuiTarget) ([]tuiWebResource, error)
	}{
		{source: "Nginx", capability: agenthub.CapabilityNginxConfigRead, load: loadTUIWebNginx},
		{source: "Domains", capability: agenthub.CapabilityDomainRead, load: loadTUIWebDomains},
		{source: "SSL", capability: agenthub.CapabilitySSLRead, load: loadTUIWebSSL},
	}

	results := make(chan tuiWebSourceResult, len(loaders))
	enabled := 0
	warnings := make([]string, 0, len(loaders))
	for _, loader := range loaders {
		loader := loader
		if !target.capability(loader.capability) {
			warnings = append(warnings, loader.source+" inventory unavailable: managed agent does not advertise "+loader.capability)
			continue
		}
		enabled++
		go func() {
			items, err := loader.load(ctx, client, target)
			results <- tuiWebSourceResult{Source: loader.source, Items: items, Err: err}
		}()
	}
	if enabled == 0 {
		return nil, warnings, errors.New("managed agent advertises no web resource read capabilities")
	}

	bySource := make(map[string][]tuiWebResource, enabled)
	succeeded := 0
	for index := 0; index < enabled; index++ {
		result := <-results
		if result.Err != nil {
			warnings = append(warnings, result.Source+" inventory unavailable: "+result.Err.Error())
			continue
		}
		succeeded++
		bySource[result.Source] = result.Items
	}
	if succeeded == 0 {
		return nil, warnings, errors.New("web resource inventory is unavailable")
	}

	items := make([]tuiWebResource, 0)
	for _, source := range []string{"Nginx", "Domains", "SSL"} {
		items = append(items, bySource[source]...)
	}
	return items, warnings, nil
}

func loadTUIWebNginx(ctx context.Context, client *apiClient, target tuiTarget) ([]tuiWebResource, error) {
	endpoint := "/api/nginx/configs"
	if !target.Local {
		endpoint = "/api/nodes/" + url.PathEscape(target.ID) + "/nginx/configs"
		configs, err := requestJSON[[]remotenodes.NginxConfig](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		sort.Slice(configs, func(i, j int) bool { return strings.ToLower(configs[i].Name) < strings.ToLower(configs[j].Name) })
		items := make([]tuiWebResource, 0, len(configs))
		for _, config := range configs {
			items = append(items, tuiWebResource{
				Kind: tuiWebNginx, ID: config.Name, Name: config.Name, Enabled: config.Enabled,
				State: enabledState(config.Enabled), Detail: fmt.Sprintf("%s · modified %s", formatTUIBytes(uint64(maxInt64(config.Size, 0))), compactWebTime(config.ModifiedAt)),
			})
		}
		return items, nil
	}

	configs, err := requestJSON[[]models.NginxConfig](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return nil, err
	}
	sort.Slice(configs, func(i, j int) bool {
		return strings.ToLower(configs[i].Filename) < strings.ToLower(configs[j].Filename)
	})
	items := make([]tuiWebResource, 0, len(configs))
	for _, config := range configs {
		detail := strings.Trim(strings.Join([]string{config.Domain, config.Type}, " · "), " ·")
		items = append(items, tuiWebResource{
			Kind: tuiWebNginx, ID: config.Filename, Name: config.Filename, Enabled: config.IsEnabled,
			State: enabledState(config.IsEnabled), Detail: detail,
		})
	}
	return items, nil
}

func loadTUIWebDomains(ctx context.Context, client *apiClient, target tuiTarget) ([]tuiWebResource, error) {
	endpoint := "/api/domains"
	if !target.Local {
		endpoint = "/api/nodes/" + url.PathEscape(target.ID) + "/domains"
		domains, err := requestJSON[[]remotenodes.Domain](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		sort.Slice(domains, func(i, j int) bool { return strings.ToLower(domains[i].Name) < strings.ToLower(domains[j].Name) })
		items := make([]tuiWebResource, 0, len(domains))
		for _, domain := range domains {
			detail := valueOrNA(domain.Kind)
			if domain.SSL {
				detail += " · TLS"
			}
			if domain.ProxyTarget != "" {
				detail += " · " + domain.ProxyTarget
			} else if domain.Root != "" {
				detail += " · " + domain.Root
			}
			items = append(items, tuiWebResource{
				Kind: tuiWebDomain, ID: domain.Config, Name: domain.Name, Enabled: domain.Enabled,
				State: enabledState(domain.Enabled), Detail: detail,
			})
		}
		return items, nil
	}

	response, err := requestJSON[struct {
		Domains []models.Domain `json:"domains"`
	}](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return nil, err
	}
	sort.Slice(response.Domains, func(i, j int) bool {
		return strings.ToLower(response.Domains[i].Name) < strings.ToLower(response.Domains[j].Name)
	})
	items := make([]tuiWebResource, 0, len(response.Domains))
	for _, domain := range response.Domains {
		detail := valueOrNA(domain.Type)
		if domain.SSLEnabled {
			detail += " · TLS"
		}
		if domain.ProxyPort > 0 {
			detail += fmt.Sprintf(" · :%d", domain.ProxyPort)
		} else if domain.Root != "" {
			detail += " · " + domain.Root
		}
		items = append(items, tuiWebResource{
			Kind: tuiWebDomain, ID: domain.Name, Name: domain.Name, Enabled: domain.IsActive,
			State: enabledState(domain.IsActive), Detail: detail,
		})
	}
	return items, nil
}

func loadTUIWebSSL(ctx context.Context, client *apiClient, target tuiTarget) ([]tuiWebResource, error) {
	endpoint := "/api/ssl/certificates"
	if !target.Local {
		endpoint = "/api/nodes/" + url.PathEscape(target.ID) + "/certificates"
		certificates, err := requestJSON[[]remotenodes.Certificate](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		sort.Slice(certificates, func(i, j int) bool {
			return strings.ToLower(certificates[i].Name) < strings.ToLower(certificates[j].Name)
		})
		items := make([]tuiWebResource, 0, len(certificates))
		for _, certificate := range certificates {
			items = append(items, tuiWebResource{
				Kind: tuiWebSSL, ID: certificate.Name, Name: certificate.Name,
				State: certificateState(certificate.DaysRemaining), DaysRemaining: certificate.DaysRemaining,
				Detail: fmt.Sprintf("%d day(s) · %s · auto-renew %s", certificate.DaysRemaining, valueOrNA(certificate.Issuer), yesNo(certificate.AutoRenew)),
			})
		}
		return items, nil
	}

	certificates, err := requestJSON[[]models.SSLCertificate](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return nil, err
	}
	sort.Slice(certificates, func(i, j int) bool {
		return strings.ToLower(certificates[i].Domain) < strings.ToLower(certificates[j].Domain)
	})
	items := make([]tuiWebResource, 0, len(certificates))
	for _, certificate := range certificates {
		items = append(items, tuiWebResource{
			Kind: tuiWebSSL, ID: certificate.Domain, Name: certificate.Domain,
			State: certificateState(certificate.DaysRemaining), DaysRemaining: certificate.DaysRemaining,
			Detail: fmt.Sprintf("%d day(s) · %s · auto-renew %s", certificate.DaysRemaining, valueOrNA(certificate.Issuer), yesNo(certificate.AutoRenew)),
		})
	}
	return items, nil
}

func enabledState(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func certificateState(days int) string {
	switch {
	case days < 0:
		return "expired"
	case days <= 14:
		return "expiring"
	default:
		return "valid"
	}
}

func compactWebTime(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return valueOrNA(value)
	}
	return parsed.UTC().Format("2006-01-02 15:04Z")
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
