BINARY := bethoven
PKG := ./cmd/bethoven

.PHONY: build build-linux build-linux-arm test run clean

## build: compile for the host OS/arch
build:
	go build -o $(BINARY) $(PKG)

## build-linux: cross-compile a static Linux x86-64 binary (for the VM)
build-linux:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY)-linux-amd64 $(PKG)

## build-linux-arm: cross-compile a static Linux ARM64 binary
build-linux-arm:
	GOOS=linux GOARCH=arm64 go build -o $(BINARY)-linux-arm64 $(PKG)

## test: run the full hermetic suite with the race detector
test:
	go test -race ./...

## run: build and run locally on port 2222 with a dev invite code
run: build
	BETHOVEN_INVITE_CODE=letmein ./$(BINARY)

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64 $(BINARY)-linux-arm64
