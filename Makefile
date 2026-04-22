BINARY := ws
INSTALL_DIR := $(HOME)/.local/bin

.PHONY: build install clean test

build:
	go build -o $(BINARY) .

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@if [ "$$(uname -s)" = "Darwin" ]; then codesign --sign - --force $(INSTALL_DIR)/$(BINARY); fi
	@echo "Installed to $(INSTALL_DIR)/$(BINARY)"

test:
	go test ./...

clean:
	rm -f $(BINARY)
