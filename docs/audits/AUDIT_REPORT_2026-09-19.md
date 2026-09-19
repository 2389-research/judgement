# Documentation audit

Date: 2026-09-19. Baseline commit: `e217e48`.

## Scope and method

Audited the sole user-facing document, README.md, against the CLI, storage,
tests, canonical check script, and pinned SDK. Historical plans and project
journals were excluded from claim counts. An independent reviewer extracted
claims, checked source evidence, then expanded mismatch patterns and compared
the code inventory with the documented interface.

## Findings fixed

Line numbers below refer to the baseline README.

| Line | Finding | Evidence and correction |
| --- | --- | --- |
| 78–80 | All malformed cache entries implied replaceable | `cache.go:67–72`, `storage.go:136–137`: files over 16 MiB fail before JSON parsing. Documented `cache_error`. |
| 69 | Human cache status appeared unconditional | `cli.go:562–570`: status requires `--cache`. Qualified the claim. |
| 114 | Version described as a build identifier | `cli.go:23`: constant `dev`. Documented the literal current output. |

## Pattern expansion and gaps

Checked cache error/lifetime claims, output conditions, version/default claims,
all command examples, environment settings, and referenced local scripts.
No additional false claims were found. Added clone and installation instructions,
setup token/size constraints, storage permission/symlink requirements, and the
current live-test limitation.

Interface inventory matches the README: 11 distinct long flag names, five app
configuration environment variables, eight JSON result fields, five error codes,
and one maintenance script. The short `-h` alias also appears in runtime help.
The pinned SDK confirms the documented endpoint, model default, usage fields,
and retry behavior. The linked TypeSafe model and XDG documentation support the
alias and path claims.

## Verification and remaining limits

`scripts/check` passed formatting, vet, lint (zero findings), race-enabled unit,
HTTP transport integration and process tests, and compilation. Live API tests
were explicitly skipped because `TYPESAFE_API_KEY` is unset. This remains a
medium verification gap: live response compatibility has not been established.
The README now states this plainly. Native hidden-input/cancellation behavior
was manually tested during implementation; those PTY checks are not automated.

Fresh-eyes review of the documentation changes found no unresolved misleading
claims. No production code changed in this audit. There is no license file;
public visibility alone does not supply a software license, and this audit did
not choose licensing terms.

## Live verification addendum

After publication, Doctor Biz supplied a key in the local `.env` file. The full
canonical gate passed, including `TestLiveJudgement`: a real arithmetic judgment
and a repeated request served from disk with a 1ns network timeout. The key was
not printed or committed. This closes the live-test gap for that scenario.
