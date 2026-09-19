# CI and release tooling

Doctor Biz requested the golangci-lint, GoReleaser, pre-commit and GitHub Actions
setup from the Chronicle project, adapted to Judgement.

Use Chronicle's approach with a small set of project-specific changes: no CGO,
four macOS/Linux architecture targets, no personal Homebrew tap or Codecov
integration. Keep tests under lint analysis and explain intentional security
test fixtures at their exact source lines. Reuse scripts/check for local checks.
Release versions must reach both human and JSON output via linker injection.

## Verification

- [x] Confirm live Jev request and cache reuse with the supplied local key.
- [x] Prove release-version test fails before replacing the constant.
- [x] Validate lint, hook and workflow configuration using their native tools.
- [x] Build all four snapshot archives and verify their checksums.
- [x] Run complete hook and canonical checks after integration.
- [x] Fresh review, commit, push branch, create PR, and verify GitHub CI.

No version tag or published release is part of this change. The existing public
repository receives the branch and PR; merge follows repository review policy.
Compaction count: 0.

Local verification: all pre-commit hooks pass; uncapped lint reports zero issues;
Go 1.23.12 race tests pass; native workflow and release validators pass. Review
caught an exact Go 1.23.0 compiler selection inherited from Chronicle: release
builds now use stable Go like snapshot CI, while the compatibility job tests 1.23.
The machine's existing global Git hook already invokes prek; no hook path changes
were needed.

GitHub verification: all five jobs passed for implementation commit `33046fe`
in [CI run 35461824858](https://github.com/2389-research/judgement/actions/runs/35461824858).
[PR #1](https://github.com/2389-research/judgement/pull/1) contains the change.
Next step: merge the reviewed PR; publish a version tag when a release is wanted.
