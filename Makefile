helpers = helpers/grab-check helpers/host-grab-check
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test install install-helpers

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C src build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../check-mk-passive-agent .

test:
	go -C src vet ./...
	go -C src test -count=1 ./...

install: install-helpers

install-helpers: $(helpers)
	install -D -t ~/local/bin $^
