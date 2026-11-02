# Jira CLI build targets. Requires Go 1.27 (go.mod toolchain directive).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := bin/jira

.PHONY: all spec-validate spec-update security smoke generate verify-generated test test-race vet lint build docker clean

all: verify-generated vet test build

## Validate specifications, overlays and risk metadata (no output written).
spec-validate:
	go run ./cmd/jiragen validate

## Regenerate generated/ from specs/manifest.yaml.
generate:
	go generate ./generated/...

## Refresh pinned versions/checksums after reviewing a spec/overlay change.
spec-update:
	go run ./cmd/jiragen update

## Fail if generated/ is not in sync with specs/.
verify-generated:
	go run ./cmd/jiragen check

vet:
	go vet ./...
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (echo "gofmt needed" && exit 1)

test:
	go test ./...

test-race:
	go test -race ./...

## Static security checks (installed on demand).
security:
	GOTOOLCHAIN=$$(go env GOVERSION) go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## Run the binary with JSON debug logs; checks exit codes, log stream and secret leaks.
smoke: build
	scripts/ci-smoke.sh $(BIN) ci-artifacts

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/jira

docker:
	docker build --build-arg VERSION=$(VERSION) -t jira-cli:$(VERSION) .

clean:
	rm -rf bin dist ci-artifacts
