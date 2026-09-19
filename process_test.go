// ABOUTME: Exercises the built CLI's streams and exit statuses as a real process.
// ABOUTME: Local scenarios need no API; the live API scenario lives in e2e_test.go.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func buildCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "judgement")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	return binary
}

func invokeCLI(t *testing.T, binary, input string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Stdin = strings.NewReader(input)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return out.String(), errOut.String(), code
}

func TestProcessJSONOutcomes(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	binary := buildCLI(t)
	for _, tt := range []struct {
		name, input, kind, errorCode string
		args                         []string
		exit                         int
	}{
		{name: "help", args: []string{"--json", "--help"}, kind: "help"},
		{name: "help then json", args: []string{"--help", "--json"}, kind: "help"},
		{name: "version", args: []string{"--json", "--version"}, kind: "version"},
		{name: "missing arguments", args: []string{"--json"}, kind: "error", errorCode: "invalid_arguments", exit: 2},
		{name: "unknown flag", args: []string{"--unknown", "--json"}, kind: "error", errorCode: "invalid_arguments", exit: 2},
		{name: "malformed input", args: []string{"--json", "--input", "-"}, input: "{", kind: "error", errorCode: "invalid_arguments", exit: 2},
		{name: "trailing input", args: []string{"--json", "--input", "-"}, input: `{"question":"Q","answers":["A","B"]} {}`, kind: "error", errorCode: "invalid_arguments", exit: 2},
		{name: "unknown input field", args: []string{"--json", "--input", "-"}, input: `{"question":"Q","answers":["A","B"],"extra":1}`, kind: "error", errorCode: "invalid_arguments", exit: 2},
		{name: "missing credentials positional", args: []string{"--json", "Q", "A", "B"}, kind: "error", errorCode: "configuration_error", exit: 1},
		{name: "missing credentials stdin", args: []string{"--json", "--input", "-"}, input: `{"question":"Q","answers":["A","B"]}`, kind: "error", errorCode: "configuration_error", exit: 1},
		{name: "missing credentials inline", args: []string{"--json", "--input-json", `{"question":"Q","answers":["A","B"]}`}, kind: "error", errorCode: "configuration_error", exit: 1},
		{name: "mixed JSON modes", args: []string{"--json", "--input", "-", "--input-json", `{"question":"Q","answers":["A","B"]}`}, kind: "error", errorCode: "invalid_arguments", exit: 2},
		{name: "mixed input", args: []string{"--json", "--input", "-", "Q", "A", "B"}, input: `{"question":"Q","answers":["A","B"]}`, kind: "error", errorCode: "invalid_arguments", exit: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, stderr, code := invokeCLI(t, binary, tt.input, tt.args...)
			if code != tt.exit || stderr != "" {
				t.Fatalf("exit=%d want=%d stderr=%q stdout=%q", code, tt.exit, stderr, out)
			}
			var envelope struct {
				Type  string                         `json:"type"`
				Error struct{ Code, Message string } `json:"error"`
			}
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatalf("not one JSON object: %v: %q", err, out)
			}
			if envelope.Type != tt.kind || envelope.Error.Code != tt.errorCode {
				t.Fatalf("unexpected envelope: %s", out)
			}
			if tt.errorCode != "" && envelope.Error.Message == "" {
				t.Fatal("error has no useful message")
			}
		})
	}
}

func TestProcessHumanAndFileInput(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	binary := buildCLI(t)
	out, stderr, code := invokeCLI(t, binary, "", "--help")
	if code != 0 || stderr != "" || !strings.Contains(out, "--json") || !strings.Contains(out, "--input") {
		t.Fatalf("help: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	out, stderr, code = invokeCLI(t, binary, "", "Q", "A")
	if code != 2 || out != "" || stderr == "" {
		t.Fatalf("usage error: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	file := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(file, []byte(`{"question":"Q","answers":["A","B"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	out, stderr, code = invokeCLI(t, binary, "", "--json", "--input", file)
	if code != 1 || stderr != "" || !strings.Contains(out, `"configuration_error"`) {
		t.Fatalf("file input did not reach configuration: exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
}

func TestProcessSignalWhileReadingStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal scenario")
	}
	binary := buildCLI(t)
	cmd := exec.Command(binary, "--json", "--input", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	// More than a pipe buffer: writing must wait until the CLI reads stdin.
	if _, err := stdin.Write([]byte(strings.Repeat(" ", 256<<10))); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	assertInputProcessTerminates(t, cmd, &stdout, &stderr)
}

func TestProcessSignalWhileReadingNamedPipe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX named pipe scenario")
	}
	pipe := filepath.Join(t.TempDir(), "request.pipe")
	if out, err := exec.Command("mkfifo", pipe).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo: %v: %s", err, out)
	}
	binary := buildCLI(t)
	cmd := exec.Command(binary, "--json", "--input", pipe)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(pipe, os.O_WRONLY, 0)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	if _, err := io.WriteString(writer, strings.Repeat(" ", 256<<10)); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	assertInputProcessTerminates(t, cmd, &stdout, &stderr)
}

func assertInputProcessTerminates(t *testing.T, cmd *exec.Cmd, stdout, stderr *bytes.Buffer) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	select {
	case err := <-finished:
		if err == nil || stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
			t.Fatalf("signal response: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-finished
		t.Fatal("CLI ignored termination while waiting for input")
	}
}
