# Orchestrator Makefile

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
GIT_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -X main.Version=$(VERSION) \
           -X main.GitCommit=$(GIT_COMMIT) \
           -X main.BuildDate=$(BUILD_DATE)

.PHONY: build install clean test

build:
	go build -ldflags "$(LDFLAGS)" -o orchestrator ./cmd/orchestrator

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/orchestrator

clean:
	rm -f orchestrator
	go clean

test:
	go test ./...

# Development build (no version info)
dev:
	go build -o orchestrator ./cmd/orchestrator

# Show version that would be embedded
version:
	@echo "Version:    $(VERSION)"
	@echo "Git Commit: $(GIT_COMMIT)"
	@echo "Build Date: $(BUILD_DATE)"
