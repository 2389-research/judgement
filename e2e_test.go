//go:build e2e

// ABOUTME: Runs the built CLI against TypeSafe, then reuses the real cached result.
// ABOUTME: Requires TYPESAFE_API_KEY and makes one billed request, without mocks.

package main

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
)

func TestLiveJudgement(t *testing.T) {
	if os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("TYPESAFE_API_KEY unset; live API test not run")
	}
	binary := buildCLI(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	input := `{"question":"What is two plus two?","answers":["four","nine"]}`
	out, stderr, code := invokeCLI(t, binary, input, "--cache", "--json", "--timeout", "60s", "--input", "-")
	if code != 0 || stderr != "" {
		t.Fatalf("live CLI: exit=%d stderr=%q stdout=%q", code, stderr, out)
	}
	var result struct {
		Type, Question, Model string
		Winner                struct {
			Index       int
			Answer      string
			Probability float64
		}
		Answers []struct {
			Index       int
			Answer      string
			Probability float64
		}
		Confidence float64
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Type != "result" || result.Model == "" || result.Question != "What is two plus two?" || len(result.Answers) != 2 {
		t.Fatalf("incomplete live result: %s", out)
	}
	if result.Winner.Index != 1 || result.Winner.Answer != "four" {
		t.Fatalf("unexpected answer to arithmetic question: %s", out)
	}
	sum := 0.0
	for i, answer := range result.Answers {
		if answer.Index != i+1 || answer.Answer != []string{"four", "nine"}[i] || answer.Probability < 0 || answer.Probability > 1 {
			t.Fatalf("invalid ordered answer: %+v", answer)
		}
		sum += answer.Probability
	}
	if math.Abs(sum-1) > 0.01 || result.Confidence < 0 || result.Confidence > 1 || result.Winner.Probability != result.Answers[0].Probability {
		t.Fatalf("invalid distribution: %s", out)
	}
	// A cache hit needs no request budget; a second API call cannot pass this.
	cachedOut, cachedErr, cachedCode := invokeCLI(t, binary, input, "--cache", "--json", "--timeout", "1ns", "--input", "-")
	if cachedCode != 0 || cachedErr != "" {
		t.Fatalf("cached CLI: exit=%d stderr=%q stdout=%q", cachedCode, cachedErr, cachedOut)
	}
	var first, cached map[string]any
	if err := json.Unmarshal([]byte(out), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(cachedOut), &cached); err != nil {
		t.Fatal(err)
	}
	if first["cached"] != false || cached["cached"] != true {
		t.Fatal("live call and cached call have incorrect cache markers")
	}
	first["cached"] = true
	if !reflect.DeepEqual(first, cached) {
		t.Fatal("cached result changed the live answer or metadata")
	}
}
