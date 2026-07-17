BINARY := opentree
INSTALL_DIR := /usr/local/bin

.PHONY: build install uninstall fmt lint vulncheck deadcode test check install-hooks demo

build:
	go build -o $(BINARY) ./cmd/opentree

install: build
	install -m 0755 $(BINARY) $(INSTALL_DIR)/$(BINARY)
	$(INSTALL_DIR)/$(BINARY) install-completion

uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)

fmt:
	goimports -w -local github.com/axelgar/opentree $(shell find . -name "*.go")

lint:
	golangci-lint run

vulncheck:
	go tool govulncheck ./...

deadcode:
	@out=$$(go tool deadcode ./cmd/opentree); \
	if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

test:
	go test ./...

check: fmt lint vulncheck deadcode test

install-hooks:
	git config core.hooksPath .githooks

# Regenerate docs/demo.gif: seed a throwaway repo, then record the TUI with VHS.
# Requires: vhs (brew install vhs). See docs/demo/.
demo:
	bash docs/demo/seed-demo.sh
	vhs docs/demo/demo.tape
