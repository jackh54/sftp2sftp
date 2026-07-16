BINARY ?= mctransfer
CMD    := ./cmd/mctransfer

.PHONY: build build-all test clean

build:
	go build -o bin/$(BINARY) $(CMD)

build-all:
	GOOS=linux   GOARCH=amd64 go build -o bin/$(BINARY)-linux-amd64   $(CMD)
	GOOS=linux   GOARCH=arm64 go build -o bin/$(BINARY)-linux-arm64   $(CMD)
	GOOS=darwin  GOARCH=amd64 go build -o bin/$(BINARY)-darwin-amd64  $(CMD)
	GOOS=darwin  GOARCH=arm64 go build -o bin/$(BINARY)-darwin-arm64  $(CMD)
	GOOS=windows GOARCH=amd64 go build -o bin/$(BINARY)-windows-amd64.exe $(CMD)

test:
	go test ./... -count=1

clean:
	rm -rf bin/
