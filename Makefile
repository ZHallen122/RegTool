.PHONY: build hub hub-run test vet lint snapshot install compose-up compose-down demo \
	helm-lint helm-template kind-e2e

COMPOSE := docker compose -f deploy/docker-compose.yml

CHART := deploy/helm/regtool-hub
KIND_CLUSTER ?= regtool-hub-e2e

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

# Every file in $(CHART)/ci is one value combination the chart has to keep
# rendering; this is what CI lints.
helm-lint:
	helm lint $(CHART) --strict
	@for values in $(CHART)/ci/*-values.yaml; do \
		echo "==> $$values"; \
		helm lint $(CHART) --strict --values "$$values" || exit 1; \
	done

helm-template:
	@for values in $(CHART)/ci/*-values.yaml; do \
		echo "==> $$values"; \
		helm template regtool-hub $(CHART) --values "$$values" > /dev/null || exit 1; \
	done
	@echo "all value files render"

# The local mirror of the kind-e2e CI job: build the image from this checkout,
# load it into a throwaway cluster, install the chart and poke the endpoints.
# Needs docker, kind, kubectl, helm and jq. The cluster is deleted at the end,
# including on failure.
kind-e2e:
	docker build -t regtool-hub:ci .
	kind create cluster --name $(KIND_CLUSTER)
	@set -e; \
	trap 'kind delete cluster --name $(KIND_CLUSTER)' EXIT; \
	kind load docker-image regtool-hub:ci --name $(KIND_CLUSTER); \
	helm install regtool-hub $(CHART) \
		--set image.repository=regtool-hub \
		--set image.tag=ci \
		--set image.pullPolicy=Never \
		--set hub.checkInterval=30s \
		--wait --timeout 3m; \
	kubectl rollout status deployment/regtool-hub --timeout 2m; \
	helm test regtool-hub --logs; \
	kubectl port-forward svc/regtool-hub 8080:8080 & \
	pf=$$!; \
	trap 'kill $$pf 2>/dev/null || true; kind delete cluster --name $(KIND_CLUSTER)' EXIT; \
	for _ in $$(seq 1 30); do curl -fsS --max-time 2 http://localhost:8080/healthz > /dev/null && break; sleep 1; done; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/readyz)" = "200"; \
	etag=$$(curl -fsS -D - -o /dev/null http://localhost:8080/v1/sources | tr -d '\r' | awk 'tolower($$1) == "etag:" { print $$2 }'); \
	test -n "$$etag"; \
	test "$$(curl -s -o /dev/null -w '%{http_code}' -H "If-None-Match: $$etag" http://localhost:8080/v1/sources)" = "304"; \
	for _ in $$(seq 1 60); do \
		[ "$$(curl -fsS http://localhost:8080/v1/health | jq '.results | length')" -gt 0 ] && break; \
		sleep 1; \
	done; \
	curl -fsS http://localhost:8080/metrics | grep -q '^regtool_hub_mirror_up'; \
	echo "kind e2e passed"
