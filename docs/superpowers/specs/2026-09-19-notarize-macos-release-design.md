# Notarize macOS release binaries

Date: 2026-09-19
Status: approved, implementing

## Problem

`brew install --cask 2389-research/tap/judgement` installs a binary that hangs on
every first invocation (setup, bare, `--help`). Root cause, proven by controlled
experiment: GoReleaser ships an adhoc-signed, un-notarized binary. Homebrew Cask
marks it with `com.apple.quarantine`. On first launch Gatekeeper tries to assess a
quarantined binary Apple has never blessed and wedges; the code itself is correct
(a never-quarantined copy runs instantly). This is a distribution bug, not a code
bug.

## Decision

Sign the macOS binaries with the org's Developer ID Application certificate and
notarize them, using GoReleaser's built-in cross-platform `notarize.macos`. No
new tool, no macOS runner: the release job stays on `ubuntu-latest`.

Rejected alternatives:
- **anchore/quill** post-build hook — works, but adds a dependency GoReleaser now
  covers natively for free.
- **Native codesign/notarytool on a macOS runner** — GoReleaser Pro only, and a
  heavier runner for no gain here.
- **Strip `com.apple.quarantine` in a cask postflight** — hides the symptom,
  fixes nothing for direct tarball downloads, and trains users to trust unsigned
  binaries. Notarization is the real fix.

## Verified facts (not assumed)

- GoReleaser's cross-platform `notarize.macos` is free; only the *native* path is
  Pro. Local `goreleaser check` (2.18.1) accepts the schema, exit 0. CI's
  `~> v2` is at least that version.
- Notarize runs after build, before archive, embedding the signature in the
  Mach-O. The shipped `tar.gz` therefore carries the signed binary and checksums
  stay consistent — the `internal/cmd/homebrew-cask` generator is unchanged.
- The Developer ID Application identity lives in `Certificates.p12` (subject
  `Developer ID Application: 2389 Research, Inc (HD9NM9NSMK)`), password in
  `Certificates.p12.password.txt`. Confirmed by reading the certificate subject,
  not the filename (`apple_dist.p12` is an Apple Distribution App Store cert —
  wrong type).
- Notary access uses the App Store Connect Developer-role key `BHN2KMQ235`, with
  fallback to the admin key `BWUFA73L84` if Apple rejects the role.

## Changes

- `.goreleaser.yml`: give the build `id: judgement`; add a `notarize.macos` block
  gated `enabled: '{{ isEnvSet "MACOS_SIGN_P12" }}'` so it no-ops without secrets.
- `.github/workflows/release.yml`: pass five secrets to the Release step.
- GitHub repo secrets (set from files, never echoed):
  `MACOS_SIGN_P12` (base64 Certificates.p12), `MACOS_SIGN_PASSWORD`,
  `MACOS_NOTARY_KEY` (base64 AuthKey_BHN2KMQ235.p8), `MACOS_NOTARY_KEY_ID`,
  `MACOS_NOTARY_ISSUER_ID`.
- `ci.yml` and the cask generator: unchanged. CI's snapshot build has no secrets,
  so notarize stays disabled and CI stays green.

## Verification

No new Go code, so no new unit tests; Gatekeeper behavior is not unit-testable.
Verification is by reproduction with the real credentials, in order:

1. `goreleaser release --snapshot --clean` with no creds — archives still build,
   notarize skipped (proves dev and CI stay green).
2. Same with the real creds exported — inspect the dist darwin binary: Developer
   ID signature present, hardened runtime set, notarization accepted.
3. Canonical gate `scripts/check`, `goreleaser check`, `actionlint`.
4. After CI is green and merged: cut prerelease `v0.0.4-rc.1` (leaves the stable
   cask untouched), install it, confirm it clears Gatekeeper without hanging.
5. Only then, stable `v0.0.4`.

## Caveats

- A bare Mach-O cannot be stapled, so Gatekeeper verifies notarization online on
  first run (one network round trip). Far better than a hang; the alternative
  needs an app bundle we do not ship.
- Notarization requires the hardened runtime. Step 2 confirms GoReleaser's signer
  sets it before we depend on it.
- v0.0.3 cask users are fixed only by v0.0.4. Their stopgap stays
  `brew reinstall --cask --no-quarantine judgement`.
