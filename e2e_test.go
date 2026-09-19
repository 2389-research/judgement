//go:build e2e

// ABOUTME: Runs the built CLI against the real TypeSafe API with JSON stdin.
// ABOUTME: Requires TYPESAFE_API_KEY and makes one billed request, without mocks.

package main

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestLiveJudgement(t *testing.T) {
	if os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("TYPESAFE_API_KEY unset; live API test not run")
	}
	binary := buildCLI(t)
	input := `{"question":"What is two plus two?","answers":["four","nine"]}`
	out, stderr, code := invokeCLI(t, binary, input, "--json", "--timeout", "60s", "--input", "-")
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
}
