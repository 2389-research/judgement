// ABOUTME: Verifies cache identity, persistence, expiry, and result validation.
// ABOUTME: Keeps cache tests isolated from the machine's real HOME and XDG paths.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	typesafe "github.com/2389-research/typesafe-go"
)

func TestCacheIdentityUsesSemanticRequestInputs(t *testing.T) {
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}
	identity := cacheIdentity(input, "jev-latest", "https://api.example.test", "secret-one")

	if got := cacheIdentity(input, "jev-latest", "https://api.example.test/", "secret-one"); got != identity {
		t.Fatalf("identity with trailing base URL slash = %q, want %q", got, identity)
	}

	tests := []struct {
		name    string
		input   requestInput
		model   string
		baseURL string
		apiKey  string
	}{
		{"question", requestInput{Question: "Pick another", Answers: input.Answers}, "jev-latest", "https://api.example.test", "secret-one"},
		{"answer order", requestInput{Question: input.Question, Answers: []string{"Beta", "Alpha"}}, "jev-latest", "https://api.example.test", "secret-one"},
		{"model", input, "jev-2", "https://api.example.test", "secret-one"},
		{"base URL", input, "jev-latest", "https://other.example.test", "secret-one"},
		{"credential", input, "jev-latest", "https://api.example.test", "secret-two"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cacheIdentity(tt.input, tt.model, tt.baseURL, tt.apiKey); got == identity {
				t.Fatalf("cacheIdentity() = unchanged %q", got)
			}
		})
	}

	if strings.Contains(identity, "secret-one") {
		t.Fatal("cache identity contains the API key")
	}
}

func TestCacheRejectsInvalidIdentity(t *testing.T) {
	useTemporaryCache(t)
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}
	result := validCachedResult(input)

	if err := saveCachedResult("../escape", result, time.Now()); err == nil {
		t.Fatal("saveCachedResult accepted a non-SHA-256 identity")
	}
	if _, _, err := loadCachedResult("../escape", input, time.Hour, time.Now()); err == nil {
		t.Fatal("loadCachedResult accepted a non-SHA-256 identity")
	}
}

