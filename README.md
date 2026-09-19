# judgement

Ask TypeSafe Jev to pick the best answer from a list. Read a short result in your
terminal, or request JSON for every outcome.

Requires Go 1.23 or later and a TypeSafe API key. The only dependency is
[typesafe-go](https://github.com/2389-research/typesafe-go), pinned to the commit
used by the sibling checkout during development.

```sh
go build -o judgement .
export TYPESAFE_API_KEY='your-key'
./judgement "What is two plus two?" "four" "nine"
```

To install into your Go bin directory, run `go install .` and ensure that directory
is on your `PATH`. Then use `judgement` without `./`.

## Inputs

Pass a question and at least two distinct, nonblank answers. Put flags before the
question. Use `--` if the question starts with a dash.

```sh
judgement --json "Which is a fruit?" "apple" "granite" "steel" "glass"
judgement --quiet "Which is a fruit?" "apple" "granite"
```

JSON requests have the same shape in a literal argument, file, or stdin:

```json
{"question":"Which is a fruit?","answers":["apple","granite"]}
```

```sh
judgement --json --input-json '{"question":"Which is a fruit?","answers":["apple","granite"]}'
judgement --json --input request.json
cat request.json | judgement --json --input -
```

JSON input and output are independent: leave off `--json` to get human output
from a JSON request. Input modes cannot be mixed. Unknown fields, extra trailing
JSON, and input over 1 MiB are rejected.

## Outputs for agents

`--json` emits exactly one JSON object on stdout, including on errors. Stderr
stays empty unless writing output fails. Always check the process exit status.
`--json --help` describes flags, environment, and input as JSON;
`--json --version` returns the build version.

Result objects have these fields:

| Field | Meaning |
| --- | --- |
| `type` | `result` |
| `question` | The supplied question |
| `winner` | `{index, answer, probability}` for the selected answer |
| `answers` | All `{index, answer, probability}` entries, in input order |
| `confidence` | Model confidence, separate from the winner's probability |
| `model` | Model identifier returned by TypeSafe |
| `usage` | `input_tokens` and `output_tokens` |

Indexes start at **1**. Probabilities are numbers from 0 to 1. Answers remain in
input order, so an agent can identify the winner without comparing strings.

Errors have this shape:

```json
{"type":"error","error":{"code":"invalid_arguments","message":"..."}}
```

| Exit | Meaning | Error codes |
| --- | --- | --- |
| 0 | Result, help, or version | — |
| 1 | Configuration, API, response, or output failure | `configuration_error`, `api_error`, `invalid_response` |
| 2 | Invalid arguments or JSON input | `invalid_arguments` |

Human output includes the winner and distribution. `--quiet` prints only the
winning answer; it cannot be combined with `--json`. Human errors go to stderr.
Messages may change; agents should branch on exit status and `error.code`.
If stdout cannot be written, the CLI exits 1 and reports the write failure on
stderr; it cannot deliver a JSON envelope through a broken output stream.

## Configuration

| Setting | Default |
| --- | --- |
| `TYPESAFE_API_KEY` | Required for judgments; help and version work without it |
| `TYPESAFE_DEFAULT_MODEL` | SDK default, `jev-latest` |
| `TYPESAFE_BASE_URL` | SDK default, `https://api.typesafe.ai` |
| `--model NAME` | Overrides `TYPESAFE_DEFAULT_MODEL` |
| `--timeout DURATION` | `30s`, total request deadline including SDK retries |

Ctrl-C cancels the request. The SDK retries transient failures within the total
deadline. A lost response can lead to a repeated, billed request; the SDK does
not offer a documented idempotency key. Judgments are probabilistic.

## Development

```sh
scripts/check
```

The gate checks formatting, `go vet`, `golangci-lint` when installed,
race-enabled unit/integration/process tests,
and compilation. With `TYPESAFE_API_KEY` set, it also runs one real API end-to-end
request through the built CLI. Without a key, it reports that test as not run.
Transport integration tests use a local HTTP fixture to inspect actual SDK
requests and exercise response handling; they do not establish live model behavior.

To work against local SDK edits without changing the pinned module dependency:

```sh
go work init . ../typesafe-go
```

The workspace files are ignored. Remove `go.work` and `go.work.sum` to return to
the pinned dependency.
