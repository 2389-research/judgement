// ABOUTME: Resolves Judgement's XDG locations and stores private local data.
// ABOUTME: Enforces user-only application directories and atomic data writes.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const applicationDirectoryName = "judgement"

func xdgDirectory(variable, fallback string) (string, error) {
	base := os.Getenv(variable)
	if !filepath.IsAbs(base) {
		home := os.Getenv("HOME")
		if !filepath.IsAbs(home) {
			return "", fmt.Errorf("cannot determine %s directory", applicationDirectoryName)
		}
		base = filepath.Join(home, fallback)
	}
	return filepath.Join(base, applicationDirectoryName), nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		return validatePrivateDirectory(path, info)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot inspect application directory: %w", err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("cannot create application directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("cannot protect application directory: %w", err)
	}
	info, err = os.Lstat(path)
	if err != nil {
		return fmt.Errorf("cannot inspect application directory: %w", err)
	}
	return validatePrivateDirectory(path, info)
}

func validatePrivateDirectory(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("application directory %q must not be a symlink", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("application directory %q is not a directory", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("application directory %q must be accessible only by its owner", path)
	}
	return nil
}

func writePrivateFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("data file %q must be a regular file and not a symlink", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot inspect data file: %w", err)
	}

	temporary, err := os.CreateTemp(directory, ".judgement-write-*")
	if err != nil {
		return fmt.Errorf("cannot create temporary data file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("cannot protect temporary data file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("cannot write temporary data file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("cannot sync temporary data file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("cannot close temporary data file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("cannot replace data file: %w", err)
	}
	removeTemporary = false
	return nil
}

func readPrivateFile(path string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		return nil, errors.New("data file size limit must not be negative")
	}
	directory := filepath.Dir(path)
	directoryInfo, err := os.Lstat(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("cannot inspect application directory: %w", err)
	}
	if err := validatePrivateDirectory(directory, directoryInfo); err != nil {
		return nil, err
	}

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("cannot inspect data file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("data file %q must be a regular file and not a symlink", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("data file %q must be readable only by its owner", path)
	}
	if info.Size() > maxBytes {
		return nil, errors.New("data file exceeds its size limit")
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("cannot open data file: %w", err)
	}
	defer func() { _ = file.Close() }()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect open data file: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm()&0o077 != 0 || !os.SameFile(info, openedInfo) {
		return nil, errors.New("data file changed while it was being opened")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read data file: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, errors.New("data file exceeds its size limit")
	}
	return raw, nil
}
