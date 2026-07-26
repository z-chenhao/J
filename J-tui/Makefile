APP := j-tui
BIN_DIR := bin
ENTRY := ./cmd/j-tui
PROVIDER ?= ollama
MODEL ?= qwen3.6:27b-q4_K_M

.PHONY: run trace build test race fmt-check vet clean check

run:
	go run $(ENTRY) --provider $(PROVIDER) --model $(MODEL)

trace:
	go run $(ENTRY) --mode json --provider $(PROVIDER) --model $(MODEL) "Use the bash tool to run pwd, then report the output."

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(APP) $(ENTRY)

test:
	go test ./...

race:
	go test -race ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

vet:
	go vet ./...

check: fmt-check vet test race build

clean:
	rm -rf $(BIN_DIR)
