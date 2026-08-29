package backup

import "testing"

func TestFrequencyToCron(t *testing.T) {
	cron, err := FrequencyToCron("daily", "03:00")
	if err != nil {
		t.Fatal(err)
	}
	if cron != "0 3 * * *" {
		t.Errorf("daily: got %q", cron)
	}

	cron, err = FrequencyToCron("weekly", "14:30")
	if err != nil {
		t.Fatal(err)
	}
	if cron != "30 14 * * 0" {
		t.Errorf("weekly: got %q", cron)
	}

	cron, err = FrequencyToCron("monthly", "02:15")
	if err != nil {
		t.Fatal(err)
	}
	if cron != "15 2 1 * *" {
		t.Errorf("monthly: got %q", cron)
	}
}

func TestCronToFrequency(t *testing.T) {
	f, tm, err := CronToFrequency("0 3 * * *")
	if err != nil || f != "daily" || tm != "03:00" {
		t.Errorf("daily roundtrip: f=%s tm=%s err=%v", f, tm, err)
	}

	f, tm, err = CronToFrequency("30 14 * * 0")
	if err != nil || f != "weekly" || tm != "14:30" {
		t.Errorf("weekly roundtrip: f=%s tm=%s err=%v", f, tm, err)
	}

	f, tm, err = CronToFrequency("15 2 1 * *")
	if err != nil || f != "monthly" || tm != "02:15" {
		t.Errorf("monthly roundtrip: f=%s tm=%s err=%v", f, tm, err)
	}
}

func TestCronToFrequencyRejectsCustomSchedules(t *testing.T) {
	for _, cron := range []string{
		"0 3 15 * *",
		"*/15 * * * *",
		"0 3 * 1 *",
		"0 3 * * 1",
	} {
		if frequency, timeHHMM, err := CronToFrequency(cron); err == nil {
			t.Fatalf("cron=%q frequency=%q time=%q unexpectedly mapped to preset", cron, frequency, timeHHMM)
		}
	}
}
