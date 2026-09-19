# Judgement notes

Doctor Biz wants JSON for every CLI outcome, including help and errors, and JSON
request input as well as positional arguments. JSON output goes to stdout,
including error envelopes; exit status still distinguishes failure.

The sibling SDK is `../typesafe-go`, not `../typesafe`. The dependency is pinned
to its current public commit, so building does not require a sibling checkout.
Go maps sort option keys on the wire: use padded positional IDs and keep output
in input order. Do not use answer text as a map key.

This machine may need `env -u GOROOT mise exec --` before Go commands. No API key
was present at session start; never claim the live API test ran without one.

Signal cancellation must wrap all input loading, including opening or reading a
named pipe supplied to --input. Canceling only the HTTP context leaves blocked
reads running while the process has intercepted SIGINT/SIGTERM. Process tests
cover stdin and named pipes. The CLI exits after cancellation; its internal
input-reading goroutine is not a reusable library API.

Local checks passed on 2026-09-19, but the live test needs an API key before
shipping; in particular the 1e-6 probability-sum tolerance has only fixture
coverage. Do not mistake the local HTTP transport fixtures for live API evidence.

Setup uses XDG paths on macOS too, rather than Go's platform-specific config/cache
defaults. Relative XDG bases are ignored. TYPESAFE_API_KEY overrides saved config.
Tests isolate config/cache roots and use only disposable keys.

Doctor Biz challenged the initial blanket cache TTL. Research the model before
assuming results age: unchanged input plus pinned Jev version defaults to no
expiry; moving aliases use 24h as an update policy. --cache-ttl 0 explicitly keeps
entries forever. Official model and consistency docs are linked in
docs/setup-cache-plan.md. Do not claim Jev is exactly deterministic.

On macOS, a terminal restore may set the transient PENDIN bit (0x20000000).
A plain `stty -icanon -echo` followed by restoring the captured state reproduces
the same difference. Verify echo/canonical/signal flags and restored behavior,
rather than treating PENDIN alone as evidence that setup left the terminal raw.
