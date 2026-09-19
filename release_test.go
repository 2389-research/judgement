// ABOUTME: Verifies release builds expose their linker-injected version.
// ABOUTME: Exercises both human and JSON version output from the real binary.

package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestReleaseVersion(t *testing.T) {
	const releaseVersion = "1.2.3-rc.1"
	binary := filepath.Join(t.TempDir(), "judgement")
	cmd := exec.Command("go", "build", "-ldflags", "-s -w -X main.version="+releaseVersion, "-o", binary, ".") // #nosec G204 -- Build this repository with a fixed test version into t.TempDir.
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build release CLI: %v\n%s", err, out)
	}

	t.Run("human", func(t *testing.T) {
		out, stderr, code := invokeCLI(t, binary, "", "--version")
		if out != "judgement "+releaseVersion+"\n" || stderr != "" || code != 0 {
			t.Fatalf("version stdout=%q stderr=%q exit=%d", out, stderr, code)
		}
	})
	t.Run("json", func(t *testing.T) {
		out, stderr, code := invokeCLI(t, binary, "", "--json", "--version")
		if stderr != "" || code != 0 {
			t.Fatalf("version stdout=%q stderr=%q exit=%d", out, stderr, code)
		}
		var envelope struct {
			Type    string `json:"type"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(out), &envelope); err != nil {
			t.Fatalf("decode version: %v", err)
		}
		if envelope.Type != "version" || envelope.Version != releaseVersion {
			t.Fatalf("version envelope=%+v", envelope)
		}
	})
}
