package api

import "testing"

func TestUFWStatusActiveRequiresExactActiveStatus(t *testing.T) {
	if ufwStatusActive("Status: inactive\n") {
		t.Fatal("inactive UFW status must not be treated as active")
	}
	if !ufwStatusActive("Status: active\n") {
		t.Fatal("active UFW status was not detected")
	}
}

func TestSSHPasswordAuthenticationDisabledUsesEffectiveSettings(t *testing.T) {
	if !sshPasswordAuthenticationDisabled("passwordauthentication no\nkbdinteractiveauthentication no\n") {
		t.Fatal("key-only effective SSH configuration was not detected")
	}
	if sshPasswordAuthenticationDisabled("passwordauthentication yes\nkbdinteractiveauthentication no\n") {
		t.Fatal("password-enabled SSH configuration must not pass")
	}
}
