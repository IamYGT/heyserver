package integrationstate

import "testing"

func TestNormalizeAcceptsOnlyCanonicalWireStates(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want State
	}{
		{name: "not configured", raw: " NOT_CONFIGURED ", want: NotConfigured},
		{name: "unavailable", raw: "Unavailable", want: Unavailable},
		{name: "healthy", raw: "healthy", want: Healthy},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := Normalize(test.raw)
			if err != nil || got != test.want {
				t.Fatalf("Normalize(%q) = %q, %v; want %q", test.raw, got, err, test.want)
			}
		})
	}
}

func TestNormalizeRejectsPresentationAndRuntimeStates(t *testing.T) {
	for _, raw := range []string{"not-configured", "stopped", "unknown", ""} {
		if got, err := Normalize(raw); err == nil || got != "" {
			t.Fatalf("Normalize(%q) = %q, %v; want an invalid-state error", raw, got, err)
		}
	}
}

func TestFromObservationRequiresSuccessfulObservationForHealthy(t *testing.T) {
	tests := []struct {
		name string
		got  State
		want State
	}{
		{name: "not configured", got: FromObservation(Observation{}), want: NotConfigured},
		{name: "configured but not successful", got: FromObservation(Observation{Configured: true}), want: Unavailable},
		{name: "configured and successful", got: FromObservation(Observation{Configured: true, Successful: true}), want: Healthy},
		{name: "success cannot override missing configuration", got: FromObservation(Observation{Successful: true}), want: NotConfigured},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("state = %q; want %q", test.got, test.want)
			}
		})
	}
}

func TestValidateRejectsNonWireSpellings(t *testing.T) {
	for _, raw := range []string{"not-configured", "stopped", "healthy-ish", " Healthy ", "HEALTHY"} {
		if err := Validate(raw); err == nil {
			t.Fatalf("Validate(%q) unexpectedly accepted a non-wire state", raw)
		}
	}
	if !IsValid(string(Healthy)) || IsValid("stopped") {
		t.Fatal("IsValid did not preserve the exact wire-state boundary")
	}
}
