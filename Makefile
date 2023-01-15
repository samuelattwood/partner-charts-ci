BINARY_NAME=partner-charts-ci

VERSION := $(shell git describe --tags)
COMMIT_HASH := $(shell git rev-parse --short HEAD)

GO_LDFLAGS :=  $(shell echo \-ldflags \"-s -w -X 'main.version=$(VERSION)' -X 'main.commit=$(COMMIT_HASH)'\")

default: build

build:
	mkdir -p bin
	go build -o bin/$(BINARY_NAME) $(GO_LDFLAGS)

build-darwin-amd64:
	GOOS=darwin \
	GOARCH=amd64 \
	  go build -o bin/$(BINARY_NAME)-darwin-amd64 $(GO_LDFLAGS)

build-darwin-arm64:
	GOOS=darwin \
	GOARCH=arm64 \
	  go build -o bin/$(BINARY_NAME)-darwin-arm64 $(GO_LDFLAGS)

build-darwin-universal: build-darwin-amd64 build-darwin-arm64
	lipo -create -output bin/$(BINARY_NAME)-darwin-universal bin/$(BINARY_NAME)-darwin-amd64 bin/$(BINARY_NAME)-darwin-arm64

clean:
	go clean
