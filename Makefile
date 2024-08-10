

GO           ?= go
GOFMT        ?= $(GO)fmt
SQLC = sqlc
FIRST_GOPATH := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
BIN_DIR ?= $(shell pwd)/build
POSTGRESQL_URL=postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable

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

# https://huanghantao.github.io/2019/06/17/golang-migrate%E5%91%BD%E4%BB%A4%E8%A1%8C%E7%9A%84%E4%BD%BF%E7%94%A8/ 
migrateUp:
	migrate -database ${POSTGRESQL_URL} -path migrations up

migrateCreate:
	migrate create -ext sql -dir migrations create_tables

migrateDown:
	migrate -database ${POSTGRESQL_URL} -path migrations down

migrateForce:
	migrate  -database ${POSTGRESQL_URL} force 20240809073908

.PHONY: all fmt run build clean
