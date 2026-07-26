APP := j
BIN_DIR := bin
ENTRY := ./cmd/j

.PHONY: run build test race fmt-check vet clean check

run:
	go run $(ENTRY)

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
