BINARY_NAME=partner-charts-ci

default: build

build:
	go build -ldflags "-s -w"

clean:
	go clean
