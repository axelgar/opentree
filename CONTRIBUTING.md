# Contributing

## Tools

- **golangci-lint**, at exactly the version in [`.golangci-version`](.golangci-version):
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(cat .golangci-version)`.
  `make lint` refuses to run against any other version — a v1 binary misreads the
  `version: "2"` config and reports issues CI does not, and a different v2 patch
  release ships a different linter set.
- **goimports**: `go install golang.org/x/tools/cmd/goimports@latest`
- **shellcheck**: `brew install shellcheck` (the release path is mostly shell)
- **jq**: `brew install jq`
- `govulncheck` and `deadcode` need no install — they run via `go tool`.

## Before opening a PR

```bash
make check      # the pre-commit gate, and what the hook runs
make check-all  # everything CI runs, including the slow parts
```

`make check` runs, in order:

| step         | what it catches                                                       |
| ------------ | --------------------------------------------------------------------- |
| `fmt`        | `goimports`                                                            |
| `toolchain`  | building against an older Go than `go.mod` pins                        |
| `tidy`       | `go.mod`/`go.sum` drift                                                |
| `lint`       | `golangci-lint` — `govet`, `errcheck`, `staticcheck`, `gosec`, `errorlint`, `forcetypeassert`, `nilerr`, `gocritic` and more (see `.golangci.yml`) |
| `vulncheck`  | known advisories in dependencies                                       |
| `deadcode`   | code unreachable from `cmd/opentree`                                   |
| `shellcheck` | every tracked shell script                                             |
| `npm-check`  | the five npm packages disagreeing about platforms or versions          |
| `test`       | `go test ./...`                                                        |
| `smoke`      | the built binary failing to come up, or a command missing its help     |

`make check-all` adds what is too slow for a commit hook: cross-compiling all
four release targets, `-race -shuffle=on`, and the coverage floor.

## What CI enforces

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs the same checks and
several a laptop cannot, on every push and pull request. This applies whether the
change was written by a human or by an AI coding agent — the gate is the same.

- **Both platforms.** Tests run on `ubuntu-latest` *and* `macos-latest`. opentree
  drives tmux, the clipboard and desktop notifications, and each of those is a
  different implementation per platform.
- **Race and shuffle.** `-race` because the TUI runs bubbletea's event loop
  alongside ACP readers and tmux pollers; `-shuffle=on` because a suite that only
  passes in declaration order has shared state it is not admitting to.
- **No silent skips.** Roughly a third of the suite shells out to `git`, `tmux`
  or `gh`, and skips itself when the binary is absent. CI installs all three and
  [`scripts/check-skips.sh`](scripts/check-skips.sh) fails the build if a test
  skipped for a missing one — otherwise a runner without tmux turns three
  packages into a green tick that proved nothing.
- **Coverage floor.** [`.github/coverage-floor`](.github/coverage-floor) is a
  ratchet, not a target. It stops a refactor quietly deleting a package's tests.
  When coverage climbs past it, raise the number in the PR that earned it.
- **Cross-compilation.** All four release targets are built on every PR, so a
  `GOOS`-specific file that does not compile fails here rather than at tag time.
- **Release configuration.** `goreleaser check`, the npm package consistency
  check, shellcheck and actionlint all run on every PR, because everything they
  cover is otherwise only exercised by a tag — where a mistake is published and
  immutable.
- **Supply chain.** `govulncheck`, `gitleaks`, CodeQL with `security-extended`,
  and every action pinned to a commit SHA rather than a mutable tag. Dependabot
  keeps the pins and dependencies current.

The `CI` job is the one to require in branch protection: it depends on all the
others and fails if any of them did.

## Releasing

Push a `vMAJOR.MINOR.PATCH` tag. [`release.yml`](.github/workflows/release.yml)
runs the full CI suite against the tag first, then checks that the tag is
reachable from `main` and that the version is not already on npm, and only then
builds and publishes. After publishing it installs the package from the registry
and runs `opentree --version` against it.

A prerelease tag (`v1.2.0-rc.1`) goes out on the `next` npm dist-tag and skips
the Homebrew tap, so `npm install @axelgar/opentree` and `brew install opentree`
keep resolving to the last stable release. Install one with
`npm install @axelgar/opentree@next`.

## Pre-commit hook

Run once after cloning to enforce `make check` on every commit:

```bash
make install-hooks
```

This points git at the tracked `.githooks/` directory
(`git config core.hooksPath .githooks`). Skip a one-off commit with
`git commit --no-verify`.
