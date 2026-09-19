// ABOUTME: Parses Judgement CLI input and runs one TypeSafe choice request.
// ABOUTME: Keeps command behavior behind run so tests can use real streams.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	typesafe "github.com/2389-research/typesafe-go"
)

var version = "dev"

const (
	defaultTimeout       = 30 * time.Second
	defaultCacheTTL      = 24 * time.Hour
	maximumInputBytes    = 1 << 20
	choiceID             = "best"
	choiceInstructions   = "Choose the best answer to the question."
	probabilityTolerance = 1e-6
)

type commandOptions struct {
	inputPath    string
	inputJSON    string
	json         bool
	quiet        bool
	help         bool
	version      bool
	model        string
	modelSet     bool
	timeout      time.Duration
	inputSet     bool
	inputJSONSet bool
	cache        bool
	cacheTTL     time.Duration
	cacheTTLSet  bool
}

type requestInput struct {
	Question string   `json:"question"`
	Answers  []string `json:"answers"`
}

type answerOutput struct {
	Index       int     `json:"index"`
	Answer      string  `json:"answer"`
	Probability float64 `json:"probability"`
}

type resultOutput struct {
	Type       string         `json:"type"`
	Question   string         `json:"question"`
	Winner     answerOutput   `json:"winner"`
	Answers    []answerOutput `json:"answers"`
	Confidence float64        `json:"confidence"`
	Model      string         `json:"model"`
	Usage      typesafe.Usage `json:"usage"`
	Cached     bool           `json:"cached"`
}

type cliError struct {
	code    string
	message string
	exit    int
}

type helpItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type helpExitCode struct {
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}

type helpInputFormat struct {
	Example     string            `json:"example"`
	Schema      map[string]string `json:"schema"`
	Constraints []string          `json:"constraints"`
}

type helpOutput struct {
	Type        string          `json:"type"`
	Usage       []string        `json:"usage"`
	Flags       []helpItem      `json:"flags"`
	InputFormat helpInputFormat `json:"input_format"`
	Environment []helpItem      `json:"environment"`
	ExitCodes   []helpExitCode  `json:"exit_codes"`
	Examples    []string        `json:"examples"`
	Commands    []helpItem      `json:"commands,omitempty"`
}

func (e *cliError) Error() string { return e.message }

