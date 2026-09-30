.PHONY: build run clean test vet install deps lint help release

# Binary name
BINARY_NAME=qws

# Directories
CMD_DIR=./cmd/qws
BUILD_DIR=.

# Go parameters
GOBASE=$(shell pwd)
GOBIN=$(GOBASE)/bin

# Version from git: the last tag, commits since it, -dirty; "dev" outside a
# repository (specs/004-releases)
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	@echo "→ Building $(BINARY_NAME) $(VERSION)..."
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY_NAME) $(CMD_DIR)
	@echo "✓ Build completed: $(BINARY_NAME)"

run: build
	@echo "→ Running $(BINARY_NAME)..."
	./$(BINARY_NAME)

test:
	@echo "→ Running tests..."
	go test ./...

vet:
	go vet ./...

# GitHub release on a pushed version tag: make release TAG=vX.Y.Z
release:
	scripts/release.sh $(TAG)

clean:
	@echo "→ Cleaning..."
	rm -f $(BINARY_NAME)
	go clean
	@echo "✓ Cleaning completed"

install: build
	@echo "→ Installing $(BINARY_NAME) to /usr/local/bin..."
	sudo cp -f $(BINARY_NAME) /usr/local/bin/
	@echo "✓ Installation completed"

deps:
	@echo "→ Installing dependencies..."
	go get github.com/jezek/xgb@latest
	go get golang.org/x/term@latest
	go mod tidy
	@echo "✓ Dependencies installed"

lint:
	@echo "→ Checking code..."
	go vet ./...
	gofmt -s -w .
	@echo "✓ Check completed"

help:
	@echo "Available commands:"
	@echo "  make build   - Build the project"
	@echo "  make run     - Build and run"
	@echo "  make test    - Run tests"
	@echo "  make vet     - Run go vet"
	@echo "  make release TAG=vX.Y.Z - GitHub release on a pushed tag"
	@echo "  make clean   - Remove binary"
	@echo "  make install - Install to /usr/local/bin"
	@echo "  make deps    - Install dependencies"
	@echo "  make lint    - Check code"
