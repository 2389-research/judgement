// ABOUTME: Renders a Homebrew cask from verified GoReleaser archives.
// ABOUTME: Reads local build output without publishing or making network calls.

package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("homebrew-cask", flag.ContinueOnError)
	var diagnostics strings.Builder
	flags.SetOutput(&diagnostics)
	dist := flags.String("dist", "dist", "directory containing GoReleaser metadata.json, checksums.txt, and archives")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(&diagnostics, "Usage: homebrew-cask [--dist DIRECTORY]\nVerify the two macOS release archives and print a Homebrew cask to stdout.\n--dist defaults to dist; no files are published.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, diagnostics.String())
			return 0
		}
		_, _ = io.WriteString(stderr, diagnostics.String())
		return 1
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "homebrew-cask: unexpected positional arguments; use --help")
		return 1
	}
	cask, err := render(*dist)
	if err == nil {
		_, err = io.WriteString(stdout, cask)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "homebrew-cask: %v\n", err)
		return 1
	}
	return 0
}

func render(dist string) (string, error) {
	// The directory is explicitly selected by the caller; filenames are fixed or validated below.
	data, err := os.ReadFile(filepath.Join(dist, "metadata.json")) // #nosec G304 -- intentional local build input.
	if err != nil {
		return "", fmt.Errorf("read metadata: %w", err)
	}
	var metadata struct {
		Project string `json:"project_name"`
		Tag     string `json:"tag"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", fmt.Errorf("parse metadata: %w", err)
	}
	versionPattern := regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	if metadata.Project != "judgement" || !versionPattern.MatchString(metadata.Version) || !versionPattern.MatchString(strings.TrimPrefix(metadata.Tag, "v")) {
		return "", errors.New("metadata must name judgement with a plain ASCII version and tag")
	}
	checksums, err := readChecksums(dist)
	if err != nil {
		return "", err
	}
	// A cask installs the macOS binary only; Homebrew's arch stanza selects between them.
	shas := make(map[string]string, 2)
	for _, arch := range []struct{ label, archive string }{
		{"arm", "darwin_arm64"}, {"intel", "darwin_amd64"},
	} {
		name := "judgement_" + metadata.Version + "_" + arch.archive + ".tar.gz"
		checksum, exists := checksums[name]
		if !exists {
			return "", fmt.Errorf("missing checksum for %s", name)
		}
		if err := verifyArchive(filepath.Join(dist, name), checksum); err != nil {
			return "", err
		}
		shas[arch.label] = checksum
	}
	var cask strings.Builder
	cask.WriteString("cask \"judgement\" do\n")
	cask.WriteString("  arch arm: \"arm64\", intel: \"amd64\"\n\n")
	fmt.Fprintf(&cask, "  version %q\n", metadata.Version)
	fmt.Fprintf(&cask, "  sha256 arm:   %q,\n         intel: %q\n\n", shas["arm"], shas["intel"])
	cask.WriteString("  url \"https://github.com/2389-research/judgement/releases/download/v#{version}/judgement_#{version}_darwin_#{arch}.tar.gz\"\n")
	cask.WriteString("  name \"Judgement\"\n")
	cask.WriteString("  desc \"Choose the best answer with TypeSafe Jev\"\n")
	cask.WriteString("  homepage \"https://github.com/2389-research/judgement\"\n\n")
	cask.WriteString("  binary \"judgement\", target: \"judgement\"\nend\n")
	return cask.String(), nil
}

func readChecksums(dist string) (map[string]string, error) {
	file, err := os.Open(filepath.Join(dist, "checksums.txt")) // #nosec G304 -- caller-selected local build directory.
	if err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	defer func() { _ = file.Close() }()
	checksums := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, errors.New("invalid checksum line")
		}
		hash, err := hex.DecodeString(fields[0])
		if err != nil || len(hash) != sha256.Size {
			return nil, errors.New("invalid SHA256 checksum")
		}
		if _, exists := checksums[fields[1]]; exists {
			return nil, fmt.Errorf("duplicate checksum for %s", fields[1])
		}
		checksums[fields[1]] = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums: %w", err)
	}
	return checksums, nil
}

func verifyArchive(path, expected string) error {
	file, err := os.Open(path) // #nosec G304 -- basename built only from validated ASCII metadata and fixed platform names.
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hash archive: %w", err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return fmt.Errorf("checksum mismatch for %s", filepath.Base(path))
	}
	return nil
}