type choiceOption string

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// A bare invocation is someone exploring the tool, not a botched request; show help
	// rather than the terse "missing input" usage error.
	if len(args) == 0 {
		return writeHelp(stdout, stderr, false)
	}
	if setupArgs, ok := setupCommandArgs(args); ok {
		return runSetup(ctx, setupArgs, stdin, stdout, stderr)
	}
	jsonMode := detectJSONMode(args)
	opts, positionals, err := parseOptions(args)
	if err != nil {
		return writeFailure(stdout, stderr, jsonMode, invalidArguments(err.Error()))
	}
	jsonMode = opts.json

	if opts.help {
		return writeHelp(stdout, stderr, jsonMode)
	}
	if opts.version {
		return writeVersion(stdout, stderr, jsonMode)
	}
	if opts.json && opts.quiet {
		return writeFailure(stdout, stderr, true, invalidArguments("cannot use --quiet with --json"))
	}

	input, err := loadInputContext(ctx, opts, positionals, stdin)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return writeFailure(stdout, stderr, jsonMode, &cliError{"api_error", "operation canceled", 1})
		}
		return writeFailure(stdout, stderr, jsonMode, invalidArguments(err.Error()))
	}
	if err := validateInput(input); err != nil {
		return writeFailure(stdout, stderr, jsonMode, invalidArguments(err.Error()))
	}

	apiKey, err := withContext(ctx, resolveAPIKey)
	if err != nil {
		return writeFailure(stdout, stderr, jsonMode, &cliError{"configuration_error", err.Error(), 1})
	}
	clientOptions := []typesafe.Option{typesafe.WithTimeout(opts.timeout), typesafe.WithAPIKey(apiKey)}
	if baseURL := os.Getenv(typesafe.EnvBaseURL); baseURL != "" {
		clientOptions = append(clientOptions, typesafe.WithBaseURL(baseURL))
	}
	if opts.modelSet {
		clientOptions = append(clientOptions, typesafe.WithModel(opts.model))
	}
	client, err := typesafe.New(clientOptions...)
	if err != nil {
		message := "TypeSafe client configuration is invalid"
		if errors.Is(err, typesafe.ErrNoAPIKey) {
			message = "set TYPESAFE_API_KEY to a valid TypeSafe API key"
		}
		return writeFailure(stdout, stderr, jsonMode, &cliError{"configuration_error", message, 1})
	}
	var identity string
	if opts.cache {
		model := os.Getenv(typesafe.EnvModel)
		if model == "" {
			model = typesafe.DefaultModel
		}
		if opts.modelSet {
			model = opts.model
		}
		baseURL := os.Getenv(typesafe.EnvBaseURL)
		if baseURL == "" {
			baseURL = typesafe.DefaultBaseURL
		}
		identity = cacheIdentity(input, model, baseURL, apiKey)
		type lookup struct {
			result resultOutput
			hit    bool
		}
		cached, err := withContext(ctx, func() (lookup, error) {
			result, hit, err := loadCachedResult(identity, input, effectiveCacheTTL(opts, model), time.Now())
			return lookup{result, hit}, err
		})
		if err != nil {
			return writeFailure(stdout, stderr, jsonMode, &cliError{"cache_error", "cannot read cache: " + err.Error(), 1})
		}
		if cached.hit {
			cached.result.Cached = true
			if err := writeResult(stdout, cached.result, opts); err != nil {
				return reportWriteFailure(stderr, err)
			}
			return 0
		}
	}

	options, optionIDs := makeChoiceOptions(input.Answers)
	question := typesafe.Choice[choiceOption](choiceID, choiceInstructions, options)
	requestCtx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	response, err := client.Ask(requestCtx, input.Question, question)
	if err != nil {
		return writeFailure(stdout, stderr, jsonMode, classifyAPIError(err))
	}
	answer, err := question.From(response)
	if err != nil {
		return writeFailure(stdout, stderr, jsonMode, invalidResponse("TypeSafe returned an invalid choice response"))
	}
	result, err := buildResult(input, optionIDs, answer, response)
	if err != nil {
		return writeFailure(stdout, stderr, jsonMode, invalidResponse(err.Error()))
	}
	if opts.cache {
		_, err := withContext(ctx, func() (struct{}, error) {
			return struct{}{}, saveCachedResult(identity, result, time.Now())
		})
		if err != nil {
			return writeFailure(stdout, stderr, jsonMode, &cliError{"cache_error", "cannot write cache: " + err.Error(), 1})
		}
	}

	if err := writeResult(stdout, result, opts); err != nil {
		return reportWriteFailure(stderr, err)
	}
	return 0
}

// A leading "setup" (optionally after --json/--help) is the setup subcommand;
// everything after it belongs to the setup flag parser, which rejects a key passed
// as an argument. Escape a judgment whose question is literally "setup" with `--`.
func setupCommandArgs(args []string) ([]string, bool) {
	for i, arg := range args {
		if arg == "setup" {
			setupArgs := append([]string(nil), args[:i]...)
			return append(setupArgs, args[i+1:]...), true
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") || (name != "json" && name != "help" && name != "h") {
			return nil, false
		}
	}
	return nil, false
}

func effectiveCacheTTL(opts commandOptions, model string) time.Duration {
	if !opts.cacheTTLSet && strings.HasPrefix(model, "jev-") {
		parts := strings.Split(strings.TrimPrefix(model, "jev-"), ".")
		if len(parts) == 3 {
			for _, part := range parts {
				if _, err := strconv.ParseUint(part, 10, 32); err != nil {
					return opts.cacheTTL
				}
			}
			return 0
		}
	}
	return opts.cacheTTL
}

