# Local development Makefile for cpi-delivery backend.
# Deployment is orchestrated by cpi-delivery-product.

GO           ?= go
GOFMT        ?= $(GO)fmt
SQLC = sqlc
FIRST_GOPATH := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
BIN_DIR ?= $(shell pwd)/build
POSTGRESQL_URL=postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable

all:  fmt run

build:
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

prepare:
	$(GO) install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	$(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

test:
	$(GO) test ./... -v

.PHONY: all fmt run build clean prepare test
