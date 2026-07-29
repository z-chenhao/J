GO ?= go
BIN_DIR ?= bin

.PHONY: build test race fmt-check vet clean check

build:
	mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/j ./cmd/j

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

fmt-check:
	test -z "$$(gofmt -l .)"

vet:
	$(GO) vet ./...

clean:
	rm -rf $(BIN_DIR)

check: fmt-check vet test race build
