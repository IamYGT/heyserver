package integrationstatus

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/extensions"
	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func catalogFixture(t *testing.T) extensions.Catalog {
	t.Helper()
	catalog, err := extensions.LoadCatalog()
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	return catalog
}

func TestStatusUsesCanonicalSchemaAndCatalogDerivedUnprobedIDs(t *testing.T) {
	catalog := catalogFixture(t)
	service := NewWithCatalog(func() (extensions.Catalog, error) { return catalog, nil },
		Probe{ID: ProcessPM2ID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
		Probe{ID: CloudflareDNSID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.NotConfigured, errors.New("token /srv/secret should stay internal")
		}},
	)

	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if response.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", response.SchemaVersion)
	}
	if response.Target.Scope != ScopeLocalHost {
		t.Fatalf("target.scope = %q, want %q", response.Target.Scope, ScopeLocalHost)
	}
	if response.ObservedAt.IsZero() || response.ObservedAt.Location() != time.UTC {
		t.Fatalf("observed_at = %v, want a UTC timestamp", response.ObservedAt)
	}
	if len(response.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(response.Results))
	}
	if got := response.Results[0]; got.ID != ProcessPM2ID || got.State != integrationstate.Healthy || got.Probe != PM2InventoryProbe || got.ErrorCode != "" {
		t.Fatalf("PM2 result = %#v", got)
	}
	if got := response.Results[1]; got.ID != CloudflareDNSID || got.State != integrationstate.NotConfigured || got.Probe != CloudflareZoneProbe || got.ErrorCode != ErrorCodeNotConfigured {
		t.Fatalf("Cloudflare result = %#v", got)
	}
	if len(response.Unprobed) != len(catalog.Entries)-2 {
		t.Fatalf("unprobed = %d, want %d", len(response.Unprobed), len(catalog.Entries)-2)
	}
	for _, id := range response.Unprobed {
		if id == ProcessPM2ID || id == CloudflareDNSID {
			t.Fatalf("probed ID %q was listed as unprobed", id)
		}
	}
	if !response.Partial {
		t.Fatal("partial = false, want true while catalog IDs remain unprobed")
	}

	wire, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal response: %v", err)
	}
	if strings.Contains(string(wire), "/srv/secret") || strings.Contains(string(wire), "token") {
		t.Fatalf("response leaked probe error detail: %s", wire)
	}
}

func TestStatusAcceptsHealthyAdditiveProbeInDeterministicCatalogOrder(t *testing.T) {
	const additiveID = "community.example"
	const secondAdditiveID = "community.zeta"
	catalog := extensions.Catalog{
		SchemaVersion: SchemaVersion,
		Entries: []extensions.Entry{
			{ID: secondAdditiveID},
			{ID: ProcessPM2ID},
			{
				ID:          additiveID,
				DisplayName: "Community <script>alert(1)</script>",
				Purpose:     "token=must-not-become-a-probe-name",
				Configuration: extensions.Configuration{
					Boundary: "/srv/private/provider-config",
				},
			},
		},
	}
	service := NewWithCatalog(func() (extensions.Catalog, error) { return catalog, nil },
		Probe{ID: secondAdditiveID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
		Probe{ID: additiveID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
		Probe{ID: ProcessPM2ID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
	)

	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(response.Results) != 3 || len(response.Unprobed) != 0 || response.Partial {
		t.Fatalf("aggregate = results %d, unprobed %d, partial %v; want 3, 0, false", len(response.Results), len(response.Unprobed), response.Partial)
	}
	wantIDs := []string{ProcessPM2ID, secondAdditiveID, additiveID}
	wantProbes := []string{PM2InventoryProbe, "community_zeta", "community_example"}
	for index, result := range response.Results {
		if result.ID != wantIDs[index] || result.Probe != wantProbes[index] || result.State != integrationstate.Healthy || result.ErrorCode != "" {
			t.Fatalf("result[%d] = %#v; want id %q probe %q healthy", index, result, wantIDs[index], wantProbes[index])
		}
	}
	if response.Results[2].Probe == catalog.Entries[2].DisplayName || response.Results[2].Probe == catalog.Entries[2].Purpose || response.Results[2].Probe == catalog.Entries[2].Configuration.Boundary {
		t.Fatalf("additive probe name was derived from catalog metadata: %#v", response.Results[2])
	}
}

func TestStatusLeavesMissingAdditiveProbeUnprobed(t *testing.T) {
	const additiveID = "community.example"
	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{
			SchemaVersion: SchemaVersion,
			Entries:       []extensions.Entry{{ID: ProcessPM2ID}, {ID: additiveID}},
		}, nil
	}, Probe{ID: ProcessPM2ID, Run: func(context.Context) (integrationstate.State, error) {
		return integrationstate.Healthy, nil
	}})

	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].ID != ProcessPM2ID {
		t.Fatalf("results = %#v; want only the registered core probe", response.Results)
	}
	if len(response.Unprobed) != 1 || response.Unprobed[0] != additiveID || !response.Partial {
		t.Fatalf("unprobed = %#v partial=%v; want [%q] and true", response.Unprobed, response.Partial, additiveID)
	}
}

func TestStatusHandlesDuplicateUnknownAndUnsafeProbeMetadataDeterministically(t *testing.T) {
	const additiveID = "community.example"
	const unknownID = "community.unknown"
	catalog := extensions.Catalog{
		SchemaVersion: SchemaVersion,
		Entries:       []extensions.Entry{{ID: additiveID}},
	}
	service := NewWithCatalog(func() (extensions.Catalog, error) { return catalog, nil },
		Probe{ID: additiveID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
		Probe{ID: additiveID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Unavailable, errors.New("duplicate definition must not win")
		}},
		Probe{ID: unknownID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Unavailable, errors.New("unknown definition must stay out of the response")
		}},
		Probe{ID: "../private", Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
	)

	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(response.Results) != 1 || len(response.Unprobed) != 0 || response.Partial {
		t.Fatalf("aggregate = results %d, unprobed %d, partial %v; want 1, 0, false", len(response.Results), len(response.Unprobed), response.Partial)
	}
	result := response.Results[0]
	if result.ID != additiveID || result.Probe != "community_example" || result.State != integrationstate.Healthy {
		t.Fatalf("additive result = %#v; want first duplicate healthy definition with safe derived name", result)
	}
}

func TestStatusRejectsUnsafeCatalogIntegrationID(t *testing.T) {
	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{
			SchemaVersion: SchemaVersion,
			Entries:       []extensions.Entry{{ID: "../private"}},
		}, nil
	})

	_, err := service.Status(context.Background())
	if err == nil || !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("Status error = %v, want ErrInvalidCatalog for unsafe catalog ID", err)
	}
}

func TestStatusCatalogJoinRequiresDockerRegistryMembership(t *testing.T) {
	catalog := catalogFixture(t)
	loader := func() (extensions.Catalog, error) { return catalog, nil }

	withoutDocker := NewWithCatalog(loader)
	response, err := withoutDocker.Status(context.Background())
	if err != nil {
		t.Fatalf("Status without Docker registry member: %v", err)
	}
	if !contains(response.Unprobed, DockerID) {
		t.Fatalf("Docker ID was removed from unprobed without registry membership: %#v", response.Unprobed)
	}

	withDocker := NewWithCatalog(loader, Probe{ID: DockerID, Run: func(context.Context) (integrationstate.State, error) {
		return integrationstate.Healthy, nil
	}})
	response, err = withDocker.Status(context.Background())
	if err != nil {
		t.Fatalf("Status with Docker registry member: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].ID != DockerID || response.Results[0].Probe != DockerInfoProbe || response.Results[0].State != integrationstate.Healthy {
		t.Fatalf("Docker result = %#v, want one healthy docker_info result", response.Results)
	}
	if contains(response.Unprobed, DockerID) {
		t.Fatalf("Docker ID remained unprobed after registry membership: %#v", response.Unprobed)
	}
}

func TestDockerProbeSuccessIsHealthyOnlyAfterObservation(t *testing.T) {
	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{SchemaVersion: SchemaVersion, Entries: []extensions.Entry{{ID: DockerID}}}, nil
	}, Probe{ID: DockerID, Run: func(context.Context) (integrationstate.State, error) {
		return integrationstate.Healthy, nil
	}})

	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(response.Results) != 1 || response.Results[0].State != integrationstate.Healthy || response.Results[0].Probe != DockerInfoProbe || response.Results[0].ErrorCode != "" {
		t.Fatalf("Docker success result = %#v", response.Results)
	}
	if response.Partial {
		t.Fatal("partial = true, want false for the only healthy catalog member")
	}
}

