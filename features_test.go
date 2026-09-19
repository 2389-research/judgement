// ABOUTME: Verifies setup routing, saved credentials, and opt-in caching through the CLI.
// ABOUTME: Uses isolated storage and a real SDK transport to count billable requests.

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Every test process uses disposable storage, including binary subprocesses.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "judgement-tests-")
	if err != nil {
		panic("cannot create isolated test storage")
	}
	for name, leaf := range map[string]string{"XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache"} {
		if err := os.Setenv(name, filepath.Join(root, leaf)); err != nil {
			_ = os.RemoveAll(root)
			panic("cannot isolate test storage")
		}
	}
	code := m.Run()
	_ = os.RemoveAll(root)
	os.Exit(code)
}

func isolateFeatureStorage(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("TYPESAFE_BASE_URL", "")
	t.Setenv("TYPESAFE_DEFAULT_MODEL", "")
}

func TestSetupCommandAndStoredCredential(t *testing.T) {
	isolateFeatureStorage(t)
	out, stderr, code := runCLI(t, context.Background(), strings.NewReader("stored-test-key\n"), "setup", "--key-stdin", "--json")
	if code != 0 || stderr != "" || strings.Contains(out, "stored-test-key") {
		t.Fatalf("setup: exit=%d out=%q stderr=%q", code, out, stderr)
	}
	var setup struct{ Type, ConfigPath string }
	var object map[string]any
	decodeOneJSON(t, out, &object)
	setup.Type, _ = object["type"].(string)
	setup.ConfigPath, _ = object["config_path"].(string)
	if setup.Type != "setup" || setup.ConfigPath != filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "judgement", "config.json") {
		t.Fatalf("wrong setup response: %s", out)
	}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		want := "Bearer stored-test-key"
		if calls.Load() == 2 {
			want = "Bearer environment-test-key"
		}
		if r.Header.Get("Authorization") != want {
			t.Errorf("wrong credential source")
		}
		writeFeatureResponse(t, w)
	}))
	defer srv.Close()
	t.Setenv("TYPESAFE_BASE_URL", srv.URL)
	for _, key := range []string{"", "environment-test-key"} {
		t.Setenv("TYPESAFE_API_KEY", key)
		out, stderr, code = runCLI(t, context.Background(), nil, "--json", "Q", "A", "B")
		if code != 0 || stderr != "" {
			t.Fatalf("judgement: exit=%d out=%q stderr=%q", code, out, stderr)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("API calls = %d", calls.Load())
	}
}

func TestSetupRoutingPreservesJSONAndQuestion(t *testing.T) {
	isolateFeatureStorage(t)
	for _, args := range [][]string{{"setup", "--json", "--help"}, {"--json", "setup", "--help"}, {"setup", "--unknown", "--json"}} {
		out, stderr, _ := runCLI(t, context.Background(), nil, args...)
		if !json.Valid([]byte(out)) || stderr != "" {
			t.Fatalf("args=%q out=%q stderr=%q", args, out, stderr)
		}
	}
	for _, args := range [][]string{
		{"--json", "--", "setup", "A", "B"},
		{"--json", "setup", "A", "B"},
		{"--json", "setup", "-1", "+1"},
		{"--json", "setup", "-1", "-2"},
		{"--json", "setup", "-alpha", "beta"},
	} {
		out, _, code := runCLI(t, context.Background(), nil, args...)
		if code != 1 || !strings.Contains(out, "configuration_error") {
			t.Fatalf("question setup was treated as a subcommand: exit=%d out=%q", code, out)
		}
	}
	out, _, code := runCLI(t, context.Background(), strings.NewReader("disposable\n"), "setup", "--json", "--key-stdin", "extra")
	if code != 2 || !strings.Contains(out, "invalid_arguments") {
		t.Fatalf("setup accepted an extra argument: exit=%d out=%q", code, out)
	}
}

func TestCacheReusesSemanticRequestAcrossInputAndOutputModes(t *testing.T) {
	isolateFeatureStorage(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeFeatureResponse(t, w)
	}))
	defer srv.Close()
	configureTestAPI(t, srv.URL)
	for i, args := range [][]string{
		{"--cache", "--json", "Q", "A", "B"},
		{"--cache", "--json", "--input-json", `{"answers":["A","B"],"question":"Q"}`},
		{"--cache", "--quiet", "Q", "A", "B"},
	} {
		out, stderr, code := runCLI(t, context.Background(), nil, args...)
		if code != 0 || stderr != "" {
			t.Fatalf("request %d: exit=%d out=%q stderr=%q", i, code, out, stderr)
		}
		if i == 2 {
			if out != "A\n" {
				t.Fatalf("quiet cache hit = %q", out)
			}
		} else {
			var result map[string]any
			decodeOneJSON(t, out, &result)
			if result["cached"] != (i == 1) {
				t.Fatalf("cache marker: %s", out)
			}
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("identical request made %d API calls", calls.Load())
	}
	// Opt-out must call the API even when an entry already exists.
	out, _, code := runCLI(t, context.Background(), nil, "--json", "Q", "A", "B")
	if code != 0 || calls.Load() != 2 {
		t.Fatalf("opt-out: calls=%d exit=%d out=%q", calls.Load(), code, out)
	}
}

