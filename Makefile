.PHONY: build test vet install

build:
	go build -o regtool .

test:
	go test -race ./...

vet:
	go vet ./...

install:
	go install .
