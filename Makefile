BINARY_NAME=partner-charts-ci

VERSION := $(shell git describe --tags)
COMMIT_HASH := $(shell git rev-parse --short HEAD)

default: build

build:
	mkdir -p bin
	go build -o bin/$(BINARY_NAME) -ldflags "-s -w -X 'main.Version=$(VERSION)' -X 'main.Commit=$(COMMIT_HASH)'"

clean:
	go clean
