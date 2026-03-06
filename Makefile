BINARY := opentree
INSTALL_DIR := /usr/local/bin

.PHONY: build install uninstall

build:
	go build -o $(BINARY) ./cmd/opentree

install: build
	install -m 0755 $(BINARY) $(INSTALL_DIR)/$(BINARY)
	$(INSTALL_DIR)/$(BINARY) install-completion

uninstall:
	rm -f $(INSTALL_DIR)/$(BINARY)
