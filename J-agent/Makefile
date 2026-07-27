APP := j-agent
BIN_DIR := bin
ENTRY := ./cmd/j-agent
PROVIDER ?= openai
MODEL ?= Qwen3.6-35B-A3B-oQ4e-mtp
BASE_URL ?= http://127.0.0.1:8000/v1
API_KEY_ENV ?= OMLX_API_KEY
REASONING_FIELD ?= reasoning_content

.PHONY: run build test race fmt-check vet clean check

run:
	go run $(ENTRY) --provider $(PROVIDER) --model $(MODEL) --base-url $(BASE_URL) --api-key-env $(API_KEY_ENV) --reasoning-field $(REASONING_FIELD)

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