func TestStatusAdmitsAllFifteenCanonicalLocalProbeDefinitions(t *testing.T) {
	ids := append([]string(nil), supportedProbeIDs...)
	entries := make([]extensions.Entry, 0, len(ids))
	probes := make([]Probe, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, extensions.Entry{ID: id})
		probes = append(probes, Probe{ID: id, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}})
	}

	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{SchemaVersion: SchemaVersion, Entries: entries}, nil
	}, probes...)
	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(response.Results) != 15 || len(response.Unprobed) != 0 || response.Partial {
		t.Fatalf("aggregate = results %d, unprobed %d, partial %v; want 15, 0, false", len(response.Results), len(response.Unprobed), response.Partial)
	}
	for index, result := range response.Results {
		wantID := ids[index]
		if result.ID != wantID || result.Probe != probeNames[wantID] || result.State != integrationstate.Healthy || result.ErrorCode != "" {
			t.Fatalf("result[%d] = %#v; want id %q probe %q healthy", index, result, wantID, probeNames[wantID])
		}
	}
}

func TestStatusStartsAllFifteenFixedProbesWithoutQueueTimeouts(t *testing.T) {
	ids := append([]string(nil), supportedProbeIDs...)
	entries := make([]extensions.Entry, 0, len(ids))
	probes := make([]Probe, 0, len(ids))
	started := make(chan struct{}, len(ids))
	release := make(chan struct{})
	for _, id := range ids {
		entries = append(entries, extensions.Entry{ID: id})
		probes = append(probes, Probe{ID: id, Run: func(ctx context.Context) (integrationstate.State, error) {
			started <- struct{}{}
			select {
			case <-release:
				return integrationstate.Healthy, nil
			case <-ctx.Done():
				return integrationstate.Unavailable, ctx.Err()
			}
		}})
	}

	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{SchemaVersion: SchemaVersion, Entries: entries}, nil
	}, probes...)
	done := make(chan Response, 1)
	go func() {
		response, _ := service.Status(context.Background())
		done <- response
	}()

	deadline := time.After(time.Second)
	for count := 0; count < len(ids); count++ {
		select {
		case <-started:
		case <-deadline:
			close(release)
			t.Fatalf("started probes = %d, want all %d before release", count, len(ids))
		}
	}
	close(release)
	select {
	case response := <-done:
		if len(response.Results) != len(ids) || response.Partial {
			t.Fatalf("aggregate = results %d partial %v; want %d and false", len(response.Results), response.Partial, len(ids))
		}
	case <-time.After(time.Second):
		t.Fatal("aggregate did not return after all probes were released")
	}
}

func TestDockerProbeNotConfiguredUsesSafeState(t *testing.T) {
	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{SchemaVersion: SchemaVersion, Entries: []extensions.Entry{{ID: DockerID}}}, nil
	}, Probe{ID: DockerID, Run: func(context.Context) (integrationstate.State, error) {
		return integrationstate.NotConfigured, errors.New("Docker prerequisite missing at /run/secrets/docker.sock")
	}})

	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := response.Results[0]; got.State != integrationstate.NotConfigured || got.ErrorCode != ErrorCodeNotConfigured {
		t.Fatalf("Docker not-configured result = %#v", got)
	}
	if response.Partial {
		t.Fatal("partial = true, want false for an explicitly not-configured optional integration")
	}
}

