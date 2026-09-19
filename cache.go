// ABOUTME: Identifies and persists successful judgement results for opt-in reuse.
// ABOUTME: Rejects stale, malformed, or request-mismatched cache entries as misses.

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	typesafe "github.com/2389-research/typesafe-go"
)

const (
	cacheSchemaVersion = 1
	maximumCacheBytes  = 16 << 20
)

type cacheEntry struct {
	Schema    int          `json:"schema"`
	CreatedAt time.Time    `json:"created_at"`
	Identity  string       `json:"identity"`
	Result    resultOutput `json:"result"`
}

func cacheIdentity(input requestInput, model, baseURL, apiKey string) string {
	credentialHash := sha256.Sum256([]byte(apiKey))
	canonical := struct {
		Schema       int          `json:"schema"`
		Input        requestInput `json:"input"`
		Model        string       `json:"model"`
		BaseURL      string       `json:"base_url"`
		Instructions string       `json:"instructions"`
		Credential   string       `json:"credential"`
	}{
		Schema:       cacheSchemaVersion,
		Input:        input,
		Model:        model,
		BaseURL:      strings.TrimRight(baseURL, "/"),
		Instructions: choiceInstructions,
		Credential:   hex.EncodeToString(credentialHash[:]),
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		panic(fmt.Sprintf("marshal cache identity: %v", err))
	}
	identityHash := sha256.Sum256(raw)
	return hex.EncodeToString(identityHash[:])
}

func loadCachedResult(identity string, input requestInput, ttl time.Duration, now time.Time) (resultOutput, bool, error) {
	if ttl < 0 {
		return resultOutput{}, false, errors.New("cache TTL must not be negative")
	}
	path, err := cacheFilePath(identity)
	if err != nil {
		return resultOutput{}, false, err
	}
	raw, err := readPrivateFile(path, maximumCacheBytes)
	if errors.Is(err, os.ErrNotExist) {
		return resultOutput{}, false, nil
	}
	if err != nil {
		return resultOutput{}, false, fmt.Errorf("read cache: %w", err)
	}

	var entry cacheEntry
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return resultOutput{}, false, nil
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return resultOutput{}, false, nil
	}
	if entry.Schema != cacheSchemaVersion || entry.Identity != identity || entry.CreatedAt.IsZero() || entry.CreatedAt.After(now) || (ttl > 0 && now.Sub(entry.CreatedAt) >= ttl) {
		return resultOutput{}, false, nil
	}
	if !validateCachedResult(entry.Result, input) {
		return resultOutput{}, false, nil
	}
	return entry.Result, true, nil
}

func saveCachedResult(identity string, result resultOutput, now time.Time) error {
	path, err := cacheFilePath(identity)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := ensurePrivateDirectory(directory); err != nil {
		return fmt.Errorf("prepare cache directory: %w", err)
	}
	entry := cacheEntry{
		Schema:    cacheSchemaVersion,
		CreatedAt: now.UTC(),
		Identity:  identity,
		Result:    result,
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}
	if err := writePrivateFile(path, raw); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}
	return nil
}

func cacheFilePath(identity string) (string, error) {
	if len(identity) != sha256.Size*2 {
		return "", errors.New("cache identity is invalid")
	}
	if _, err := hex.DecodeString(identity); err != nil {
		return "", errors.New("cache identity is invalid")
	}
	directory, err := xdgDirectory("XDG_CACHE_HOME", ".cache")
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, identity+".json"), nil
}

func validateCachedResult(result resultOutput, input requestInput) bool {
	if result.Type != "result" || result.Question != input.Question || strings.TrimSpace(result.Model) == "" || len(result.Answers) != len(input.Answers) {
		return false
	}
	if result.Usage.InputTokens < 0 || result.Usage.OutputTokens < 0 {
		return false
	}

	_, ids := makeChoiceOptions(input.Answers)
	probabilities := make(map[choiceOption]float64, len(ids))
	for i, id := range ids {
		answer := result.Answers[i]
		if answer.Index != i+1 || answer.Answer != input.Answers[i] {
			return false
		}
		probabilities[id] = answer.Probability
	}
	if result.Winner.Index < 1 || result.Winner.Index > len(ids) || result.Winner != result.Answers[result.Winner.Index-1] {
		return false
	}

	rebuilt, err := buildResult(input, ids, typesafe.ChoiceAnswer[choiceOption]{
		Value:         ids[result.Winner.Index-1],
		Probabilities: probabilities,
		Confidence:    result.Confidence,
	}, &typesafe.Result{Model: result.Model, Usage: result.Usage})
	if err != nil {
		return false
	}
	return equalCachedResults(rebuilt, result)
}

func equalCachedResults(left, right resultOutput) bool {
	if left.Type != right.Type || left.Question != right.Question || left.Winner != right.Winner || left.Confidence != right.Confidence || left.Model != right.Model || left.Usage != right.Usage || len(left.Answers) != len(right.Answers) {
		return false
	}
	for i := range left.Answers {
		if left.Answers[i] != right.Answers[i] {
			return false
		}
	}
	return true
}
