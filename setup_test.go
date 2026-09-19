// ABOUTME: Verifies setup argument handling, private config writes, and output.
// ABOUTME: Uses stdin mode and isolated XDG directories for deterministic tests.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSetupJSONReadsStdinAndWritesOnlyAPIKey(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())
	secret := "setup-secret"
	var stdout, stderr bytes.Buffer
	exit := runSetup(context.Background(), []string{"--key-stdin", "--json"}, strings.NewReader("  "+secret+"\n"), &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout_bytes=%d stderr_bytes=%d", exit, stdout.Len(), stderr.Len())
	}
	if strings.Contains(stdout.String(), secret) {
		t.Fatal("stdout exposed the API key")
	}
	var envelope struct {
		Type       string `json:"type"`
		ConfigPath string `json:"config_path"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(configHome, "judgement", "config.json")
	if envelope.Type != "setup" || envelope.ConfigPath != wantPath {
		t.Fatalf("envelope = %#v", envelope)
	}
	raw, err := os.ReadFile(wantPath) // #nosec G304 -- Read the config fixture under this test's temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]string
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config) != 1 || config["api_key"] != secret {
		t.Fatal("saved configuration did not contain only the expected API key")
	}
	assertMode(t, filepath.Dir(wantPath), 0o700)
	assertMode(t, wantPath, 0o600)
}

func TestRunSetupReplacesStoredCredential(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "")
	writeTestConfig(t, configHome, `{"api_key":"old-secret"}`)
	var stdout, stderr bytes.Buffer
	exit := runSetup(context.Background(), []string{"--key-stdin"}, strings.NewReader("new-secret\n"), &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 || strings.Contains(stdout.String(), "new-secret") {
		t.Fatalf("exit=%d stdout_bytes=%d stderr_bytes=%d", exit, stdout.Len(), stderr.Len())
	}
	got, err := resolveAPIKey()
	if err != nil || got != "new-secret" {
		t.Fatal("setup did not replace the stored API key")
	}
}

func TestRunSetupAcceptsLargestReadableConfig(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TYPESAFE_API_KEY", "")
	wrapperBytes := len("{\"api_key\":\"\"}\n")
	key := strings.Repeat("x", int(maximumConfigBytes)-wrapperBytes)
	var stdout, stderr bytes.Buffer
	exit := runSetup(context.Background(), []string{"--key-stdin", "--json"}, strings.NewReader(key), &stdout, &stderr)
	if exit != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout_bytes=%d stderr_bytes=%d", exit, stdout.Len(), stderr.Len())
	}
	got, err := resolveAPIKey()
	if err != nil || got != key {
		t.Fatal("largest accepted setup key did not round-trip")
	}
}

func TestRunSetupRejectsUnreadableConfigSizeAndPreservesCredential(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"plain boundary overflow", strings.Repeat("x", int(maximumConfigBytes)-len("{\"api_key\":\"\"}\n")+1)},
		{"JSON escaping expansion", strings.Repeat("\\", int(maximumConfigBytes/2))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configHome := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", configHome)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("TYPESAFE_API_KEY", "")
			writeTestConfig(t, configHome, `{"api_key":"preserved-key"}`)
			var stdout, stderr bytes.Buffer
			exit := runSetup(context.Background(), []string{"--key-stdin", "--json"}, strings.NewReader(tc.key), &stdout, &stderr)
			if exit != 2 {
				t.Fatalf("exit=%d stdout_bytes=%d stderr_bytes=%d", exit, stdout.Len(), stderr.Len())
			}
			got, err := resolveAPIKey()
			if err != nil || got != "preserved-key" {
				t.Fatal("rejected setup input changed the stored API key")
			}
		})
	}
}

func TestRunSetupRejectsInvalidModesBeforeReadingInput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"JSON requires stdin", []string{"--json"}, "--key-stdin"},
		{"unexpected positional", []string{"--key-stdin", "extra"}, "unexpected"},
		{"unknown flag", []string{"--key-stdin", "--wat"}, "flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := runSetup(context.Background(), tc.args, panicReader{}, &stdout, &stderr)
			if exit != 2 || !strings.Contains(stdout.String()+stderr.String(), tc.want) {
				t.Fatalf("exit=%d stdout_bytes=%d stderr_bytes=%d", exit, stdout.Len(), stderr.Len())
			}
		})
	}
}

func TestRunSetupHelpNeedsNoInputOrConfigAccess(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative")
	t.Setenv("HOME", "")
	for _, args := range [][]string{{"--help"}, {"--json", "--help"}} {
		var stdout, stderr bytes.Buffer
		exit := runSetup(context.Background(), args, panicReader{}, &stdout, &stderr)
		if exit != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "key-stdin") {
			t.Fatalf("args=%v exit=%d stdout_bytes=%d stderr_bytes=%d", args, exit, stdout.Len(), stderr.Len())
		}
	}
}

func TestRunSetupRejectsNonterminalInteractiveInput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	exit := runSetup(context.Background(), nil, strings.NewReader("secret\n"), &stdout, &stderr)
	if exit != 2 || !strings.Contains(stderr.String(), "terminal") || strings.Contains(stderr.String(), "secret") {
		t.Fatalf("exit=%d stdout_bytes=%d stderr_bytes=%d", exit, stdout.Len(), stderr.Len())
	}
}

func TestRunSetupBoundsAndCancelsStdinRead(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	exit := runSetup(context.Background(), []string{"--key-stdin", "--json"}, strings.NewReader(strings.Repeat("x", int(maximumConfigBytes+1))), &stdout, &stderr)
	if exit != 2 || !strings.Contains(stdout.String(), "16 KiB") {
		t.Fatalf("oversize exit=%d stdout_bytes=%d stderr_bytes=%d", exit, stdout.Len(), stderr.Len())
	}

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	finished := make(chan int, 1)
	go func() {
		var cancelOut, cancelErr bytes.Buffer
		finished <- runSetup(ctx, []string{"--key-stdin", "--json"}, &blockingReader{started: started}, &cancelOut, &cancelErr)
	}()
	<-started
	cancel()
	select {
	case got := <-finished:
		if got != 1 {
			t.Fatalf("canceled exit = %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("setup did not return after context cancellation")
	}
}

func TestRunSetupNeverExposesInvalidKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	secret := "secret with whitespace"
	var stdout, stderr bytes.Buffer
	exit := runSetup(context.Background(), []string{"--key-stdin", "--json"}, strings.NewReader(secret), &stdout, &stderr)
	if exit != 2 || strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatalf("exit=%d stdout_bytes=%d stderr_bytes=%d", exit, stdout.Len(), stderr.Len())
	}
}

type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("unexpected input read") }

type blockingReader struct {
	started chan struct{}
}

func (r *blockingReader) Read([]byte) (int, error) {
	close(r.started)
	select {}
}

var _ io.Reader = panicReader{}