func parseOptions(args []string) (commandOptions, []string, error) {
	opts := commandOptions{timeout: defaultTimeout}
	var timeoutText string
	flags := flag.NewFlagSet("judgement", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.inputPath, "input", "", "read JSON input from a file or -")
	flags.StringVar(&opts.inputJSON, "input-json", "", "read JSON input from this value")
	flags.BoolVar(&opts.json, "json", false, "emit JSON")
	flags.BoolVar(&opts.quiet, "quiet", false, "emit only the winning answer")
	flags.BoolVar(&opts.help, "help", false, "show help")
	flags.BoolVar(&opts.help, "h", false, "show help")
	flags.BoolVar(&opts.version, "version", false, "show version")
	flags.StringVar(&opts.model, "model", "", "use a TypeSafe model")
	flags.StringVar(&timeoutText, "timeout", defaultTimeout.String(), "set total request timeout")
	flags.BoolVar(&opts.cache, "cache", false, "cache successful judgments")
	flags.DurationVar(&opts.cacheTTL, "cache-ttl", defaultCacheTTL, "set cached result lifetime")

	if err := flags.Parse(args); err != nil {
		return commandOptions{}, nil, fmt.Errorf("invalid arguments: %w", err)
	}
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "input":
			opts.inputSet = true
		case "input-json":
			opts.inputJSONSet = true
		case "model":
			opts.modelSet = true
		case "cache-ttl":
			opts.cacheTTLSet = true
		}
	})
	if opts.cacheTTLSet && !opts.cache {
		return commandOptions{}, nil, errors.New("--cache-ttl requires --cache")
	}
	if opts.cacheTTL < 0 {
		return commandOptions{}, nil, errors.New("cache TTL must not be negative; use 0 for no expiry")
	}

	if opts.modelSet && strings.TrimSpace(opts.model) == "" {
		return commandOptions{}, nil, errors.New("model cannot be blank")
	}
	timeout, err := time.ParseDuration(timeoutText)
	if err != nil {
		return commandOptions{}, nil, fmt.Errorf("timeout must be a duration such as 30s: %w", err)
	}
	if timeout <= 0 {
		return commandOptions{}, nil, errors.New("timeout must be positive")
	}
	opts.timeout = timeout
	return opts, flags.Args(), nil
}

func detectJSONMode(args []string) bool {
	jsonMode := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		switch arg {
		case "--input", "-input", "--input-json", "-input-json", "--model", "-model", "--timeout", "-timeout", "--cache-ttl", "-cache-ttl":
			i++
			continue
		case "--json", "-json", "--json=true", "-json=true":
			jsonMode = true
		case "--json=false", "-json=false":
			jsonMode = false
		default:
			for _, prefix := range []string{"--json=", "-json="} {
				if value, found := strings.CutPrefix(arg, prefix); found {
					parsed, err := strconv.ParseBool(value)
					if err == nil {
						jsonMode = parsed
					}
				}
			}
		}
	}
	return jsonMode
}

func loadInput(opts commandOptions, positionals []string, stdin io.Reader) (requestInput, error) {
	if opts.inputSet && opts.inputJSONSet {
		return requestInput{}, errors.New("only one JSON input may be used: choose --input or --input-json")
	}
	if (opts.inputSet || opts.inputJSONSet) && len(positionals) > 0 {
		return requestInput{}, errors.New("cannot mix JSON input with positional arguments")
	}
	if opts.inputSet {
		if opts.inputPath == "" {
			return requestInput{}, errors.New("--input needs a file path or - for stdin")
		}
		if opts.inputPath == "-" {
			return decodeInput(stdin)
		}
		file, err := os.Open(opts.inputPath)
		if err != nil {
			return requestInput{}, fmt.Errorf("cannot read input file %q: %w", opts.inputPath, err)
		}
		defer func() { _ = file.Close() }()
		return decodeInput(file)
	}
	if opts.inputJSONSet {
		return decodeInput(strings.NewReader(opts.inputJSON))
	}
	if len(positionals) < 3 {
		return requestInput{}, errors.New("provide a question and at least two answers")
	}
	return requestInput{Question: positionals[0], Answers: positionals[1:]}, nil
}

