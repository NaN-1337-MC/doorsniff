# doorsniff
# Minecraft server scanner written in Go.

.PHONY: all build run test tidy clean help scan tui list init version

BINARY := doorsniff
CMD_DIR := ./cmd/doorsniff
PKG := ./...

VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null)
DATE := $(shell date -u +%Y-%m-%d)

LDFLAGS := -X github.com/renanbray/doorsniff/version.Version=$(VERSION)
LDFLAGS += -X github.com/renanbray/doorsniff/version.Commit=$(COMMIT)
LDFLAGS += -X github.com/renanbray/doorsniff/version.Date=$(DATE)

all: build

## help: print this help
help:
	@echo "Available targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST)

## build: compile the binary into ./bin/$(BINARY)
build:
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(CMD_DIR)

## run: build and run with the given args (usage: make run ARGS="scan")
run: build
	./bin/$(BINARY) $(ARGS)

## test: run smoke tests
test:
	go test -count=1 -short $(PKG)

## tidy: run go mod tidy
tidy:
	go mod tidy

## clean: remove build artifacts
clean:
	rm -rf bin/

# Convenience shortcuts for subcommands
scan: build
	./bin/$(BINARY) scan $(ARGS)

tui: build
	./bin/$(BINARY) tui $(ARGS)

list: build
	./bin/$(BINARY) list $(ARGS)

init: build
	./bin/$(BINARY) init $(ARGS)
