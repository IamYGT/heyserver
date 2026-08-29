package php

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRestartAndReloadValidateConfigurationFirst(t *testing.T) {
	t.Parallel()
	var calls []string
	service := &Service{runCommand: func(name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		return []byte("ok"), nil
	}}
	if err := service.RestartFPM("8.4"); err != nil {
		t.Fatal(err)
	}
	if err := service.ReloadFPM("8.4"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"php-fpm8.4 -t", "systemctl restart php8.4-fpm",
		"php-fpm8.4 -t", "systemctl reload php8.4-fpm",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestLifecycleActionStopsWhenConfigurationTestFails(t *testing.T) {
	t.Parallel()
	var calls []string
	service := &Service{runCommand: func(name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(append([]string{name}, args...), " "))
		if strings.HasPrefix(name, "php-fpm") {
			return []byte("invalid pool directive"), errors.New("exit status 78")
		}
		return nil, nil
	}}
	err := service.RestartFPM("8.4")
	if err == nil || !strings.Contains(err.Error(), "invalid pool directive") {
		t.Fatalf("error = %v", err)
	}
	if !errors.Is(err, ErrFPMConfigInvalid) {
		t.Fatalf("error = %v, want ErrFPMConfigInvalid", err)
	}
	if !reflect.DeepEqual(calls, []string{"php-fpm8.4 -t"}) {
		t.Fatalf("unsafe lifecycle calls = %#v", calls)
	}
}

func TestLifecycleActionReportsSystemdFailure(t *testing.T) {
	t.Parallel()
	service := &Service{runCommand: func(name string, args ...string) ([]byte, error) {
		if name == "systemctl" {
			return []byte("unit unavailable"), errors.New("exit status 5")
		}
		return []byte("configuration valid"), nil
	}}

	err := service.ReloadFPM("8.4")
	if !errors.Is(err, ErrFPMLifecycleAction) {
		t.Fatalf("error = %v, want ErrFPMLifecycleAction", err)
	}
	if !strings.Contains(err.Error(), "unit unavailable") {
		t.Fatalf("error = %v, want command output", err)
	}
}
