# Local and untagged builds report 0.0.0. GoReleaser overrides VERSION
# with the version from the release tag.
VERSION ?= 0.0.0
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build install test lint fmt clean release run

build:
	@mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devhostd ./cmd/devhostd

install:
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/devhostd

test:
	go test -race ./...

run: build
	./bin/devhostd

lint:
	go vet ./...
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || (printf '%s\n' "gofmt issues"; exit 1)

fmt:
	gofmt -w cmd internal

clean:
	rm -rf bin dist

release:
	@mkdir -p bin
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devhostd-linux-amd64       ./cmd/devhostd
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devhostd-linux-arm64       ./cmd/devhostd
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devhostd-darwin-amd64      ./cmd/devhostd
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devhostd-darwin-arm64      ./cmd/devhostd
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devhostd-windows-amd64.exe ./cmd/devhostd
