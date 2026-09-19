// ABOUTME: Verifies formula rendering against actual local release artifacts.
// ABOUTME: Covers platform selection, corrupted inputs, and command execution.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func releaseFixture(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "metadata.json", fmt.Sprintf(`{"project_name":"judgement","tag":"v%s","version":"%s"}`, version, version))
	var checksums strings.Builder
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
		name := "judgement_" + version + "_" + platform + ".tar.gz"
		writeFixture(t, dir, name, platform)
		fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256([]byte(platform)), name)
	}
	writeFixture(t, dir, "checksums.txt", checksums.String())
	return dir
}

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestFormulaPlatforms(t *testing.T) {
	for _, version := range []string{"1.2.3", "1.2.3+build-7", "0.0.0-SNAPSHOT-abc123"} {
		t.Run(version, func(t *testing.T) {
			dir := releaseFixture(t, version)
			var out, stderr bytes.Buffer
			if code := run([]string{"--dist", dir}, &out, &stderr); code != 0 {
				t.Fatalf("exit %d: %s", code, &stderr)
			}
			for _, platform := range []struct{ os, cpu, archive string }{
				{"macos", "intel", "darwin_amd64"}, {"macos", "arm", "darwin_arm64"},
				{"linux", "intel", "linux_amd64"}, {"linux", "arm", "linux_arm64"},
			} {
				want := fmt.Sprintf("  on_%s do\n    on_%s do\n      url \"https://github.com/2389-research/judgement/releases/download/v%s/judgement_%s_%s.tar.gz\"\n      sha256 \"%x\"", platform.os, platform.cpu, version, version, platform.archive, sha256.Sum256([]byte(platform.archive)))
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing mapping %s: %s", platform.archive, &out)
				}
			}
			for _, want := range []string{"class Judgement < Formula", `bin.install "judgement"`, `system "#{bin}/judgement", "--version"`} {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
		})
	}
}

func TestRejectBrokenRelease(t *testing.T) {
	for _, tc := range []struct{ name, file, content, want string }{
		{"missing archive", "judgement_1.2.3_linux_arm64.tar.gz", "", "archive"},
		{"corrupt archive", "judgement_1.2.3_linux_arm64.tar.gz", "corrupted", "checksum"},
		{"missing checksum", "checksums.txt", "", "checksum"},
		{"invalid checksum", "checksums.txt", "oops  judgement_1.2.3_linux_arm64.tar.gz\n", "checksum"},
		{"wrong project", "metadata.json", `{"project_name":"other","tag":"v1.2.3","version":"1.2.3"}`, "metadata"},
		{"traversal", "metadata.json", `{"project_name":"judgement","tag":"v1.2.3","version":"../../evil"}`, "metadata"},
		{"ruby injection", "metadata.json", `{"project_name":"judgement","tag":"#{system('evil')}","version":"1.2.3"}`, "metadata"},
		{"unicode", "metadata.json", `{"project_name":"judgement","tag":"v1.2.3","version":"1.2.3-☃"}`, "metadata"},
		{"invalid json", "metadata.json", `{`, "metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := releaseFixture(t, "1.2.3")
			if tc.name == "missing archive" {
				if err := os.Remove(filepath.Join(dir, tc.file)); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFixture(t, dir, tc.file, tc.content)
			}
			var out, stderr bytes.Buffer
			if code := run([]string{"--dist", dir}, &out, &stderr); code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit %d stdout %q stderr %q", code, &out, &stderr)
			}
		})
	}
}

func TestCommandArguments(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--unknown"}, {"extra"}} {
		var out, stderr bytes.Buffer
		code := run(args, &out, &stderr)
		if args[0] == "--help" {
			if code != 0 || !strings.Contains(out.String(), "--dist") {
				t.Fatalf("help: %d %s %s", code, &out, &stderr)
			}
		} else if code != 1 || stderr.Len() == 0 || out.Len() != 0 {
			t.Fatalf("bad args: %d %s", code, &stderr)
		}
	}
}

func TestCommandEntrypoint(t *testing.T) {
	dir := releaseFixture(t, "1.2.3")
	cmd := exec.CommandContext(context.Background(), "go", "run", ".", "--dist", dir) // #nosec G204 -- fixed Go command with a test-owned temporary directory.
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("class Judgement < Formula")) {
		t.Fatalf("command: %v %s", err, out)
	}
}
