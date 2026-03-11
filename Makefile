BINARY := opentree
INSTALL_DIR := /usr/local/bin

.PHONY: build install uninstall fmt lint test check

build:
	go build -o $(BINARY) ./cmd/opentree

install: build
	install -m 0755 $(BINARY) $(INSTALL_DIR)/$(BINARY)
	$(INSTALL_DIR)/$(BINARY) install-completion

uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)

fmt:
	gofmt -w ./...

lint:
	golangci-lint run

test:
	go test ./...

check: fmt lint test