func TestSaveAndLoadCachedResult(t *testing.T) {
	cacheRoot := useTemporaryCache(t)
	now := time.Date(2026, 9, 19, 12, 34, 56, 0, time.FixedZone("test", -5*60*60))
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}
	identity := cacheIdentity(input, "jev-latest", "https://api.example.test", "secret")
	want := validCachedResult(input)

	if err := saveCachedResult(identity, want, now); err != nil {
		t.Fatalf("saveCachedResult: %v", err)
	}
	got, hit, err := loadCachedResult(identity, input, 24*time.Hour, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("loadCachedResult: %v", err)
	}
	if !hit {
		t.Fatal("loadCachedResult reported a miss, want hit")
	}
	assertResultEqual(t, got, want)

	path := filepath.Join(cacheRoot, "judgement", identity+".json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cache file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cache file mode = %04o, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- Read the cache fixture under this test's temporary directory.
	if err != nil {
		t.Fatalf("read cache file: %v", err)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatal("cache file contains the API key")
	}
	var stored struct {
		Schema    int          `json:"schema"`
		CreatedAt time.Time    `json:"created_at"`
		Identity  string       `json:"identity"`
		Result    resultOutput `json:"result"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decode cache file: %v", err)
	}
	if stored.Schema != 1 || !stored.CreatedAt.Equal(now.UTC()) || stored.Identity != identity {
		t.Fatalf("cache metadata = schema %d, time %s, identity %q", stored.Schema, stored.CreatedAt, stored.Identity)
	}
	assertResultEqual(t, stored.Result, want)
}

func TestLoadCachedResultTreatsMissingExpiredAndMalformedEntriesAsMisses(t *testing.T) {
	cacheRoot := useTemporaryCache(t)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}
	identity := cacheIdentity(input, "jev-latest", "https://api.example.test", "secret")

	if _, hit, err := loadCachedResult(identity, input, time.Hour, now); err != nil || hit {
		t.Fatalf("missing load = hit %v, err %v; want miss", hit, err)
	}
	if err := saveCachedResult(identity, validCachedResult(input), now.Add(-time.Hour)); err != nil {
		t.Fatalf("save expired result: %v", err)
	}
	if _, hit, err := loadCachedResult(identity, input, time.Hour, now); err != nil || hit {
		t.Fatalf("expired load = hit %v, err %v; want miss", hit, err)
	}

	path := filepath.Join(cacheRoot, "judgement", identity+".json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write malformed cache: %v", err)
	}
	if _, hit, err := loadCachedResult(identity, input, time.Hour, now); err != nil || hit {
		t.Fatalf("malformed load = hit %v, err %v; want miss", hit, err)
	}
}

func TestLoadCachedResultKeepsOldEntryWhenTTLIsZero(t *testing.T) {
	useTemporaryCache(t)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}
	identity := cacheIdentity(input, "jev-1.2.3", "https://api.example.test", "secret")
	want := validCachedResult(input)

	if err := saveCachedResult(identity, want, now.Add(-365*24*time.Hour)); err != nil {
		t.Fatalf("save old result: %v", err)
	}
	got, hit, err := loadCachedResult(identity, input, 0, now)
	if err != nil {
		t.Fatalf("loadCachedResult: %v", err)
	}
	if !hit {
		t.Fatal("loadCachedResult reported a miss for a zero TTL, want hit")
	}
	assertResultEqual(t, got, want)
}

func TestLoadCachedResultRejectsNegativeTTL(t *testing.T) {
	useTemporaryCache(t)
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}

	if _, _, err := loadCachedResult(strings.Repeat("0", 64), input, -time.Second, time.Now()); err == nil {
		t.Fatal("loadCachedResult accepted a negative TTL")
	}
}

func TestLoadCachedResultTreatsWrongMetadataAsAMiss(t *testing.T) {
	cacheRoot := useTemporaryCache(t)
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}
	identity := cacheIdentity(input, "jev-latest", "https://api.example.test", "secret")

	tests := []struct {
		name   string
		change func(*cacheEntry)
	}{
		{"schema", func(entry *cacheEntry) { entry.Schema++ }},
		{"identity", func(entry *cacheEntry) { entry.Identity = strings.Repeat("0", 64) }},
		{"future timestamp", func(entry *cacheEntry) { entry.CreatedAt = now.Add(time.Second) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := cacheEntry{Schema: cacheSchemaVersion, CreatedAt: now, Identity: identity, Result: validCachedResult(input)}
			tt.change(&entry)
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatalf("marshal cache entry: %v", err)
			}
			path := filepath.Join(cacheRoot, "judgement", identity+".json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatalf("create cache directory: %v", err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatalf("write cache entry: %v", err)
			}
			if _, hit, err := loadCachedResult(identity, input, time.Hour, now); err != nil || hit {
				t.Fatalf("load = hit %v, err %v; want metadata miss", hit, err)
			}
		})
	}
}

func TestLoadCachedResultRejectsMismatchedOrInvalidResults(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}

	tests := []struct {
		name   string
		change func(*resultOutput)
	}{
		{"question", func(result *resultOutput) { result.Question = "Another question" }},
		{"answer index", func(result *resultOutput) { result.Answers[0].Index = 2 }},
		{"answer text", func(result *resultOutput) { result.Answers[0].Answer = "Gamma" }},
		{"winner", func(result *resultOutput) { result.Winner = result.Answers[1] }},
		{"model", func(result *resultOutput) { result.Model = "" }},
		{"coverage", func(result *resultOutput) { result.Answers = result.Answers[:1] }},
		{"probability range", func(result *resultOutput) { result.Answers[1].Probability = -0.1 }},
		{"probability sum", func(result *resultOutput) { result.Answers[1].Probability = 0.2 }},
		{"confidence", func(result *resultOutput) { result.Confidence = 1.1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useTemporaryCache(t)
			identity := cacheIdentity(input, "jev-latest", "https://api.example.test", "secret")
			result := validCachedResult(input)
			tt.change(&result)
			if err := saveCachedResult(identity, result, now); err != nil {
				t.Fatalf("saveCachedResult: %v", err)
			}
			if _, hit, err := loadCachedResult(identity, input, time.Hour, now); err != nil || hit {
				t.Fatalf("load = hit %v, err %v; want invalid cache miss", hit, err)
			}
		})
	}
}

func TestLoadCachedResultReportsFilesystemErrors(t *testing.T) {
	cacheRoot := useTemporaryCache(t)
	input := requestInput{Question: "Pick one", Answers: []string{"Alpha", "Beta"}}
	identity := cacheIdentity(input, "jev-latest", "https://api.example.test", "secret")
	path := filepath.Join(cacheRoot, "judgement", identity+".json")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("create directory in place of cache file: %v", err)
	}

	if _, _, err := loadCachedResult(identity, input, time.Hour, time.Now()); err == nil {
		t.Fatal("loadCachedResult returned nil error for a non-regular cache file")
	}
}

func useTemporaryCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	return filepath.Join(root, "cache")
}

func validCachedResult(input requestInput) resultOutput {
	return resultOutput{
		Type:       "result",
		Question:   input.Question,
		Winner:     answerOutput{Index: 1, Answer: input.Answers[0], Probability: 0.75},
		Answers:    []answerOutput{{Index: 1, Answer: input.Answers[0], Probability: 0.75}, {Index: 2, Answer: input.Answers[1], Probability: 0.25}},
		Confidence: 0.8,
		Model:      "jev-2026-09-01",
		Usage:      typesafe.Usage{InputTokens: 12, OutputTokens: 4},
	}
}

func assertResultEqual(t *testing.T, got, want resultOutput) {
	t.Helper()
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)
	if gotErr != nil || wantErr != nil {
		t.Fatalf("marshal results: got error %v, want error %v", gotErr, wantErr)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("result = %s, want %s", gotJSON, wantJSON)
	}
}
