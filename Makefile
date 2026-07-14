BINARY := opentree
INSTALL_DIR := /usr/local/bin

.PHONY: build install uninstall fmt lint vulncheck deadcode test check install-hooks

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
	go tool deadcode ./cmd/opentree

test:
	go test ./...

check: fmt lint vulncheck deadcode test

install-hooks:
	git config core.hooksPath .githooks
