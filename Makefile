BINARY := ra
INSTALL_DIR := $(HOME)/.local/bin
LDFLAGS=-ldflags "-X github.com/adamtait/remote-agent/internal/logging.buildVersion=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)"

.PHONY: build install clean test

build:
	go build $(LDFLAGS) -o $(BINARY) .

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@if [ "$$(uname -s)" = "Darwin" ]; then codesign --sign - --force $(INSTALL_DIR)/$(BINARY); fi
	@echo "Installed to $(INSTALL_DIR)/$(BINARY)"

test:
	go test ./...

clean:
	rm -f $(BINARY)
