// ABOUTME: Verifies Judgement's argument, transport, and output contracts.
// ABOUTME: Uses an HTTP test server with the real TypeSafe SDK request path.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunJSONResultUsesOneChoiceAndPreservesAnswerOrder(t *testing.T) {
	var request struct {
		State     string `json:"state"`
		Model     string `json:"model"`
		Questions map[string]struct {
			Type         string            `json:"type"`
			Instructions string            `json:"instructions"`
			Criteria     map[string]string `json:"criteria"`
		} `json:"questions"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("request = %s %s, want POST /v1/systemone", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"model":"jev-9.1",
			"answers":{"best":{"type":"choice","choice":"2","probabilities":{"1":0.2,"2":0.7,"3":0.1},"confidence":0.8}},
			"usage":{"input_tokens":12,"output_tokens":4}
		}`)
	}))
	defer srv.Close()
	configureTestAPI(t, srv.URL)

	stdout, stderr, exit := runCLI(t, context.Background(), nil,
		"--json", "--model", "jev-test", "Best snack?", "chips", "apple", "toast")
	if exit != 0 {
		t.Fatalf("exit = %d, stderr = %q, stdout = %q", exit, stderr, stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}

	var got struct {
		Type     string `json:"type"`
		Question string `json:"question"`
		Winner   struct {
			Index       int     `json:"index"`
			Answer      string  `json:"answer"`
			Probability float64 `json:"probability"`
		} `json:"winner"`
		Answers []struct {
			Index       int     `json:"index"`
			Answer      string  `json:"answer"`
			Probability float64 `json:"probability"`
		} `json:"answers"`
		Confidence float64 `json:"confidence"`
		Model      string  `json:"model"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	decodeOneJSON(t, stdout, &got)
	if got.Type != "result" || got.Question != "Best snack?" {
		t.Errorf("result header = %#v", got)
	}
	if got.Winner.Index != 2 || got.Winner.Answer != "apple" || got.Winner.Probability != 0.7 {
		t.Errorf("winner = %#v", got.Winner)
	}
	if len(got.Answers) != 3 || got.Answers[0].Answer != "chips" || got.Answers[2].Answer != "toast" {
		t.Errorf("answers = %#v", got.Answers)
	}
	if got.Confidence != 0.8 || got.Model != "jev-9.1" || got.Usage.InputTokens != 12 || got.Usage.OutputTokens != 4 {
		t.Errorf("result metadata = %#v", got)
	}

	if request.State != "Best snack?" || request.Model != "jev-test" || len(request.Questions) != 1 {
		t.Errorf("request = %#v", request)
	}
	question := request.Questions["best"]
	if question.Type != "choice" || question.Instructions != "Choose the best answer to the question." {
		t.Errorf("choice question = %#v", question)
	}
	if len(question.Criteria) != 3 || question.Criteria["1"] != "chips" || question.Criteria["2"] != "apple" || question.Criteria["3"] != "toast" {
		t.Errorf("choice criteria = %#v", question.Criteria)
	}
}

func TestRunPadsChoiceIDsAtTenAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Questions map[string]struct {
				Criteria map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		criteria := request.Questions["best"].Criteria
		if criteria["01"] != "a1" || criteria["10"] != "a10" {
			t.Errorf("criteria IDs = %#v", criteria)
		}
		probabilities := make(map[string]float64, 10)
		for i := 1; i <= 10; i++ {
			probabilities[fmt.Sprintf("%02d", i)] = 0.1
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"model": "jev-test", "usage": map[string]int{},
			"answers": map[string]any{"best": map[string]any{
				"type": "choice", "choice": "01", "probabilities": probabilities, "confidence": 0.1,
			}},
		}); err != nil {
			t.Errorf("encode fixture: %v", err)
		}
	}))
	defer srv.Close()
	configureTestAPI(t, srv.URL)

	args := []string{"--quiet", "question"}
	for i := 1; i <= 10; i++ {
		args = append(args, fmt.Sprintf("a%d", i))
	}
	stdout, stderr, exit := runCLI(t, context.Background(), nil, args...)
	if exit != 0 || stdout != "a1\n" || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestRunHumanResultIncludesDistribution(t *testing.T) {
	srv := successfulServer(t, "2", map[string]float64{"1": 0.25, "2": 0.75}, 0.75)
	defer srv.Close()
	configureTestAPI(t, srv.URL)

	stdout, stderr, exit := runCLI(t, context.Background(), nil, "Question?", "first", "second")
	if exit != 0 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	for _, want := range []string{"Winner: second", "1. first: 0.250000", "2. second: 0.750000"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
	}
}

func TestRunReadsStrictJSONInput(t *testing.T) {
	srv := successfulServer(t, "1", map[string]float64{"1": 0.6, "2": 0.4}, 0.6)
	defer srv.Close()
	configureTestAPI(t, srv.URL)

	dir := t.TempDir()
	inputPath := dir + "/request.json"
	if err := os.WriteFile(inputPath, []byte(`{"question":"Pick","answers":["yes","no"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--json", "--input", inputPath)
	if exit != 0 || stderr != "" {
		t.Fatalf("file input: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}

	stdout, stderr, exit = runCLI(t, context.Background(), strings.NewReader(`{"question":"Pick","answers":["yes","no"]}`), "--input=-", "--quiet")
	if exit != 0 || stdout != "yes\n" || stderr != "" {
		t.Fatalf("stdin input: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}

	stdout, stderr, exit = runCLI(t, context.Background(), nil, "--input-json", `{"question":"Pick","answers":["yes","no"]}`, "--quiet")
	if exit != 0 || stdout != "yes\n" || stderr != "" {
		t.Fatalf("literal input: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"too few positionals", []string{"--json", "question", "one"}, "", "at least two answers"},
		{"blank question", []string{"--json", " ", "one", "two"}, "", "question cannot be blank"},
		{"blank answer", []string{"--json", "q", "one", " "}, "", "answer 2 cannot be blank"},
		{"duplicate answer", []string{"--json", "q", "same", "same"}, "", "answers must be unique"},
		{"mixed input", []string{"--json", "--input", "-", "q", "a", "b"}, `{}`, "cannot mix"},
		{"two JSON inputs", []string{"--json", "--input", "-", "--input-json", `{}`}, `{}`, "only one JSON input"},
		{"unknown field", []string{"--json", "--input", "-"}, `{"question":"q","answers":["a","b"],"extra":true}`, "unknown field"},
		{"trailing JSON", []string{"--json", "--input", "-"}, `{"question":"q","answers":["a","b"]}{}`, "one JSON object"},
		{"malformed JSON", []string{"--json", "--input", "-"}, `{`, "valid JSON"},
		{"quiet JSON", []string{"--json", "--quiet", "q", "a", "b"}, "", "cannot use --quiet with --json"},
		{"bad timeout", []string{"--json", "--timeout", "0s", "q", "a", "b"}, "", "timeout must be positive"},
		{"blank model", []string{"--json", "--model=", "q", "a", "b"}, "", "model cannot be blank"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exit := runCLI(t, context.Background(), strings.NewReader(tc.stdin), tc.args...)
			if exit != 2 {
				t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", exit, stdout, stderr)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
			var envelope errorEnvelope
			decodeOneJSON(t, stdout, &envelope)
			if envelope.Type != "error" || envelope.Error.Code != "invalid_arguments" || !strings.Contains(envelope.Error.Message, tc.want) {
				t.Errorf("envelope = %#v, want message containing %q", envelope, tc.want)
			}
		})
	}
}

func TestRunBoundsJSONInput(t *testing.T) {
	stdout, stderr, exit := runCLI(t, context.Background(), strings.NewReader(strings.Repeat("x", 1<<20+1)), "--json", "--input", "-")
	if exit != 2 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stdout, &envelope)
	if envelope.Error.Code != "invalid_arguments" || !strings.Contains(envelope.Error.Message, "1 MiB") {
		t.Errorf("envelope = %#v", envelope)
	}
}

func TestRunRecognizesJSONModeAfterBadFlag(t *testing.T) {
	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--not-a-flag", "--json")
	if exit != 2 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stdout, &envelope)
	if envelope.Error.Code != "invalid_arguments" {
		t.Errorf("envelope = %#v", envelope)
	}
}

func TestRunJSONPrescanAcceptsFlagBooleanSyntax(t *testing.T) {
	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--not-a-flag", "--json=TRUE")
	if exit != 2 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stdout, &envelope)
	if envelope.Error.Code != "invalid_arguments" {
		t.Errorf("envelope = %#v", envelope)
	}
}

func TestRunJSONPrescanRespectsValuesAndBoundaries(t *testing.T) {
	cases := [][]string{
		{"--model", "--json", "q", "a", "b"},
		{"--input-json", "--json"},
		{"q", "a", "b", "--json"},
		{"--", "--json", "a", "b"},
	}
	for _, args := range cases {
		stdout, stderr, exit := runCLI(t, context.Background(), nil, args...)
		if exit == 0 || stdout != "" || stderr == "" {
			t.Errorf("args=%q exit=%d stdout=%q stderr=%q; want human error", args, exit, stdout, stderr)
		}
	}
}

func TestRunHelpAndVersionJSON(t *testing.T) {
	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--json", "--help")
	if exit != 0 || stderr != "" {
		t.Fatalf("help exit=%d stderr=%q", exit, stderr)
	}
	var help map[string]any
	decodeOneJSON(t, stdout, &help)
	for _, field := range []string{"usage", "flags", "input_format", "environment", "exit_codes", "examples"} {
		if _, ok := help[field]; !ok {
			t.Errorf("help lacks %q: %#v", field, help)
		}
	}
	if help["type"] != "help" {
		t.Errorf("help type = %#v", help["type"])
	}

	stdout, stderr, exit = runCLI(t, context.Background(), nil, "--json", "--version")
	if exit != 0 || stderr != "" {
		t.Fatalf("version exit=%d stderr=%q", exit, stderr)
	}
	var version map[string]any
	decodeOneJSON(t, stdout, &version)
	if version["type"] != "version" || version["version"] != "dev" {
		t.Errorf("version = %#v", version)
	}
}

func TestRunHumanHelpListsEveryStructuredFlag(t *testing.T) {
	jsonOutput, _, _ := runCLI(t, context.Background(), nil, "--json", "--help")
	var structured struct {
		Flags []struct {
			Name string `json:"name"`
		} `json:"flags"`
	}
	decodeOneJSON(t, jsonOutput, &structured)

	humanOutput, stderr, exit := runCLI(t, context.Background(), nil, "--help")
	if exit != 0 || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	for _, flag := range structured.Flags {
		if !strings.Contains(humanOutput, flag.Name) {
			t.Errorf("human help lacks structured flag %q", flag.Name)
		}
	}
	if !strings.Contains(humanOutput, `{"question":"...","answers":["...","..."]}`) {
		t.Errorf("human help lacks unescaped JSON example: %q", humanOutput)
	}
}

func TestRunHumanHelpVersionAndFailuresUseExpectedStreams(t *testing.T) {
	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--help")
	if exit != 0 || !strings.Contains(stdout, "Usage: judgement") || stderr != "" {
		t.Errorf("help exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runCLI(t, context.Background(), nil, "--version")
	if exit != 0 || stdout != "judgement dev\n" || stderr != "" {
		t.Errorf("version exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runCLI(t, context.Background(), nil, "q", "a")
	if exit != 2 || stdout != "" || !strings.Contains(stderr, "at least two answers") {
		t.Errorf("failure exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

func TestRunConfigurationErrorDoesNotLeakCredential(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "secret-do-not-print")
	t.Setenv("TYPESAFE_BASE_URL", "://bad")
	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--json", "q", "a", "b")
	if exit != 1 || stderr != "" || strings.Contains(stdout, "secret-do-not-print") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stdout, &envelope)
	if envelope.Error.Code != "configuration_error" {
		t.Errorf("envelope = %#v", envelope)
	}
}

func TestRunMissingAPIKeyIsConfigurationError(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	t.Setenv("TYPESAFE_BASE_URL", "")
	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--json", "q", "a", "b")
	if exit != 1 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stdout, &envelope)
	if envelope.Error.Code != "configuration_error" || !strings.Contains(envelope.Error.Message, "TYPESAFE_API_KEY") {
		t.Errorf("envelope = %#v", envelope)
	}
}

func TestRunAPIErrorDoesNotExposeResponseOrCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"secret server detail"}`)
	}))
	defer srv.Close()
	configureTestAPI(t, srv.URL)
	t.Setenv("TYPESAFE_API_KEY", "secret-key")

	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--json", "q", "a", "b")
	if exit != 1 || stderr != "" || strings.Contains(stdout, "secret server detail") || strings.Contains(stdout, "secret-key") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stdout, &envelope)
	if envelope.Error.Code != "api_error" || !strings.Contains(envelope.Error.Message, "401") {
		t.Errorf("envelope = %#v", envelope)
	}
}

func TestRunRejectsInvalidResponses(t *testing.T) {
	cases := []struct {
		name          string
		choice        string
		probabilities map[string]float64
		confidence    float64
	}{
		{"missing coverage", "1", map[string]float64{"1": 1}, 1},
		{"out of range", "1", map[string]float64{"1": 1.1, "2": -0.1}, 1},
		{"sum", "1", map[string]float64{"1": 0.4, "2": 0.4}, 0.5},
		{"winner not maximum", "1", map[string]float64{"1": 0.4, "2": 0.6}, 0.5},
		{"winner just below maximum", "1", map[string]float64{"1": 0.49999975, "2": 0.50000025}, 0.5},
		{"confidence", "1", map[string]float64{"1": 0.6, "2": 0.4}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := successfulServer(t, tc.choice, tc.probabilities, tc.confidence)
			defer srv.Close()
			configureTestAPI(t, srv.URL)
			stdout, stderr, exit := runCLI(t, context.Background(), nil, "--json", "q", "a", "b")
			if exit != 1 || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
			}
			var envelope errorEnvelope
			decodeOneJSON(t, stdout, &envelope)
			if envelope.Error.Code != "invalid_response" {
				t.Errorf("envelope = %#v", envelope)
			}
		})
	}
}

func TestValidUnitFloatRejectsNonfiniteValues(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if validUnitFloat(value) {
			t.Errorf("validUnitFloat(%v) = true, want false", value)
		}
	}
}

func TestRunTimeoutCancelsRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	configureTestAPI(t, srv.URL)

	started := time.Now()
	stdout, stderr, exit := runCLI(t, context.Background(), nil, "--json", "--timeout", "20ms", "q", "a", "b")
	if exit != 1 || stderr != "" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	var envelope errorEnvelope
	decodeOneJSON(t, stdout, &envelope)
	if envelope.Error.Code != "api_error" {
		t.Errorf("envelope = %#v", envelope)
	}
	if elapsed := time.Since(started); elapsed >= 100*time.Millisecond {
		t.Errorf("request returned after %s, want timeout before server response", elapsed)
	}
}

func TestRunReportsOutputFailure(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	var stderr bytes.Buffer
	exit := run(context.Background(), []string{"--json", "q", "a"}, nil, failingWriter{}, &stderr)
	if exit != 1 || !strings.Contains(stderr.String(), "write") {
		t.Errorf("exit=%d stderr=%q", exit, stderr.String())
	}
}

type errorEnvelope struct {
	Type  string `json:"type"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write broke") }

func configureTestAPI(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	t.Setenv("TYPESAFE_BASE_URL", baseURL)
	t.Setenv("TYPESAFE_DEFAULT_MODEL", "jev-env")
}

func successfulServer(t *testing.T, choice string, probabilities map[string]float64, confidence float64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"model": "jev-test", "usage": map[string]int{"input_tokens": 1, "output_tokens": 1},
			"answers": map[string]any{"best": map[string]any{
				"type": "choice", "choice": choice, "probabilities": probabilities, "confidence": confidence,
			}},
		}); err != nil {
			t.Errorf("encode fixture: %v", err)
		}
	}))
}

func runCLI(t *testing.T, ctx context.Context, stdin io.Reader, args ...string) (string, string, int) {
	t.Helper()
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	var stdout, stderr bytes.Buffer
	exit := run(ctx, args, stdin, &stdout, &stderr)
	return stdout.String(), stderr.String(), exit
}

func decodeOneJSON(t *testing.T, raw string, dst any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	if err := decoder.Decode(dst); err != nil {
		t.Fatalf("decode JSON %q: %v", raw, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("extra JSON in %q: %v", raw, err)
	}
}
