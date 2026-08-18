BINARY := opentree
INSTALL_DIR := /usr/local/bin
GOLANGCI_VERSION := $(shell tr -d '[:space:]' < .golangci-version)

.PHONY: build install uninstall fmt fmt-check lint vulncheck deadcode test test-race cover \
        tidy build-all shellcheck npm-check smoke toolchain check check-all install-hooks demo

build:
	go build -o $(BINARY) ./cmd/opentree

install: build
	install -m 0755 $(BINARY) $(INSTALL_DIR)/$(BINARY)
	$(INSTALL_DIR)/$(BINARY) install-completion

uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)

# Worktrees live inside the repo, so a plain find would reformat the checked-out
# copies of this project sitting under them. git ls-files lists only what this
# checkout actually tracks.
fmt:
	goimports -w -local github.com/axelgar/opentree $(shell git ls-files '*.go')

# A gate must not rewrite the tree it is gating. `check` used to start with
# `fmt`, and the pre-commit hook runs `check` — so goimports reformatted the
# working tree while git went on to commit the index it had already read.
# Unformatted code committed green and the fix sat behind as an unstaged change.
fmt-check:
	@out=$$(goimports -l -local github.com/axelgar/opentree $(shell git ls-files '*.go')); \
	if [ -n "$$out" ]; then \
		echo "not formatted — run 'make fmt':"; \
		echo "$$out" | sed 's/^/  /'; \
		exit 1; \
	fi

# The version lives in .golangci-version so this, CI and CONTRIBUTING.md cannot
# drift. A v1 binary misreads the v2 config and reports issues CI does not, and
# a different v2 patch release ships different linters — either way the failure
# is "works on my machine" in the direction that wastes the most time.
lint:
	@have=$$(golangci-lint version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1); \
	if [ "$$have" != "$(GOLANGCI_VERSION)" ]; then \
		echo "golangci-lint $(GOLANGCI_VERSION) is required, found $${have:-none}"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v$(GOLANGCI_VERSION)"; \
		exit 1; \
	fi
	golangci-lint run

vulncheck:
	go tool govulncheck ./...

deadcode:
	@out=$$(go tool deadcode ./cmd/opentree); \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

test:
	go test ./...

# What CI runs. -race because the TUI drives bubbletea's loop alongside ACP
# readers and tmux pollers; -shuffle because a suite that only passes in
# declaration order has shared state it is not admitting to.
test-race:
	go test ./... -race -shuffle=on -timeout=20m

cover:
	go test ./... -covermode=atomic -coverprofile=coverage.out
	./scripts/check-coverage.sh coverage.out

toolchain:
	./scripts/check-toolchain.sh

tidy:
	@go mod tidy
	@git diff --exit-code go.mod go.sum || { echo "go.mod/go.sum are not tidy — commit the result of 'go mod tidy'"; exit 1; }

# goreleaser builds these four at tag time. A GOOS-specific file that does not
# compile should fail here, not there.
build-all:
	@for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "  $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -o /dev/null ./cmd/opentree || exit 1; \
	done

shellcheck:
	git ls-files '*.sh' .githooks | xargs shellcheck --severity=warning

npm-check:
	./scripts/check-npm-packages.sh

# Walks every command in the real binary. Catches the cobra wiring that unit
# tests do not reach — a duplicate flag panics at process start, not at call.
smoke: build
	./scripts/smoke.sh ./$(BINARY)

# The pre-commit gate: everything that is fast enough to run on every commit.
check: fmt-check toolchain tidy lint vulncheck deadcode shellcheck npm-check test smoke

# Everything CI runs, including the slow parts. Worth running before opening a
# PR; `check` is what the hook enforces.
check-all: check build-all test-race cover

install-hooks:
	git config core.hooksPath .githooks

# Regenerate docs/demo.gif: seed a throwaway repo, then record the TUI with VHS.
# Requires: vhs (brew install vhs). See docs/demo/.
demo:
	bash docs/demo/seed-demo.sh
	vhs docs/demo/demo.tape