func TestDockerProbeUnavailableDoesNotLeakRawError(t *testing.T) {
	const secret = "docker-socket-secret-123"
	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{SchemaVersion: SchemaVersion, Entries: []extensions.Entry{{ID: DockerID}}}, nil
	}, Probe{ID: DockerID, Run: func(context.Context) (integrationstate.State, error) {
		return integrationstate.Unavailable, errors.New("permission denied " + secret + " at /var/run/docker.sock")
	}})

	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := response.Results[0]; got.State != integrationstate.Unavailable || got.ErrorCode != ErrorCodeProbeFailed {
		t.Fatalf("Docker unavailable result = %#v", got)
	}
	if !response.Partial {
		t.Fatal("partial = false, want true for an unavailable Docker probe")
	}
	wire, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal response: %v", err)
	}
	for _, forbidden := range []string{secret, "/var/run/docker.sock", "permission denied"} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, wire)
		}
	}
}

func TestDockerProbeTimeoutIsUnavailableWithSafeTimeoutCode(t *testing.T) {
	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{SchemaVersion: SchemaVersion, Entries: []extensions.Entry{{ID: DockerID}}}, nil
	}, Probe{ID: DockerID, Run: func(ctx context.Context) (integrationstate.State, error) {
		<-ctx.Done()
		return integrationstate.Unavailable, ctx.Err()
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	response, err := service.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := response.Results[0]; got.State != integrationstate.Unavailable || got.ErrorCode != ErrorCodeTimeout {
		t.Fatalf("Docker timeout result = %#v", got)
	}
	if !response.Partial {
		t.Fatal("partial = false, want true for a timed-out Docker probe")
	}
}

func TestStatusMarksProbeFailureWithoutLeakingRawError(t *testing.T) {
	catalog := catalogFixture(t)
	const secret = "super-secret-token-123"
	service := NewWithCatalog(func() (extensions.Catalog, error) { return catalog, nil },
		Probe{ID: ProcessPM2ID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Unavailable, errors.New("pm2 output /etc/private/app.log " + secret)
		}},
		Probe{ID: CloudflareDNSID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
	)

	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := response.Results[0]; got.State != integrationstate.Unavailable || got.ErrorCode != ErrorCodeProbeFailed {
		t.Fatalf("failed result = %#v", got)
	}
	if !response.Partial {
		t.Fatal("partial = false, want true for a failed probe")
	}
	wire, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal response: %v", err)
	}
	for _, forbidden := range []string{secret, "/etc/private/app.log", "pm2 output"} {
		if strings.Contains(string(wire), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, wire)
		}
	}
}

func TestStatusMapsDeadlineToUnavailableTimeoutAndHTTP200SafeResult(t *testing.T) {
	catalog := catalogFixture(t)
	service := NewWithCatalog(func() (extensions.Catalog, error) { return catalog, nil },
		Probe{ID: ProcessPM2ID, Run: func(ctx context.Context) (integrationstate.State, error) {
			<-ctx.Done()
			return integrationstate.Unavailable, ctx.Err()
		}},
		Probe{ID: CloudflareDNSID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	response, err := service.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got := response.Results[0]; got.State != integrationstate.Unavailable || got.ErrorCode != ErrorCodeTimeout {
		t.Fatalf("timeout result = %#v", got)
	}
	if !response.Partial {
		t.Fatal("partial = false, want true for a timeout")
	}
	if response.Results[1].State != integrationstate.Healthy {
		t.Fatalf("Cloudflare result = %#v, want healthy", response.Results[1])
	}
}

func TestStatusReturnsCatalogErrorWithoutBuildingPartialWireObject(t *testing.T) {
	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{}, errors.New("catalog secret/path should stay internal")
	})

	_, err := service.Status(context.Background())
	if !errors.Is(err, ErrCatalogUnavailable) {
		t.Fatalf("Status error = %v, want ErrCatalogUnavailable", err)
	}
}

func TestStatusRejectsInvalidCatalogSchema(t *testing.T) {
	service := NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{SchemaVersion: 9}, nil
	})

	_, err := service.Status(context.Background())
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("Status error = %v, want ErrInvalidCatalog", err)
	}
}
