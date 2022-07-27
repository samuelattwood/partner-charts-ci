BINARY_NAME=partner-charts-ci

default: build

build:
	mkdir -p bin
	go build -o bin/$(BINARY_NAME) -ldflags "-s -w"

clean:
	go clean
