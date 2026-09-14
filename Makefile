.PHONY: build hub hub-run test vet lint snapshot install compose-up compose-down demo

COMPOSE := docker compose -f deploy/docker-compose.yml

# The tape is rendered in the official VHS container so nothing has to be
# installed locally. The tag is pinned: the image bundles its own Chromium, and
# a newer one has broken frame capture before.
VHS_IMAGE ?= ghcr.io/charmbracelet/vhs:v0.10.0

PKG := github.com/ZHallen122/RegTool/internal/cli
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o regtool .

hub:
	go build -o regtool-hub ./cmd/regtool-hub

hub-run:
	go run ./cmd/regtool-hub --log-level debug

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

# Re-records assets/demo.gif from demo/demo.tape. The tape runs inside the
# container, so the binary it drives is built for linux/amd64 and left in demo/,
# which is on the PATH the tape sets up.
demo:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o demo/regtool .
	docker run --rm -v "$(CURDIR):/vhs" $(VHS_IMAGE) demo/demo.tape

compose-up:
	$(COMPOSE) up -d --build

# -v also drops the check history and the Grafana state, which is what you want
# from a stack you brought up to try it.
compose-down:
	$(COMPOSE) down -v
