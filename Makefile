

GO           ?= go
GOFMT        ?= $(GO)fmt
FIRST_GOPATH := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
BIN_DIR ?= $(shell pwd)/build


all:  fmt run



build: |
	@echo ">> building binaries"
	$(GO) build -o build/maco-deploy

fmt:
	@echo ">> format code style"
	$(GOFMT) -w $$(find . -path ./vendor -prune -o -name '*.go' -print)
run: build
	@echo ">> start locally"
	 build/maco-deploy
clean:
	rm -rf $(BIN_DIR)

.PHONY: all fmt run build clean