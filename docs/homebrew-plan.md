# Homebrew formula publishing

Doctor Biz supplied HOMEBREW_TAP_TOKEN and chose a formula rather than a cask.
Publish to 2389-research/homebrew-tap after stable tagged releases. No release tag
or immediate tap write is part of this configuration change.

Use a small Go renderer over GoReleaser metadata and archive checksums. Validate
all four archives before emitting OS/architecture mappings, binary installation,
and a version smoke test. CI renders snapshots without secrets; tagged releases
use the existing token to commit Formula/judgement.rb. Prereleases skip tap writes.
This avoids the deprecated GoReleaser formula publisher and unsigned cask hooks.

Verification: TDD for generator validation and mappings, canonical checks, real
snapshot generation, Ruby syntax, local Homebrew install/test against a snapshot,
workflow validation, and independent review. Token write access can only be
confirmed by a release publication; secret presence alone does not establish it.

Compaction count: 0.

Local Homebrew fetched and validated the snapshot but refused installation because
this machine's Xcode/Command Line Tools are outdated. The temporary tap was removed.
The reproducible smoke test now runs in macOS CI using real snapshot archives.
