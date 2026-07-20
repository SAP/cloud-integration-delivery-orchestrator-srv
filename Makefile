# Local development Makefile for cpi-delivery backend.
# Deployment is orchestrated by cpi-delivery-product.

GO           ?= go
GOFMT        ?= $(GO)fmt
BIN_DIR      ?= $(shell pwd)/build

# --- CF service instance names (must match your CF space) ---
SVC_UAA      ?= cpi-delivery-uaa
SVC_UAA_API  ?= uaa-api
SVC_DB       ?= mmt-devops-pgsql
SVC_DEST     ?= mmt_devops_destination
SVC_CONN     ?= mmt_devops_connectivity
SK_NAME      ?= local-dev

all: fmt build

build:
	@echo ">> building binaries"
	$(GO) build -o build/maco-deploy

fmt:
	@echo ">> format code style"
	$(GOFMT) -w $$(find .  -path ./.history -prune -o -name '*.go' -print)

clean:
	rm -rf $(BIN_DIR)

prepare:
	$(GO) install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
	$(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

test:
	$(GO) test ./... -v

# --- sync-env: assemble .env from CF service keys (stable credentials) ---
#
# Prerequisites:
#   - cf CLI logged in and targeting the correct org/space
#   - jq installed
#
# What it does:
#   1. Checks CF CLI login status (fails early if not logged in)
#   2. Creates service keys named "local-dev" if they don't already exist
#      (this is a one-time side effect on your CF space)
#   3. Reads credentials from each service key
#   4. Assembles VCAP_SERVICES JSON and writes .env
#
# Why service keys:
#   Service key credentials are independent of app bindings.
#   They remain stable across cf deploy/restage — no need to regenerate .env.
#
# Usage:
#   make sync-env                         # uses default service instance names
#   make sync-env SVC_DB=my-pg-instance   # override a specific instance name
#   make sync-env SK_NAME=dev2            # use a different key name

sync-env: _check-cf _ensure-keys
	@echo ">> reading service key credentials..."
	@SK_UAA=$$(cf service-key $(SVC_UAA) $(SK_NAME) | sed -n '/^{/,$$p' | jq -c '.') && \
	SK_UAA_API=$$(cf service-key $(SVC_UAA_API) $(SK_NAME) | sed -n '/^{/,$$p' | jq -c '.') && \
	SK_DB=$$(cf service-key $(SVC_DB) $(SK_NAME) | sed -n '/^{/,$$p' | jq -c '.') && \
	SK_DEST=$$(cf service-key $(SVC_DEST) $(SK_NAME) | sed -n '/^{/,$$p' | jq -c '.') && \
	SK_CONN=$$(cf service-key $(SVC_CONN) $(SK_NAME) | sed -n '/^{/,$$p' | jq -c '.') && \
	VCAP_SERVICES=$$(jq -n -c \
		--argjson uaa "$$SK_UAA" \
		--argjson uaa_api "$$SK_UAA_API" \
		--argjson db "$$SK_DB" \
		--argjson dest "$$SK_DEST" \
		--argjson conn "$$SK_CONN" \
		'{ "xsuaa": [ { "label": "xsuaa", "plan": "application", "name": "$(SVC_UAA)", "tags": ["xsuaa"], "credentials": $$uaa }, { "label": "xsuaa", "plan": "apiaccess", "name": "$(SVC_UAA_API)", "tags": ["xsuaa"], "credentials": $$uaa_api } ], "postgresql-db": [ { "label": "postgresql-db", "plan": "development", "name": "$(SVC_DB)", "tags": ["relational","database"], "credentials": $$db } ], "destination": [ { "label": "destination", "plan": "lite", "name": "$(SVC_DEST)", "tags": ["destination","conn","connsvc"], "credentials": $$dest } ], "connectivity": [ { "label": "connectivity", "plan": "lite", "name": "$(SVC_CONN)", "tags": ["connectivity","conn","connsvc"], "credentials": $$conn } ] }') && \
	VCAP_APP='{"application_name":"cpi-delivery-local","application_uris":["localhost"],"name":"cpi-delivery-local","uris":["localhost"]}' && \
	printf 'VCAP_SERVICES=%s\nVCAP_APPLICATION=%s\nVITE_DEV_URL=http://localhost:5173\nLOCAL_POSTGRES_URI=postgres://postgres:passw0rd@127.0.0.1:5432/macodeploy?sslmode=disable\n' "$$VCAP_SERVICES" "$$VCAP_APP" > .env && \
	echo ">> .env written successfully (credentials from service keys '$(SK_NAME)')"

_check-cf:
	@cf target >/dev/null 2>&1 || (echo "ERROR: cf CLI not logged in. Run: cf login -a <api> -o <org> -s <space>" && exit 1)
	@echo ">> CF target: $$(cf target | grep -E 'org:|space:' | tr '\n' ' ')"

_ensure-keys:
	@echo ">> ensuring service keys '$(SK_NAME)' exist (creates if missing)..."
	@cf service-key $(SVC_UAA) $(SK_NAME) >/dev/null 2>&1 || cf create-service-key $(SVC_UAA) $(SK_NAME)
	@cf service-key $(SVC_UAA_API) $(SK_NAME) >/dev/null 2>&1 || cf create-service-key $(SVC_UAA_API) $(SK_NAME)
	@cf service-key $(SVC_DB) $(SK_NAME) >/dev/null 2>&1 || cf create-service-key $(SVC_DB) $(SK_NAME)
	@cf service-key $(SVC_DEST) $(SK_NAME) >/dev/null 2>&1 || cf create-service-key $(SVC_DEST) $(SK_NAME)
	@cf service-key $(SVC_CONN) $(SK_NAME) >/dev/null 2>&1 || cf create-service-key $(SVC_CONN) $(SK_NAME)

.PHONY: all fmt build clean prepare test sync-env _check-cf _ensure-keys
