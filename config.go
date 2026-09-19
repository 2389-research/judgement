// ABOUTME: Loads Judgement's TypeSafe API key from the environment or config.
// ABOUTME: Parses bounded private config data without exposing credential text.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const maximumConfigBytes int64 = 16 << 10

type storedConfig struct {
	APIKey string `json:"api_key"`
}

func resolveAPIKey() (string, error) {
	if raw := os.Getenv("TYPESAFE_API_KEY"); raw != "" {
		key, err := normalizeAPIKey(raw)
		if err != nil {
			return "", errors.New("TYPESAFE_API_KEY must contain one nonblank token")
		}
		return key, nil
	}

	directory, err := xdgDirectory("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return "", fmt.Errorf("cannot locate Judgement configuration: %w", err)
	}
	raw, err := readPrivateFile(filepath.Join(directory, "config.json"), maximumConfigBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("no TypeSafe API key found; run judgement setup or set TYPESAFE_API_KEY")
		}
		return "", fmt.Errorf("cannot read Judgement configuration: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config storedConfig
	if err := decoder.Decode(&config); err != nil {
		return "", errors.New("judgement configuration is invalid")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", errors.New("judgement configuration is invalid")
	}
	key, err := normalizeAPIKey(config.APIKey)
	if err != nil {
		return "", errors.New("judgement configuration contains an invalid API key")
	}
	return key, nil
}

func normalizeAPIKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" || strings.ContainsFunc(key, unicode.IsSpace) {
		return "", errors.New("API key must contain one nonblank token")
	}
	return key, nil
}
