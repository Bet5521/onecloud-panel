VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X onecloud-panel/internal/version.Version=$(VERSION) \
	-X onecloud-panel/internal/version.Commit=$(COMMIT) \
	-X onecloud-panel/internal/version.BuildDate=$(BUILD_DATE)

BIN := onecloud-panel
DIST := dist

.PHONY: all build clean test vet fmt tidy run-panel run-agent

all: build

build: $(DIST)/$(BIN)-linux-armv7 $(DIST)/$(BIN)-linux-arm64 $(DIST)/$(BIN)-windows-amd64.exe

$(DIST):
	mkdir -p $(DIST)

$(DIST)/$(BIN)-linux-armv7: $(DIST)
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/onecloud-panel

$(DIST)/$(BIN)-linux-arm64: $(DIST)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/onecloud-panel

$(DIST)/$(BIN)-windows-amd64.exe: $(DIST)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $@ ./cmd/onecloud-panel

test:
	CGO_ENABLED=0 go test ./... -p 1

vet:
	go vet ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

run-panel:
	go run ./cmd/onecloud-panel panel --listen :8000 --data-dir ./runtime-data

run-agent:
	go run ./cmd/onecloud-panel agent --listen :9000 --data-dir ./runtime-agent

clean:
	rm -rf $(DIST)
