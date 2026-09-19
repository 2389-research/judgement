# Setup and opt-in cache

Doctor Biz requested a setup command that prompts for a TypeSafe API key and
stores it in XDG locations, plus a flag to cache repeated requests. Existing
JSON output, positional arguments, and input formats must keep working.

## Contract and decisions

- `judgement setup` prompts on stderr, hides terminal input, and atomically saves
  `{"api_key":"..."}` to `$XDG_CONFIG_HOME/judgement/config.json`. Fall back to
  `$HOME/.config` for unset, empty, or relative XDG_CONFIG_HOME, including on macOS.
- `judgement setup --key-stdin --json` reads the key from stdin without prompting
  and emits `{"type":"setup","config_path":"..."}`. No secret appears in output.
  `setup --help` and `setup --json --help` work without input or config access.
  Interactive setup requires a terminal; JSON setup requires --key-stdin.
  Flags after setup or leading --json are accepted. Only literal first positional
  `setup` as the sole subcommand invokes setup; `-- "setup" "A" "B"` is a question.
- Environment TYPESAFE_API_KEY, if nonempty, overrides the stored key; otherwise
  load config. Missing credentials point to setup or the environment variable.
  Help/version/invalid requests never require config. Store only the key, not
  environment settings. Setup does not make a billed API request or validate
  the credential remotely. Re-running it replaces the saved credential.
- Config/cache directories are created with mode 0700 and data files with mode
  0600. Do not change permissions on existing XDG parent directories. Reject an
  insecure/non-directory/symlink application directory and symlink/nonregular
  data files. Config reads reject group/other-readable files. Atomic replacement
  protects existing data if writing fails. Bound key/config reads to 16 KiB;
  never include the file contents or key in an error message. Keys are nonblank
  single tokens (leading/trailing whitespace stripped).
- Add x/term v0.30.0, compatible with Go 1.23, to hide terminal input and restore
  terminal state after interruption. This is the bounded exception to the original
  standard-library-only helper preference.
- `--cache` opts into persistent successful-result caching under
  `$XDG_CACHE_HOME/judgement` (fallback `$HOME/.cache`, same XDG absolute-path rule).
  Default TTL is unlimited for pinned `jev-MAJOR.MINOR.PATCH` versions and 24h
  for aliases/other model names; `--cache-ttl DURATION` overrides it and requires
  --cache. Zero means no expiry; negative values are rejected. No cache reads or
  writes without --cache.
- Cache identity hashes canonical request data: schema version, question, ordered
  answers, model selection, normalized API base URL, fixed Choice instructions,
  and a SHA-256 credential fingerprint. Never store keys. SHA-256 file names.
  Text/JSON input and output style must not affect identity. Failures are not cached.
- Read cache after input/config validation and before Ask. Misses, expired entries,
  malformed entries, and entries with invalid/mismatched results call the API.
  Bound cache reads to 16 MiB; cache write failures and non-missing filesystem read
  failures report cache_error/exit 1. JSON/human errors keep the existing contract.
- Successful results always include `cached` boolean; true only on a cache hit.
  JSON cache hits keep the original model, probabilities, confidence, and usage;
  usage describes the original call. Human output adds `Cache: hit/miss` only
  with --cache. Quiet output remains only the answer. No automatic eviction task;
  expired matching entries are replaced, and cache contents may be deleted.
- Signal cancellation covers setup input, loading config, and cache I/O as well
  as request input/network. Terminal echo must be restored before process exit.

## Shared interfaces and ownership

Coordinator owns cli.go, existing cli/process/e2e tests, help integration, docs,
go.mod/go.sum, and build checks. Setup worker owns storage.go, config.go, setup.go
and their tests. Cache worker owns cache.go and cache_test.go.

Setup/storage worker provides:

```go
func xdgDirectory(variable, fallback string) (string, error) // app dir; fallback .config/.cache
func ensurePrivateDirectory(path string) error
func writePrivateFile(path string, data []byte) error // atomic; app dir checks
func readPrivateFile(path string, maxBytes int64) ([]byte, error) // os.ErrNotExist preserved
func resolveAPIKey() (string, error) // env override, then config; sanitized errors
func runSetup(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int
```

Cache worker provides:

```go
func cacheIdentity(input requestInput, model, baseURL, apiKey string) string
func loadCachedResult(identity string, input requestInput, ttl time.Duration, now time.Time) (resultOutput, bool, error)
func saveCachedResult(identity string, result resultOutput, now time.Time) error
```

`resultOutput` gains `Cached bool` with JSON name `cached` (coordinator).
Use existing `makeChoiceOptions`, `buildResult`, `validUnitFloat` for validation.
Cache file payload uses schema version, created_at, identity, and result fields.

## Verification steps

- [x] Write failing setup/storage tests, implement, verify focused green.
- [x] Write failing cache tests, implement, verify focused green.
- [x] Integrate with failing CLI tests for setup routing, saved-key/environment
  precedence, cached request count, output modes, and timeout/invalid flags.
- [x] Isolate all tests from actual HOME/XDG config/cache; never read real keys.
- [x] Real binary setup tests verify files, modes, no echo/secret disclosure, and
  cancellation. Transport integration tests prove one call on cache hit and
  fresh calls for changed question/order/model/endpoint/credential or expiry.
- [x] Run scripts/check (format/vet/lint/race/build, live test when key available).
- [x] Independent fresh review, fix findings, rerun checks, commit on
  wip/setup-cache. Record live-test limitations honestly.

Compaction count: 1. No user credential will be requested in chat or read for tests.

## Cache research (2026-09-19)

Doctor Biz asked whether results actually age. The initial blanket 24h proposal
was a precaution, not a measured property of Jev. Official models docs describe
shared weights, no training on customer requests, moving aliases, and explicit
version pinning: https://docs.typesafe.ai/models . The site FAQ emphasizes
consistency, without promising exact determinism: https://typesafe.ai/ . The
self-consistency cookbook changes a uid on each call, explicitly preventing an
identical-input determinism conclusion, and caches recorded results for reuse:
https://docs.typesafe.ai/cookbooks/consistency_noul_cookbook .

Inference: for unchanged input and a fixed model version, a stored judgment has
no natural daily expiry. Use no default expiry for pinned versions, and 24h only
as our update policy for aliases/unknown model names. Explicit --cache-ttl wins.
Time-sensitive facts must be represented in changed input; a fresh call with old
facts does not supply updated facts. TypeSafe's policy is not a service guarantee
that versioned outputs will always be byte-identical.

## Verification record

Local canonical checks pass with zero lint findings and race-enabled unit,
transport integration, process tests, and build checks. The full local test suite
also passes with a disposable inherited API key. Independent review approved the
changes after fixes for test credential disclosure, encoded config size bounds,
and the question `setup` with negative-number answers.

Manual real-PTY checks verified hidden entry, successful saving with modes
0700/0600, Ctrl-C cancellation, external SIGTERM cancellation, and normal terminal
restoration. All keys and config roots were disposable; no real key was used.
The native PTY checks are manual, while stdin setup and storage have automated
process coverage. This is a low-severity test automation gap.

Live API coverage (medium remaining verification gap) is implemented but not run
because TYPESAFE_API_KEY is unset. It now checks a real judgment followed by a
cache hit with a 1ns network timeout, asserting all result metadata stays equal.
Run `scripts/check` with a real key before publishing; the tests do not read the
user's saved setup credential. No local check failures remain.
