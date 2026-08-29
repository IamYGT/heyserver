package releaseversion

import "testing"

func TestCompareStableReleases(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      State
	}{
		{candidate: "v1.2.3", current: "1.2.3", want: Current},
		{candidate: "1.2.2", current: "1.2.3", want: Behind},
		{candidate: "0.99.99", current: "1.0.0", want: Behind},
		{candidate: "2.0.0", current: "1.99.99", want: Ahead},
		{candidate: "dev", current: "dev", want: Unknown},
		{candidate: "ci-a1b2c3", current: "1.2.3", want: Unknown},
		{candidate: "1.2.3-rc.1", current: "1.2.3", want: Unknown},
		{candidate: "1.2", current: "1.2.3", want: Unknown},
	}

	for _, test := range tests {
		if got := Compare(test.candidate, test.current); got != test.want {
			t.Errorf("Compare(%q, %q) = %q, want %q", test.candidate, test.current, got, test.want)
		}
	}
}
