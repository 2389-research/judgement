# Judgement CLI implementation plan

Goal: choose the best supplied answer with TypeSafe Jev, from shell arguments or
JSON, with usable human output and a complete JSON interface for agents.

Architecture: a root Go main package, standard library flag parsing and JSON,
and the pinned github.com/2389-research/typesafe-go SDK. No framework or service.

## Contract

- `judgement [flags] "question" "answer1" "answer2" ...`, at least two answers.
- Flags precede positional arguments; `--` permits question text starting `-`.
- `--input FILE` reads one JSON object; `--input -` reads stdin;
  `--input-json JSON` takes a literal JSON blob. These modes are exclusive. Object fields:
  `question` string and `answers` string array. Reject unknown fields, trailing
  JSON, blank question/answers, duplicates, and mixing JSON with positionals.
  Bound input reads to 1 MiB. Provide an actionable validation error.
- `--json` emits one JSON object to stdout for success, validation/config/API
  failure, help, and version. No prose on stdout/stderr in JSON mode except an
  unavoidable output write failure. Recognize JSON mode even after a bad flag,
  but respect `--` and flag values/positional boundaries. Reject `--quiet` with
  `--json`. `--quiet` otherwise emits only the winning answer.
- `--help`/`-h`, `--version`, `--model NAME`, `--timeout DURATION` (30s total).
- Read TYPESAFE_API_KEY, TYPESAFE_BASE_URL and TYPESAFE_DEFAULT_MODEL via SDK.
- Result envelope: `type:"result"`, `question`, `winner` (1-based `index`,
  `answer`, `probability`), `answers` (same fields, input order), `confidence`,
  `model`, `usage` (SDK token counts).
- Error envelope: `type:"error"`, `error:{code,message}`; exit 2 for bad input,
  exit 1 for config/API/output failures; 0 for results/help/version. Codes are
  stable (`invalid_arguments`, `configuration_error`, `api_error`,
  `invalid_response`). Output write failures use stderr and exit 1 because stdout
  is unavailable. Human failures go to stderr.
- Help envelope: `type:"help"` plus structured usage, flags, input format,
  environment, exit codes, examples. Version: `type:"version",version:"dev"`.
- Ask one Choice using the question as state, a fixed instruction to choose
  the best answer to that question, and padded positional IDs mapped to answer
  descriptions. Validate returned coverage and finite probabilities/confidence
  in [0,1]; check sum approximately 1 and winner is a maximum (ties allowed).
- Interrupt/termination cancel the request. Never print credentials or raw API
  error bodies. No API key flag, persistence, batch processing, or explanations
  invented for model decisions.

## Work and verification

- [x] CLI worker: write failing tests, confirm red, implement argument/JSON input,
  SDK call, output, and failures; confirm green. Own main.go, cli.go, cli_test.go.
- [x] Coordinator: README, scripts/check, process-level local tests and live
  binary test (real API only; skip explicitly when no key); build/install usage.
- [x] Run canonical format/vet/race test/build checks and real local invocations.
- [x] Fresh review; fix findings with regression tests; rerun required checks.
- [ ] Commit on wip/judgement-cli and record verification and remaining limits.

Success means positional and JSON inputs share validation and request behavior,
JSON covers every outcome, checks pass, and the live-test status is explicit.
Compaction count: 0.

## Verification record

2026-09-19: `scripts/check` passed formatting, vet, golangci-lint (0 issues),
race-enabled unit/HTTP integration/process tests, and compilation. The live API
test was explicitly skipped because TYPESAFE_API_KEY was unset. Before shipping,
run the same gate with a key to verify the real model response.

TDD evidence: focused CLI tests failed against the initial exit-99 stub before
implementation. Process tests caught ignored termination during both stdin and
named-pipe input reads. Context cancellation must cover input loading as well
as HTTP requests; both regressions now pass. Human output includes all answer
probabilities; human and JSON help share one definition.

Known limitation (medium): live API compatibility and the probability-sum
tolerance remain unverified against a live response. No source or test failures
remain in the local gate. No remote or publication was configured.
