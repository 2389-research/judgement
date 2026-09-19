# judgement

Ask TypeSafe Jev to pick the best answer from a list. Read a short result in your
terminal, or request JSON for every outcome.

Requires Go 1.23 or later and a TypeSafe API key. API requests use
[typesafe-go](https://github.com/2389-research/typesafe-go), pinned to the commit
used by the sibling checkout during development. Go's `x/term` package handles
hidden terminal input during setup.

```sh
git clone https://github.com/2389-research/judgement.git
cd judgement
go build -o judgement .
./judgement setup
./judgement "What is two plus two?" "four" "nine"
```

To install without a checkout, run
`go install github.com/2389-research/judgement@latest`. From a checkout, use
`go install .`. Ensure your Go bin directory is on `PATH`, then use `judgement`
without `./`.

## Setup

`judgement setup` prompts for a TypeSafe API key without echoing it. It saves the
key to `$XDG_CONFIG_HOME/judgement/config.json`, falling back to
`$HOME/.config/judgement/config.json`, including on macOS. Relative XDG paths are
ignored, following the [XDG specification](https://specifications.freedesktop.org/basedir/latest/).

The file contains a plaintext key with user-only permissions (`0600`), inside a
user-only application directory (`0700`). Writes replace the file atomically;
running setup again replaces the saved key. Setup does not contact TypeSafe or
verify that the key works.

`TYPESAFE_API_KEY` takes precedence over the saved key. Set it to use a temporary
credential or configure CI without writing a config file. For agents that need
to save a key, pipe it from a secret manager:

```sh
your-secret-command | judgement setup --key-stdin --json
judgement setup --json --help
```

`your-secret-command` represents your own command that emits a key. The CLI has
no key argument, so it does not place the credential in shell history or process
arguments. JSON setup requires `--key-stdin` and emits only a confirmation with
`type: "setup"` and `config_path`; it never prints the key. Canceling the prompt
leaves an existing credential untouched.

Keys must be a single nonblank token; surrounding whitespace is stripped.
Input and the encoded config file are each limited to 16 KiB. Config/cache
application directories and data files must not be symlinks. Existing directories
and files with group/other permissions are rejected on reads; the CLI does not
repair their permissions automatically.

## Opt-in cache

```sh
judgement --cache --json "Which is a fruit?" "apple" "granite"
judgement --cache --cache-ttl 15m "Which is a fruit?" "apple" "granite"
```

`--cache` reuses successful results indefinitely when you select a pinned Jev
version such as `--model jev-1.13.0`. Moving aliases (`jev-latest`, `jev-preview`)
and other model names default to **24 hours**. `--cache-ttl` overrides this policy:
use a positive Go duration for expiry or `0` for no expiry. It requires `--cache`.
Without `--cache`, each judgment calls the API and leaves the cache alone.

Entries live under `$XDG_CACHE_HOME/judgement`, falling back to
`$HOME/.cache/judgement`. They store the question, answers, and result with the
same user-only permissions as config; they never store the API key. The cache
identity includes the ordered answers, question, model selection, API endpoint,
and credential fingerprint. Equivalent JSON and positional inputs share entries;
changing any request identity field causes a miss.

JSON results include `cached: true` on a hit and `cached: false` on a fresh call.
With `--cache`, human output shows `Cache: hit` or `Cache: miss`; quiet output
remains just the answer. Cached usage counts describe the original request,
not a new billed call.
The alias TTL limits how long a name such as `jev-latest` can retain an older
model's answer. It is an update policy, not an inherent lifetime of a judgment.
TypeSafe documents that aliases move when releases ship and recommends pinning
versions when stable behavior matters. For unchanged input and a fixed version,
we expect long-lived reuse to be appropriate; this does not promise identical
results on fresh calls. See [TypeSafe model versions](https://docs.typesafe.ai/models).

Failed requests are not cached. Expired entries or entries with invalid JSON or
result data are replaced after a successful request. Files larger than 16 MiB
and filesystem permission/write errors produce `cache_error`
instead of silently disabling caching. There is no background cleanup: deleting
this application's cache directory clears all entries.

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
`--json --version` returns the version envelope, such as
`{"type":"version","version":"dev"}` for a source build. Release archives report
their tagged version.

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
| `cached` | Whether this result came from the local cache |

Indexes start at **1**. Probabilities are numbers from 0 to 1. Answers remain in
input order, so an agent can identify the winner without comparing strings.

Errors have this shape:

```json
{"type":"error","error":{"code":"invalid_arguments","message":"..."}}
```

| Exit | Meaning | Error codes |
| --- | --- | --- |
| 0 | Result, setup, help, or version | — |
| 1 | Configuration, cache, API, response, or output failure | `configuration_error`, `cache_error`, `api_error`, `invalid_response` |
| 2 | Invalid arguments or JSON input | `invalid_arguments` |

Human output includes the winner and distribution. `--quiet` prints only the
winning answer; it cannot be combined with `--json`. Human errors go to stderr.
Messages may change; agents should branch on exit status and `error.code`.
If stdout cannot be written, the CLI exits 1 and reports the write failure on
stderr; it cannot deliver a JSON envelope through a broken output stream.

## Configuration

| Setting | Default |
| --- | --- |
| `TYPESAFE_API_KEY` | Overrides the key saved by setup; one source is required for judgments |
| `TYPESAFE_DEFAULT_MODEL` | SDK default, `jev-latest` |
| `TYPESAFE_BASE_URL` | SDK default, `https://api.typesafe.ai` |
| `--model NAME` | Overrides `TYPESAFE_DEFAULT_MODEL` |
| `--timeout DURATION` | `30s`, total request deadline including SDK retries |
| `--cache` | Off |
| `--cache-ttl DURATION` | No expiry for pinned Jev versions; `24h` otherwise; `0` means no expiry; requires `--cache` |
| `XDG_CONFIG_HOME` | `$HOME/.config` |
| `XDG_CACHE_HOME` | `$HOME/.cache` |

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

On 2026-09-19, the full gate passed with a real TypeSafe API key, including the
live arithmetic judgment and reuse of its cached result. This verifies that
scenario against the live API; it does not establish accuracy on other questions.

To work against local SDK edits without changing the pinned module dependency:

```sh
go work init . ../typesafe-go
```

The workspace files are ignored. Remove `go.work` and `go.work.sum` to return to
the pinned dependency.

### Commit checks and CI

Install [golangci-lint](https://golangci-lint.run/docs/welcome/install/)
v2.13.2, [GoReleaser](https://goreleaser.com/install/) v2,
[actionlint](https://github.com/rhysd/actionlint) v1.7.12, and
[prek](https://prek.j178.dev/). Then enable the hooks:

```sh
prek install
prek run --all-files
```

The `.pre-commit-config.yaml` also works with `pre-commit`. Hooks check whitespace,
YAML, file size, merge conflicts, private keys, Go formatting, module tidiness,
the canonical local gate, and workflow/release configuration. Hooks run with
`TYPESAFE_API_KEY` empty so committing never triggers billed API tests. Run
`scripts/check` with the key exported when you want live verification. The CLI
and check script do not load `.env` automatically; that file is ignored by Git.

GitHub CI runs on pushes to `main` and pull requests targeting `main`. It tests
Go 1.23 and current stable Go on Linux, plus stable Go on macOS, runs the configured
linters, validates workflows, and builds release snapshots without publishing.
Live API tests do not run in CI. Development tools may require newer Go than the
CLI's minimum supported version.

### Releases

GoReleaser builds macOS and Linux archives for amd64 and arm64 without CGO,
includes the README, and generates SHA-256 checksums. Validate locally with:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

Snapshot artifacts go into the ignored `dist/` directory. Pushing a version tag
such as `v1.2.3` triggers the release workflow, runs tests, and publishes archives
and checksums to this repository's GitHub Releases using its `GITHUB_TOKEN`.
No Homebrew tap is configured.
