VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)
GOFLAGS  = -trimpath
PLATFORMS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test test-root test-linux vet fmt cross clean install

build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/afl ./cmd/afl

test:
	go test ./...

## Runs the privileged integration tests on this host (macOS: schg, Linux: chattr +i).
test-root:
	sudo go test ./... -run 'Strong' -v

## Runs the whole suite as root with CAP_LINUX_IMMUTABLE inside Docker.
test-linux:
	docker run --rm --cap-add LINUX_IMMUTABLE -v "$(CURDIR)":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.27 go test ./...

## Proves the capability check: same image without the cap must fail with exit 3.
test-linux-nocap:
	docker run --rm -v "$(CURDIR)":/src -w /src -e GOFLAGS=-buildvcs=false golang:1.27 sh -c 'go build -o /tmp/afl ./cmd/afl && touch /tmp/x && /tmp/afl lock /tmp/x; echo "exit=$$?"'

vet:
	go vet ./...
	GOOS=linux GOARCH=amd64 go vet ./...
	GOOS=linux GOARCH=arm64 go vet ./...
	GOOS=linux GOARCH=386 go vet ./...
	GOOS=freebsd GOARCH=amd64 go vet ./...
	GOOS=openbsd GOARCH=amd64 go vet ./...

fmt:
	gofmt -l -w .

cross:
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/afl-$$os-$$arch ./cmd/afl || exit 1; \
	done

install:
	CGO_ENABLED=0 go install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/afl

clean:
	rm -rf bin dist
