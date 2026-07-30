BIN_DIR := bin

OMLX_PYTHON ?= /opt/homebrew/opt/omlx/libexec/bin/python3.11
PYTHON ?= python3

.PHONY: build check test race fmt-check vet python-test fetch-lens install-script-check clean

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/jspace-server ./cmd/jspace-server
	go build -o $(BIN_DIR)/jspace-record ./cmd/jspace-record
	go build -o $(BIN_DIR)/jspace-gateway ./cmd/jspace-gateway
	go build -o $(BIN_DIR)/jspace-observer ./cmd/jspace-observer

test:
	go test ./...
	PYTHONPATH=probe $(PYTHON) -m unittest discover -s probe -p 'test_*.py'

race:
	go test -race ./...

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

vet:
	go vet ./...

python-test:
	PYTHONPATH=probe $(PYTHON) -m unittest discover -s probe -p 'test_*.py'

install-script-check:
	sh -n install.sh

fetch-lens:
	$(OMLX_PYTHON) scripts/fetch-lens.py

check: fmt-check vet test race build install-script-check

clean:
	rm -rf $(BIN_DIR) probe/__pycache__ scripts/__pycache__
