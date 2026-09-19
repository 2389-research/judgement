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
