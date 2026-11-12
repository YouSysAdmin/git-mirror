BIN     := git-mirror
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X git-mirror/pkg/version.Version=$(VERSION)

.PHONY: build install test vet lint fmt clean run-server run-tui snapshot docker

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BIN) ./cmd/git-mirror

install:
	CGO_ENABLED=0 go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/git-mirror

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w cmd internal pkg

clean:
	rm -rf bin dist

# Local dry run of the release pipeline: archives, deb/rpm/apk and container
# images (one image per platform, nothing is published).
snapshot:
	goreleaser release --snapshot --clean --skip=publish

# Build the container image locally without goreleaser (linux/amd64 only).
docker: build
	rm -rf dist/docker-ctx && mkdir -p dist/docker-ctx/linux/amd64
	cp bin/git-mirror dist/docker-ctx/linux/amd64/
	docker build --platform linux/amd64 -t git-mirror:dev -f build/docker/Dockerfile dist/docker-ctx

# Development helpers (use a throwaway data dir).
run-server: build
	GIT_MIRROR_DIR=./.data ./bin/$(BIN) server --listen 127.0.0.1:8080 --ssh-listen 127.0.0.1:2222 --tick 5s

run-tui: build
	GIT_MIRROR_DIR=./.data ./bin/$(BIN) tui
