# Homebrew cask publishing

Doctor Biz first chose a formula, then reversed it: judgement ships as a macOS
cask, matching the sibling project chronicle. Publish to 2389-research/homebrew-tap
after stable tagged releases.

A small Go renderer reads GoReleaser metadata and archive checksums. It verifies
the two macOS archives (darwin amd64 and arm64) against their release checksums,
then emits a cask with an `arch` stanza, both SHA-256 sums, the release URL, and
a `binary` stanza. The renderer rejects any metadata whose project is not
judgement or whose version or tag is not plain ASCII semver, so a corrupt build
cannot inject text into the cask. CI renders snapshots without secrets; stable
tagged releases use the existing token to commit
Casks/judgement.rb and remove the retired Formula/judgement.rb. Prereleases skip
tap writes.

The cask covers macOS only. GoReleaser still builds the Linux archives and the
GitHub release still publishes them, so Linux users download a tarball directly.
This follows chronicle's working cask and avoids the deprecated GoReleaser
publisher.

Verification: TDD for generator mappings and rejection of broken releases,
canonical checks, real snapshot generation, and Ruby syntax. CI does not install
the cask — an unsigned macOS binary can't clear Gatekeeper in headless CI, which
is why chronicle's CI skips it too. Token write access was already proven by the
v0.0.1 formula release; the v0.0.2 cask release reuses the same token.