func loadInputContext(ctx context.Context, opts commandOptions, positionals []string, stdin io.Reader) (requestInput, error) {
	return withContext(ctx, func() (requestInput, error) { return loadInput(opts, positionals, stdin) })
}

// Blocking filesystem and input operations must not swallow process cancellation.
func withContext[T any](ctx context.Context, operation func() (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	loaded := make(chan result, 1)
	go func() {
		value, err := operation()
		loaded <- result{value, err}
	}()
	select {
	case result := <-loaded:
		return result.value, result.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}

func decodeInput(reader io.Reader) (requestInput, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maximumInputBytes+1))
	if err != nil {
		return requestInput{}, fmt.Errorf("cannot read JSON input: %w", err)
	}
	if len(raw) > maximumInputBytes {
		return requestInput{}, errors.New("JSON input exceeds the 1 MiB limit")
	}

	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var input requestInput
	if err := decoder.Decode(&input); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			return requestInput{}, fmt.Errorf("JSON input has an %w", err)
		}
		return requestInput{}, fmt.Errorf("input must be valid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return requestInput{}, errors.New("input must contain exactly one JSON object")
	}
	return input, nil
}

func validateInput(input requestInput) error {
	if strings.TrimSpace(input.Question) == "" {
		return errors.New("question cannot be blank")
	}
	if len(input.Answers) < 2 {
		return errors.New("provide at least two answers")
	}
	seen := make(map[string]struct{}, len(input.Answers))
	for i, answer := range input.Answers {
		if strings.TrimSpace(answer) == "" {
			return fmt.Errorf("answer %d cannot be blank", i+1)
		}
		if _, exists := seen[answer]; exists {
			return errors.New("answers must be unique")
		}
		seen[answer] = struct{}{}
	}
	return nil
}

func makeChoiceOptions(answers []string) (typesafe.Opts[choiceOption], []choiceOption) {
	width := len(strconv.Itoa(len(answers)))
	options := make(typesafe.Opts[choiceOption], len(answers))
	ids := make([]choiceOption, len(answers))
	for i, answer := range answers {
		id := choiceOption(fmt.Sprintf("%0*d", width, i+1))
		ids[i] = id
		options[id] = answer
	}
	return options, ids
}

func buildResult(input requestInput, ids []choiceOption, answer typesafe.ChoiceAnswer[choiceOption], response *typesafe.Result) (resultOutput, error) {
	if len(answer.Probabilities) != len(ids) {
		return resultOutput{}, errors.New("TypeSafe response does not cover every answer")
	}
	if !validUnitFloat(answer.Confidence) {
		return resultOutput{}, errors.New("TypeSafe response has invalid confidence")
	}

	answers := make([]answerOutput, len(ids))
	var sum float64
	winnerIndex := -1
	for i, id := range ids {
		probability, ok := answer.Probabilities[id]
		if !ok {
			return resultOutput{}, errors.New("TypeSafe response does not cover every answer")
		}
		if !validUnitFloat(probability) {
			return resultOutput{}, errors.New("TypeSafe response has an invalid probability")
		}
		sum += probability
		answers[i] = answerOutput{Index: i + 1, Answer: input.Answers[i], Probability: probability}
		if id == answer.Value {
			winnerIndex = i
		}
	}
	if math.Abs(sum-1) > probabilityTolerance {
		return resultOutput{}, errors.New("TypeSafe response probabilities do not sum to 1")
	}
	if winnerIndex < 0 {
		return resultOutput{}, errors.New("TypeSafe response winner is not an answer")
	}
	winnerProbability := answers[winnerIndex].Probability
	for _, candidate := range answers {
		if candidate.Probability > winnerProbability {
			return resultOutput{}, errors.New("TypeSafe response winner does not have maximum probability")
		}
	}
	return resultOutput{
		Type:       "result",
		Question:   input.Question,
		Winner:     answers[winnerIndex],
		Answers:    answers,
		Confidence: answer.Confidence,
		Model:      response.Model,
		Usage:      response.Usage,
	}, nil
}

func validUnitFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func classifyAPIError(err error) *cliError {
	var apiError *typesafe.Error
	if errors.As(err, &apiError) {
		return &cliError{"api_error", fmt.Sprintf("TypeSafe API request failed with status %d", apiError.StatusCode), 1}
	}
	if strings.Contains(err.Error(), "decoding response") {
		return invalidResponse("TypeSafe returned an invalid response")
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &cliError{"api_error", "TypeSafe API request was canceled or timed out", 1}
	}
	return &cliError{"api_error", "TypeSafe API request failed", 1}
}

func invalidArguments(message string) *cliError {
	return &cliError{"invalid_arguments", message, 2}
}

func invalidResponse(message string) *cliError {
	return &cliError{"invalid_response", message, 1}
}

func writeResult(writer io.Writer, result resultOutput, opts commandOptions) error {
	if opts.json {
		return json.NewEncoder(writer).Encode(result)
	}
	if opts.quiet {
		_, err := fmt.Fprintln(writer, result.Winner.Answer)
		return err
	}
	if _, err := fmt.Fprintf(writer,
		"Winner: %s\nIndex: %d\nProbability: %.6f\nConfidence: %.6f\nModel: %s\n",
		result.Winner.Answer, result.Winner.Index, result.Winner.Probability, result.Confidence, result.Model); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, "Answers:\n"); err != nil {
		return err
	}
	for _, answer := range result.Answers {
		if _, err := fmt.Fprintf(writer, "  %d. %s: %.6f\n", answer.Index, answer.Answer, answer.Probability); err != nil {
			return err
		}
	}
	if opts.cache {
		status := "miss"
		if result.Cached {
			status = "hit"
		}
		if _, err := fmt.Fprintln(writer, "Cache: "+status); err != nil {
			return err
		}
	}
	return nil
}

