# Build regtool-hub and ship it on a base image with no shell and no package
# manager. The binary is pure Go — modernc.org/sqlite needs no libc — so
# CGO_ENABLED=0 produces a static binary that distroless/static can run.

FROM golang:1.25-alpine AS build

WORKDIR /src

# The module files are copied on their own first so `go mod download` is cached
# against them: editing a .go file must not re-download the module graph.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags "-s -w" -o /out/regtool-hub ./cmd/regtool-hub

# The data directory is created here rather than in the final stage, because
# distroless has no shell to mkdir with. Owning it as 65532 — the uid behind
# the nonroot tag — is what lets the container create hub.db in a fresh volume.
RUN mkdir -p /data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/regtool-hub /usr/local/bin/regtool-hub
COPY --from=build --chown=65532:65532 /data /data

WORKDIR /data
VOLUME ["/data"]

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/regtool-hub", "--db", "/data/hub.db"]
CMD ["--addr", ":8080"]
