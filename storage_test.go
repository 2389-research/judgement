// ABOUTME: Verifies private XDG directory and atomic file storage behavior.
// ABOUTME: Keeps all filesystem effects inside per-test temporary directories.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestXDGDirectoryUsesAbsoluteValueAndFallsBackForRelativeValue(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(t.TempDir(), "config")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	got, err := xdgDirectory("XDG_CONFIG_HOME", ".config")
	if err != nil || got != filepath.Join(xdg, "judgement") {
		t.Fatalf("absolute XDG directory = %q, %v", got, err)
	}

	t.Setenv("XDG_CONFIG_HOME", "relative")
	got, err = xdgDirectory("XDG_CONFIG_HOME", ".config")
	if err != nil || got != filepath.Join(home, ".config", "judgement") {
		t.Fatalf("relative XDG fallback = %q, %v", got, err)
	}
}

func TestXDGDirectoryRejectsMissingOrRelativeHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, home := range []string{"", "relative"} {
		t.Run(home, func(t *testing.T) {
			t.Setenv("HOME", home)
			if _, err := xdgDirectory("XDG_CONFIG_HOME", ".config"); err == nil {
				t.Fatal("xdgDirectory succeeded without an absolute home")
			}
		})
	}
}

func TestEnsurePrivateDirectoryCreatesOnlyApplicationDirectoryPrivately(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(parent, 0o755); err != nil { // #nosec G301 -- Verify that private storage leaves a shared parent unchanged.
		t.Fatal(err)
	}
	path := filepath.Join(parent, "judgement")
	if err := ensurePrivateDirectory(path); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o700)
	assertMode(t, parent, 0o755)
}

func TestEnsurePrivateDirectoryRejectsUnsafeExistingPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission and symlink contract")
	}
	root := t.TempDir()
	unsafe := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafe, 0o755); err != nil { // #nosec G301 -- Deliberately unsafe fixture must be rejected.
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(unsafe); err == nil {
		t.Fatal("accepted group-readable application directory")
	}
	regular := filepath.Join(root, "file")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(regular); err == nil {
		t.Fatal("accepted regular file as application directory")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(unsafe, link); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(link); err == nil {
		t.Fatal("accepted symlink as application directory")
	}
}

func TestPrivateFileRoundTripUsesPrivateModesAndAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "judgement", "config.json")
	if err := writePrivateFile(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Dir(path), 0o700)
	assertMode(t, path, 0o600)

	if err := os.Chmod(path, 0o644); err != nil { // #nosec G302 -- Verify replacement restores private permissions on an unsafe fixture.
		t.Fatal(err)
	}
	if err := writePrivateFile(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	assertMode(t, path, 0o600)
	got, err := readPrivateFile(path, 16)
	if err != nil || string(got) != "second" {
		t.Fatalf("read = %q, %v", got, err)
	}
}

func TestReadPrivateFileRejectsUnsafeFilesAndBoundsReads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission and symlink contract")
	}
	dir := filepath.Join(t.TempDir(), "judgement")
	if err := ensurePrivateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "data")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil { // #nosec G306 -- Deliberately unsafe fixture contains only dummy data and must be rejected.
		t.Fatal(err)
	}
	if _, err := readPrivateFile(path, 16); err == nil || containsError(err, "secret") {
		t.Fatalf("insecure read error = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateFile(path, 3); err == nil || containsError(err, "secret") {
		t.Fatalf("oversize read error = %v", err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateFile(link, 16); err == nil {
		t.Fatal("accepted symlink data file")
	}
	directory := filepath.Join(dir, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateFile(directory, 16); err == nil {
		t.Fatal("accepted directory as data file")
	}
}

func TestReadPrivateFilePreservesNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "judgement", "missing")
	_, err := readPrivateFile(path, 16)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}

func TestWritePrivateFileRejectsExistingSymlinkAndNonregularFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "judgement")
	if err := ensurePrivateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(link, []byte("replace")); err == nil {
		t.Fatal("replaced symlink")
	}
	got, err := os.ReadFile(target) // #nosec G304 -- Verify the test-owned symlink target retains its original contents.
	if err != nil || string(got) != "keep" {
		t.Fatalf("symlink target = %q, %v", got, err)
	}
	if err := writePrivateFile(dir, []byte("replace")); err == nil {
		t.Fatal("replaced directory")
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

func containsError(err error, text string) bool {
	return err != nil && strings.Contains(err.Error(), text)
}
