.PHONY: build test vet lint snapshot install

PKG := github.com/ZHallen122/RegTool/internal/cli
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o regtool .

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.5.0 run ./...

snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean

install:
	go install -ldflags "$(LDFLAGS)" .
