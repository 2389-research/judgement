// ABOUTME: Verifies API credential precedence and strict config parsing.
// ABOUTME: Uses isolated XDG directories so tests never read real credentials.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAPIKeyPrefersNonemptyEnvironment(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "  environment-key  ")
	writeTestConfig(t, configHome, `{"api_key":"stored-key"}`)

	got, err := resolveAPIKey()
	if err != nil || got != "environment-key" {
		t.Fatal("environment API key did not take precedence")
	}
}

func TestResolveAPIKeyLoadsStoredCredential(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "")
	writeTestConfig(t, configHome, "{\"api_key\":\"  stored-key  \"}\n")

	got, err := resolveAPIKey()
	if err != nil || got != "stored-key" {
		t.Fatal("stored API key did not load")
	}
}

func TestResolveAPIKeyRejectsInvalidEnvironmentWithoutReadingConfig(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "bad key")
	writeTestConfig(t, configHome, `{"api_key":"stored-key"}`)

	_, err := resolveAPIKey()
	if err == nil || strings.Contains(err.Error(), "bad key") {
		t.Fatal("invalid environment credential was accepted or exposed")
	}
}

func TestResolveAPIKeyReportsMissingCredentialActionably(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "")

	_, err := resolveAPIKey()
	if err == nil || !strings.Contains(err.Error(), "judgement setup") || !strings.Contains(err.Error(), "TYPESAFE_API_KEY") {
		t.Fatal("missing credential error was not actionable")
	}
}

func TestResolveAPIKeyRejectsMalformedOversizeAndInsecureConfigWithoutSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("filesystem permission coverage")
	}
	for _, tc := range []struct {
		name string
		raw  string
		mode os.FileMode
	}{
		{"malformed", `{"api_key":"very-secret"`, 0o600},
		{"unknown field", `{"api_key":"very-secret","other":true}`, 0o600},
		{"trailing JSON", `{"api_key":"very-secret"}{}`, 0o600},
		{"blank", `{"api_key":"   "}`, 0o600},
		{"multiple tokens", `{"api_key":"very-secret another"}`, 0o600},
		{"oversize", strings.Repeat("x", int(maximumConfigBytes+1)), 0o600},
		{"insecure", `{"api_key":"very-secret"}`, 0o644},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configHome := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", configHome)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("TYPESAFE_API_KEY", "")
			path := writeTestConfig(t, configHome, tc.raw)
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			_, err := resolveAPIKey()
			if err == nil || strings.Contains(err.Error(), "very-secret") {
				t.Fatal("invalid stored credential was accepted or exposed")
			}
		})
	}
}

func writeTestConfig(t *testing.T, configHome, raw string) string {
	t.Helper()
	dir := filepath.Join(configHome, "judgement")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
