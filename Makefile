.PHONY: build run clean test install

# Binary name
BINARY_NAME=qws

# Directories
CMD_DIR=./cmd/qws
BUILD_DIR=.

# Go parameters
GOBASE=$(shell pwd)
GOBIN=$(GOBASE)/bin

build:
	@echo "→ Building $(BINARY_NAME)..."
	go build -o $(BINARY_NAME) $(CMD_DIR)
	@echo "✓ Build completed: $(BINARY_NAME)"

run: build
	@echo "→ Running $(BINARY_NAME)..."
	./$(BINARY_NAME)

test:
	@echo "→ Running tests..."
	go test -v ./...

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
	@echo "  make clean   - Remove binary"
	@echo "  make install - Install to /usr/local/bin"
	@echo "  make deps    - Install dependencies"
	@echo "  make lint    - Check code"
