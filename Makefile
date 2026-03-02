VERSION ?= 0.2.4
BINARY  := verbose
DEST    := $(HOME)/go/bin/$(BINARY)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: install build

install: ## Build and install to ~/go/bin/
	go build -ldflags "$(LDFLAGS)" -o $(DEST) .
	@echo "installed $(DEST) $(VERSION)"

build: ## Build locally
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .
