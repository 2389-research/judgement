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

The full gate passed against the live API on 2026-09-19 using the key Doctor Biz
supplied in .env. The arithmetic response passed validation, and its cached result
was reused with a 1ns network timeout. This verifies one live scenario, not general
model accuracy. Never print or commit .env; it is ignored along with .env.*.

Setup uses XDG paths on macOS too, rather than Go's platform-specific config/cache
defaults. Relative XDG bases are ignored. TYPESAFE_API_KEY overrides saved config.
Tests isolate config/cache roots and use only disposable keys.

A leading `setup` is always the setup subcommand, which takes no key argument, so
`judgement setup <key>` returns a "no key argument" error (enter the key at the
prompt or pipe it with --key-stdin) rather than falling through to a judgment or a
billed API call. Escape a judgment whose question is literally "setup" with
`judgement -- setup ...`. Running judgement with no arguments prints help (exit 0),
the same as --help, not the terse missing-input error.

Doctor Biz challenged the initial blanket cache TTL. Research the model before
assuming results age: unchanged input plus pinned Jev version defaults to no
expiry; moving aliases use 24h as an update policy. --cache-ttl 0 explicitly keeps
entries forever. Official model and consistency docs are linked in
docs/setup-cache-plan.md. Do not claim Jev is exactly deterministic.

On macOS, a terminal restore may set the transient PENDIN bit (0x20000000).
A plain `stty -icanon -echo` followed by restoring the captured state reproduces
the same difference. Verify echo/canonical/signal flags and restored behavior,
rather than treating PENDIN alone as evidence that setup left the terminal raw.

The 2026-09-19 documentation audit corrected cache size-error behavior, conditional
human cache status, and the literal dev version. Keep these claims tied to source;
the report is in docs/audits/AUDIT_REPORT_2026-09-19.md. Live verification ran after
the initial public push; README and the audit addendum record the passing result.

Release builds use stable Go; the CI compatibility job separately tests Go 1.23.
Using go-version-file with this module would select the original Go 1.23.0 release.
The global hook on this machine already calls prek, so keep core.hooksPath intact.
Commit hooks clear TYPESAFE_API_KEY to avoid billed requests; explicit scripts/check
with the key exported includes the live test.

Homebrew publishing targets 2389-research/homebrew-tap, using HOMEBREW_TAP_TOKEN
only in the tagged release workflow. judgement ships as a macOS cask copied from
the sibling project chronicle, reversing the earlier formula choice (Doctor Biz
called the formula choice incorrect). Stable tags publish Casks/judgement.rb and
delete the retired Formula/judgement.rb; prerelease tags skip the tap write. The
v0.0.1 stable release (2026-09-19) proved the stored HOMEBREW_TAP_TOKEN can push
to the tap; that release shipped the now-retired formula. The Go generator
(internal/cmd/homebrew-cask) reads GoReleaser metadata and checksums, verifies the
two macOS archives, and rejects any non-judgement or non-semver metadata. It only
validates the tag against the semver pattern, never tag == "v"+version: goreleaser
snapshots carry version 0.0.1-SNAPSHOT-<commit> against tag v0.0.1, and CI's
release-check runs the generator on that snapshot. Do not add goreleaser's brews
or homebrew_casks configuration; the custom generator is intentional. CI checks
generation and Ruby syntax only — an unsigned cask can't be install-tested in
headless CI, so there is no macOS install smoke test (chronicle's CI skips it too).

The cask ships a prebuilt binary that Homebrew marks with com.apple.quarantine;
macOS Gatekeeper hangs on the first launch of a quarantined, un-notarized binary,
so a brew-installed judgement appeared to hang on every command (setup, bare,
--help). Fixed by a notarize.macos block in .goreleaser.yml (ships in v0.0.4),
gated on isEnvSet MACOS_SIGN_P12 so local and CI snapshot builds stay unsigned and
green. It signs the darwin builds with the 2389 Developer ID Application cert (team
HD9NM9NSMK) and notarizes with the App Store Connect Developer key BHN2KMQ235;
GoReleaser's cross-platform notarize.macos runs on ubuntu-latest, ordered build →
notarize → archive → checksum so archives hold the signed binary. Five repo secrets
back it (MACOS_SIGN_P12, MACOS_SIGN_PASSWORD, MACOS_NOTARY_KEY, MACOS_NOTARY_KEY_ID,
MACOS_NOTARY_ISSUER_ID), sourced from the vault at
/Users/harper/workspace/icloud-2389/Apple/2389; never print or commit the .p12/.p8
or the p12 password. A bare CLI can't be stapled, so the first run does an online
Gatekeeper check; spctl -a -t exec always says "does not seem to be an app" for a
bare CLI and is not a notarization failure — Apple accepting the notarization is the
proof. Sibling toki dodges the same hang without notarization by stripping
com.apple.quarantine in a cask post-install hook; judgement chose notarization and
keeps its custom cask generator.
