

GO           ?= go
GOFMT        ?= $(GO)fmt
SQLC = ~/go/bin/sqlc
FIRST_GOPATH := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
BIN_DIR ?= $(shell pwd)/build


all:  fmt run

sqlgen: |
	@echo ">> generating db code"
	$(SQLC) -f sqlc/sqlc.yaml  generate

build: | sqlgen
	@echo ">> building binaries"
	$(GO) build -o build/maco-deploy

fmt:
	@echo ">> format code style"
	$(GOFMT) -w $$(find .  -path ./.history -prune -o -name '*.go' -print)

run: build
	@echo ">> start locally"
	 build/maco-deploy

clean:
	rm -rf $(BIN_DIR)

.PHONY: all fmt run build clean