func TestCacheMissesForRequestIdentityAndExpiry(t *testing.T) {
	for _, change := range []string{"question", "answer order", "model", "credential", "endpoint", "expiry"} {
		t.Run(change, func(t *testing.T) {
			isolateFeatureStorage(t)
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); writeFeatureResponse(t, w) })
			srv := httptest.NewServer(handler)
			defer srv.Close()
			configureTestAPI(t, srv.URL)
			out, _, code := runCLI(t, context.Background(), nil, "--cache", "--json", "Q", "A", "B")
			if code != 0 {
				t.Fatalf("prime: %d %s", code, out)
			}
			args := []string{"--cache", "--json", "Q", "A", "B"}
			switch change {
			case "question":
				args[2] = "Another question"
			case "answer order":
				args[3], args[4] = args[4], args[3]
			case "model":
				args = append([]string{"--model", "another-model"}, args...)
			case "credential":
				t.Setenv("TYPESAFE_API_KEY", "another-test-key")
			case "endpoint":
				second := httptest.NewServer(handler)
				defer second.Close()
				t.Setenv("TYPESAFE_BASE_URL", second.URL)
			case "expiry":
				args = append([]string{"--cache-ttl", "1ns"}, args...)
			}
			out, _, code = runCLI(t, context.Background(), nil, args...)
			if code != 0 || calls.Load() != 2 {
				t.Fatalf("changed request: calls=%d exit=%d out=%q", calls.Load(), code, out)
			}
		})
	}
}

func TestCacheDisabledDoesNotCreateStorage(t *testing.T) {
	isolateFeatureStorage(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeFeatureResponse(t, w) }))
	defer srv.Close()
	configureTestAPI(t, srv.URL)
	out, _, code := runCLI(t, context.Background(), nil, "--json", "Q", "A", "B")
	if code != 0 {
		t.Fatalf("exit=%d out=%q", code, out)
	}
	if _, err := os.Stat(os.Getenv("XDG_CACHE_HOME")); !os.IsNotExist(err) {
		t.Fatalf("cache touched without --cache: %v", err)
	}
}

func TestCacheHumanOutputReportsHitAndMiss(t *testing.T) {
	isolateFeatureStorage(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { writeFeatureResponse(t, w) }))
	defer srv.Close()
	configureTestAPI(t, srv.URL)
	for _, want := range []string{"Cache: miss", "Cache: hit"} {
		out, _, code := runCLI(t, context.Background(), nil, "--cache", "Q", "A", "B")
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("want %q: exit=%d out=%q", want, code, out)
		}
	}
}

func TestCacheFlagsRejectInvalidTTL(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "--cache-ttl", "1h", "Q", "A", "B"},
		{"--json", "--cache", "--cache-ttl", "-1s", "Q", "A", "B"},
		{"--json", "--cache", "--cache-ttl", "nope", "Q", "A", "B"},
		{"--cache-ttl", "--json"},
	} {
		out, stderr, code := runCLI(t, context.Background(), nil, args...)
		if code != 2 {
			t.Fatalf("args=%q exit=%d out=%q stderr=%q", args, code, out, stderr)
		}
		if args[0] == "--json" && (!json.Valid([]byte(out)) || stderr != "") {
			t.Fatalf("JSON invalid TTL: out=%q stderr=%q", out, stderr)
		}
	}
}

func TestCacheTTLPolicy(t *testing.T) {
	for _, tt := range []struct {
		name, model string
		args        []string
		want        time.Duration
	}{
		{"stable alias", "jev-latest", []string{"--cache"}, 24 * time.Hour},
		{"preview alias", "jev-preview", []string{"--cache"}, 24 * time.Hour},
		{"pinned release", "jev-1.13.0", []string{"--cache"}, 0},
		{"unknown selection", "custom-model", []string{"--cache"}, 24 * time.Hour},
		{"explicit expiry", "jev-1.13.0", []string{"--cache", "--cache-ttl", "2h"}, 2 * time.Hour},
		{"explicit forever", "jev-latest", []string{"--cache", "--cache-ttl", "0"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts, _, err := parseOptions(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if got := effectiveCacheTTL(opts, tt.model); got != tt.want {
				t.Fatalf("TTL=%s want=%s", got, tt.want)
			}
		})
	}
}

func writeFeatureResponse(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if _, err := io.WriteString(w, `{"model":"jev-test","answers":{"best":{"type":"choice","choice":"1","probabilities":{"1":0.8,"2":0.2},"confidence":0.75}},"usage":{"input_tokens":10,"output_tokens":0}}`); err != nil {
		t.Errorf("fixture response: %v", err)
	}
}
