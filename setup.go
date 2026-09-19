// ABOUTME: Runs Judgement's local API credential setup command.
// ABOUTME: Supports hidden terminal input and bounded noninteractive input.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/term"
)

func runSetup(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	jsonMode := detectJSONMode(args)
	flags := flag.NewFlagSet("judgement setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var keyStdin, help bool
	flags.BoolVar(&keyStdin, "key-stdin", false, "read the API key from stdin")
	flags.BoolVar(&jsonMode, "json", jsonMode, "emit JSON")
	flags.BoolVar(&help, "help", false, "show help")
	flags.BoolVar(&help, "h", false, "show help")
	if err := flags.Parse(args); err != nil {
		return writeFailure(stdout, stderr, jsonMode, invalidArguments("invalid setup arguments: "+err.Error()))
	}
	if flags.NArg() != 0 {
		return writeFailure(stdout, stderr, jsonMode, invalidArguments("unexpected setup argument"))
	}
	if help {
		return writeSetupHelp(stdout, stderr, jsonMode)
	}
	if jsonMode && !keyStdin {
		return writeFailure(stdout, stderr, true, invalidArguments("JSON setup requires --key-stdin"))
	}

	var raw []byte
	var err error
	if keyStdin {
		raw, err = readSetupInput(ctx, stdin)
	} else {
		raw, err = readHiddenSetupInput(ctx, stdin, stderr)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeFailure(stdout, stderr, jsonMode, &cliError{"configuration_error", "setup canceled", 1})
		}
		if errors.Is(err, errInteractiveTerminalRequired) {
			return writeFailure(stdout, stderr, jsonMode, invalidArguments(err.Error()))
		}
		if errors.Is(err, errSetupInputTooLarge) || errors.Is(err, errInvalidSetupKey) {
			return writeFailure(stdout, stderr, jsonMode, invalidArguments(err.Error()))
		}
		return writeFailure(stdout, stderr, jsonMode, &cliError{"configuration_error", "cannot read API key", 1})
	}
	key, err := normalizeAPIKey(string(raw))
	if err != nil {
		return writeFailure(stdout, stderr, jsonMode, invalidArguments(errInvalidSetupKey.Error()))
	}

	directory, err := xdgDirectory("XDG_CONFIG_HOME", ".config")
	if err != nil {
		return writeFailure(stdout, stderr, jsonMode, &cliError{"configuration_error", "cannot locate Judgement configuration", 1})
	}
	configPath := filepath.Join(directory, "config.json")
	encoded, err := json.Marshal(storedConfig{APIKey: key}) // #nosec G117 -- Setup deliberately persists the key through writePrivateFile with mode 0600.
	if err != nil {
		return writeFailure(stdout, stderr, jsonMode, &cliError{"configuration_error", "cannot encode Judgement configuration", 1})
	}
	encoded = append(encoded, '\n')
	if int64(len(encoded)) > maximumConfigBytes {
		return writeFailure(stdout, stderr, jsonMode, invalidArguments("API key is too long to store"))
	}
	if err := ctx.Err(); err != nil {
		return writeFailure(stdout, stderr, jsonMode, &cliError{"configuration_error", "setup canceled", 1})
	}
	if err := writePrivateFile(configPath, encoded); err != nil {
		return writeFailure(stdout, stderr, jsonMode, &cliError{"configuration_error", "cannot save Judgement configuration: " + err.Error(), 1})
	}

	if jsonMode {
		result := struct {
			Type       string `json:"type"`
			ConfigPath string `json:"config_path"`
		}{Type: "setup", ConfigPath: configPath}
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return reportWriteFailure(stderr, err)
		}
		return 0
	}
	if _, err := fmt.Fprintf(stdout, "Saved TypeSafe API key to %s\n", configPath); err != nil {
		return reportWriteFailure(stderr, err)
	}
	return 0
}

var (
	errInteractiveTerminalRequired = errors.New("interactive setup requires a terminal; use --key-stdin")
	errSetupInputTooLarge          = errors.New("API key input exceeds the 16 KiB limit")
	errInvalidSetupKey             = errors.New("API key must contain one nonblank token")
)

func readSetupInput(ctx context.Context, reader io.Reader) ([]byte, error) {
	type readResult struct {
		raw []byte
		err error
	}
	result := make(chan readResult, 1)
	go func() {
		raw, err := io.ReadAll(io.LimitReader(reader, maximumConfigBytes+1))
		result <- readResult{raw: raw, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case read := <-result:
		if read.err != nil {
			return nil, read.err
		}
		if int64(len(read.raw)) > maximumConfigBytes {
			return nil, errSetupInputTooLarge
		}
		return read.raw, nil
	}
}

func readHiddenSetupInput(ctx context.Context, reader io.Reader, stderr io.Writer) ([]byte, error) {
	file, ok := reader.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return nil, errInteractiveTerminalRequired
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd := int(file.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	terminalIO := &contextReadWriter{ctx: ctx, reader: file, writer: stderr, maxBytes: maximumConfigBytes}
	terminal := term.NewTerminal(terminalIO, "")
	key, readErr := terminal.ReadPassword("TypeSafe API key: ")
	restoreErr := term.Restore(fd, state)
	_, newlineErr := io.WriteString(stderr, "\n")
	if readErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, readErr
	}
	if restoreErr != nil {
		return nil, restoreErr
	}
	if newlineErr != nil {
		return nil, newlineErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(key)) > maximumConfigBytes {
		return nil, errSetupInputTooLarge
	}
	return []byte(key), nil
}

type contextReadWriter struct {
	ctx      context.Context
	reader   io.Reader
	writer   io.Writer
	maxBytes int64
	read     int64
}

func (rw *contextReadWriter) Read(buffer []byte) (int, error) {
	type result struct {
		count int
		err   error
	}
	remaining := rw.maxBytes - rw.read
	if remaining < int64(len(buffer)) {
		buffer = buffer[:remaining+1]
	}
	read := make(chan result, 1)
	go func() {
		count, err := rw.reader.Read(buffer)
		read <- result{count: count, err: err}
	}()
	select {
	case <-rw.ctx.Done():
		return 0, rw.ctx.Err()
	case result := <-read:
		rw.read += int64(result.count)
		if rw.read > rw.maxBytes {
			return 0, errSetupInputTooLarge
		}
		return result.count, result.err
	}
}

func (rw *contextReadWriter) Write(buffer []byte) (int, error) {
	return rw.writer.Write(buffer)
}

func writeSetupHelp(stdout, stderr io.Writer, jsonMode bool) int {
	help := struct {
		Type  string     `json:"type"`
		Usage []string   `json:"usage"`
		Flags []helpItem `json:"flags"`
	}{
		Type:  "help",
		Usage: []string{"judgement setup", "judgement setup --key-stdin [--json]"},
		Flags: []helpItem{{"--key-stdin", "read the API key from stdin"}, {"--json", "emit JSON"}, {"--help, -h", "show help"}},
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(help); err != nil {
			return reportWriteFailure(stderr, err)
		}
		return 0
	}
	for _, usage := range help.Usage {
		if _, err := fmt.Fprintln(stdout, "Usage: "+usage); err != nil {
			return reportWriteFailure(stderr, err)
		}
	}
	if _, err := io.WriteString(stdout, "\nPrompt for and save a TypeSafe API key.\n\nFlags:\n"); err != nil {
		return reportWriteFailure(stderr, err)
	}
	for _, option := range help.Flags {
		if _, err := fmt.Fprintf(stdout, "  %-18s %s\n", option.Name, option.Description); err != nil {
			return reportWriteFailure(stderr, err)
		}
	}
	return 0
}