func writeFailure(stdout, stderr io.Writer, jsonMode bool, failure *cliError) int {
	var err error
	if jsonMode {
		envelope := struct {
			Type  string `json:"type"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}{Type: "error"}
		envelope.Error.Code = failure.code
		envelope.Error.Message = failure.message
		err = json.NewEncoder(stdout).Encode(envelope)
	} else {
		_, err = fmt.Fprintf(stderr, "judgement: %s\n", failure.message)
	}
	if err != nil {
		return reportWriteFailure(stderr, err)
	}
	return failure.exit
}

func reportWriteFailure(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "judgement: output write failed: %v\n", err)
	return 1
}

func writeHelp(stdout, stderr io.Writer, jsonMode bool) int {
	help := helpOutput{
		Type: "help",
		Usage: []string{
			`judgement [flags] "question" "answer1" "answer2" ...`,
			"judgement [flags] --input FILE",
			"judgement [flags] --input-json JSON",
			"judgement setup [--key-stdin] [--json]",
		},
		Flags: []helpItem{
			{"--input FILE", "read JSON from FILE or - for stdin"},
			{"--input-json JSON", "read JSON from the command line"},
			{"--json", "emit JSON for every outcome"},
			{"--quiet", "emit only the winning answer"},
			{"--model NAME", "override the configured model"},
			{"--timeout DURATION", "set the total request timeout; default 30s"},
			{"--cache", "reuse successful judgments from the local cache"},
			{"--cache-ttl DURATION", "cached result lifetime; 0 means no expiry; default 24h for aliases, no expiry for pinned Jev versions; requires --cache"},
			{"--help, -h", "show help"},
			{"--version", "show version"},
		},
		InputFormat: helpInputFormat{
			Example:     `{"question":"...","answers":["...","..."]}`,
			Schema:      map[string]string{"question": "string", "answers": "array of at least two strings"},
			Constraints: []string{"unknown fields and trailing JSON are rejected", "question and answers must be nonblank", "answers must be unique", "maximum size is 1 MiB"},
		},
		Environment: []helpItem{
			{"TYPESAFE_API_KEY", "API key; overrides the credential saved by setup"},
			{"TYPESAFE_BASE_URL", "optional API base URL"},
			{"TYPESAFE_DEFAULT_MODEL", "optional default model"},
			{"XDG_CONFIG_HOME", "config base directory; defaults to $HOME/.config"},
			{"XDG_CACHE_HOME", "cache base directory; defaults to $HOME/.cache"},
		},
		ExitCodes: []helpExitCode{
			{0, "result, setup, help, or version"},
			{1, "configuration, cache, API, invalid response, or output failure"},
			{2, "invalid arguments or input"},
		},
		Examples: []string{
			`judgement "Best snack?" "chips" "apple"`,
			`judgement --json --input request.json`,
		},
		Commands: []helpItem{{"setup", "save a TypeSafe API key; use setup --help for details"}},
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(help); err != nil {
			return reportWriteFailure(stderr, err)
		}
		return 0
	}
	if err := writeHumanHelp(stdout, help); err != nil {
		return reportWriteFailure(stderr, err)
	}
	return 0
}

func writeHumanHelp(writer io.Writer, help helpOutput) error {
	for i, usage := range help.Usage {
		prefix := "       "
		if i == 0 {
			prefix = "Usage: "
		}
		if _, err := fmt.Fprintln(writer, prefix+usage); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, "\nChoose the best supplied answer with TypeSafe Jev.\n\nFlags:\n"); err != nil {
		return err
	}
	for _, flag := range help.Flags {
		if _, err := fmt.Fprintf(writer, "  %-20s %s\n", flag.Name, flag.Description); err != nil {
			return err
		}
	}
	if len(help.Commands) > 0 {
		if _, err := io.WriteString(writer, "\nCommands:\n"); err != nil {
			return err
		}
		for _, command := range help.Commands {
			if _, err := fmt.Fprintf(writer, "  %-20s %s\n", command.Name, command.Description); err != nil {
				return err
			}
		}
	}
	if _, err := fmt.Fprintf(writer, "\nJSON input: %s\n\nEnvironment:\n", help.InputFormat.Example); err != nil {
		return err
	}
	for _, environment := range help.Environment {
		if _, err := fmt.Fprintf(writer, "  %-24s %s\n", environment.Name, environment.Description); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, "\nExit codes:\n"); err != nil {
		return err
	}
	for _, exit := range help.ExitCodes {
		if _, err := fmt.Fprintf(writer, "  %d  %s\n", exit.Code, exit.Meaning); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(writer, "\nExamples:\n"); err != nil {
		return err
	}
	for _, example := range help.Examples {
		if _, err := fmt.Fprintln(writer, "  "+example); err != nil {
			return err
		}
	}
	return nil
}

func writeVersion(stdout, stderr io.Writer, jsonMode bool) int {
	var err error
	if jsonMode {
		err = json.NewEncoder(stdout).Encode(struct {
			Type    string `json:"type"`
			Version string `json:"version"`
		}{"version", version})
	} else {
		_, err = fmt.Fprintf(stdout, "judgement %s\n", version)
	}
	if err != nil {
		return reportWriteFailure(stderr, err)
	}
	return 0
}
