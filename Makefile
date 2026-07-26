# Local development Makefile for cpi-delivery backend.
# Deployment is orchestrated by cpi-delivery-product.

GO           ?= go
GOFMT        ?= $(GO)fmt
DOCKER       ?= docker
BIN_DIR      ?= $(shell pwd)/build

# --- Local PostgreSQL ---
DB_CONTAINER ?= cpi-delivery-db
DB_IMAGE     ?= postgres:15

all: fmt build

build:
	@echo ">> building binaries"
	$(GO) build -o build/maco-deploy

fmt:
	@echo ">> format code style"
	$(GOFMT) -w $$(find .  -path ./.history -prune -o -name '*.go' -print)

clean:
	rm -rf $(BIN_DIR)

test:
	$(GO) test ./... -v

# --- run-db: start local PostgreSQL (one-time setup) ---
# Data persists in named volume "cpi-delivery-pgdata" across container restarts.
# Idempotent: skips if container already exists.
# Use DOCKER=podman if using Podman.

run-db:
	@if $(DOCKER) ps -a --format '{{.Names}}' | grep -q '^$(DB_CONTAINER)$$'; then \
		$(DOCKER) start $(DB_CONTAINER) 2>/dev/null || true; \
		echo ">> $(DB_CONTAINER) already exists, started"; \
	else \
		$(DOCKER) run -d --name $(DB_CONTAINER) \
			-e POSTGRES_USER=postgres \
			-e POSTGRES_PASSWORD=passw0rd \
			-e POSTGRES_DB=macodeploy \
			-p 5432:5432 \
			-v cpi-delivery-pgdata:/var/lib/postgresql/data \
			--restart=unless-stopped \
			$(DB_IMAGE) && \
		echo ">> $(DB_CONTAINER) created and started"; \
	fi

# --- sync-env: pull VCAP_SERVICES from deployed CF app and write .env ---
#
# Prerequisites:
#   - cf CLI logged in and targeting the correct org/space
#   - jq installed
#   - App must be deployed (pulls env from running app)
#
# What it does:
#   1. Checks CF CLI login status
#   2. Fetches VCAP_SERVICES and VCAP_APPLICATION from the deployed app via CF API
#   3. Writes .env (format compatible with VS Code launch.json envFile)
#
# Note: credentials change on each cf deploy/restage. Re-run after deploy.
#
# Usage:
#   make sync-env                        # uses default app name
#   make sync-env CF_APP=my-app-name     # override app name

CF_APP ?= cpi-delivery

sync-env: _check-cf
	@echo ">> pulling env from deployed app '$(CF_APP)'..."
	@APP_GUID=$$(cf app $(CF_APP) --guid) && \
	cf curl /v3/apps/$$APP_GUID/env > /tmp/.cpi-delivery-env.json && \
	VCAP_SERVICES=$$(jq -c '.system_env_json.VCAP_SERVICES' /tmp/.cpi-delivery-env.json) && \
	VCAP_APP=$$(jq -c '.application_env_json.VCAP_APPLICATION' /tmp/.cpi-delivery-env.json) && \
	printf 'VCAP_SERVICES=%s\nVCAP_APPLICATION=%s\nVITE_DEV_URL=http://localhost:5173\nOTEL_SDK_DISABLED=true\nLOCAL_POSTGRES_URI=postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable\n' "$$VCAP_SERVICES" "$$VCAP_APP" > .env && \
	rm -f /tmp/.cpi-delivery-env.json && \
	echo ">> .env written successfully (from app '$(CF_APP)')"

_check-cf:
	@cf target >/dev/null 2>&1 || (echo "ERROR: cf CLI not logged in. Run: cf login -a <api> -o <org> -s <space>" && exit 1)
	@echo ">> CF target: $$(cf target | grep -E 'org:|space:' | tr '\n' ' ')"

.PHONY: all fmt build clean test run-db sync-env _check-cf
