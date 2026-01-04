

GO           ?= go
GOFMT        ?= $(GO)fmt
SQLC = sqlc
FIRST_GOPATH := $(firstword $(subst :, ,$(shell $(GO) env GOPATH)))
BIN_DIR ?= $(shell pwd)/build
POSTGRESQL_URL=postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable

# Cloud Foundry configuration
CF_API ?= https://api.cf.sap.hana.ondemand.com
CF_ORG ?= MaCo-devops
CF_SPACE ?= DEVOPS
CF_USER ?=
CF_PASSWORD ?=

# MTA configuration
MTA_JAR_MERGE ?= true

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

# Cloud Foundry deployment commands
cf-login:
	@echo ">> Logging into Cloud Foundry"
	@if [ -z "$(CF_USER)" ] || [ -z "$(CF_PASSWORD)" ]; then \
		echo "Error: CF_USER and CF_PASSWORD must be set"; \
		echo "Usage: make cf-login CF_USER=username CF_PASSWORD=password"; \
		exit 1; \
	fi
	cf login -a $(CF_API) -o $(CF_ORG) -s $(CF_SPACE) -u $(CF_USER) -p "$(CF_PASSWORD)"

cf-build:
	@echo ">> Building MTA archive with MBT"
	@which mbt > /dev/null || (echo "Error: mbt not found. Please install SAP Multi-Target Cloud Foundry CLI (MBT)" && exit 1)
	mbt build --mtar=mta_archives/$(shell grep '^ID:' mta.yaml | awk '{print $$2}')_$(shell grep '^version:' mta.yaml | awk '{print $$2}').mtar

cf-deploy: cf-login cf-build
	@echo ">> Deploying to Cloud Foundry"
	cf deploy mta_archives/*.mtar --mtar

# Deploy without rebuilding (assumes .mtar already exists)
cf-deploy-only: cf-login
	@echo ">> Deploying existing MTA archive to Cloud Foundry"
	@if [ ! -f mta_archives/*.mtar ]; then \
		echo "Error: No .mtar file found in mta_archives/"; \
		echo "Run 'make cf-build' first to build the archive"; \
		exit 1; \
	fi
	cf deploy mta_archives/*.mtar --mtar

# Quick deploy command (login + build + deploy in one)
deploy: cf-deploy

.PHONY: all fmt run build clean cf-login cf-build cf-deploy cf-deploy-only deploy
