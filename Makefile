# Local development Makefile for the cpi-delivery backend.
# Helps bootstrap and run a local debug/dev environment.
# Deployment itself is orchestrated by cpi-delivery-product.

# --- Toolchain ---
GO           ?= go
GOFMT        ?= $(GO)fmt
DOCKER       ?= podman  # podman, docker

# --- Build output ---
BIN_DIR      ?= $(shell pwd)/build
BIN_NAME     ?= cpi-delivery

# --- Local runtime ---
ENV_FILE     ?= .env
VSCODE_DIR   ?= .vscode

# --- Local PostgreSQL ---
DB_CONTAINER ?= cpi-delivery-db
DB_IMAGE     ?= postgres:15
DB_NAME      ?= cpidelivery
DB_VOLUME    ?= cpi-delivery-pgdata

# --- Cloud Foundry (sync-env) ---
CF_APP       ?= cpi-delivery

.PHONY: all fmt build clean test run run-db setup-vscode sync-env _check-cf

all: fmt build

build:
	@echo ">> building $(BIN_NAME)"
	$(GO) build -o $(BIN_DIR)/$(BIN_NAME)

fmt:
	@echo ">> format code style"
	$(GOFMT) -w $$(find . -path ./.history -prune -o -name '*.go' -print)

clean:
	rm -rf $(BIN_DIR)

test:
	$(GO) test ./... -v

# --- run: build and start the backend locally on :8080 ---
# Loads environment from $(ENV_FILE) (create it first with `make sync-env`).
# The app reads VCAP_SERVICES/VCAP_APPLICATION + LOCAL_POSTGRES_URI from the env.
run: build
	@test -f $(ENV_FILE) || { echo "ERROR: $(ENV_FILE) not found. Run 'make sync-env' first."; exit 1; }
	@echo ">> starting $(BIN_NAME) on http://localhost:8080 (env: $(ENV_FILE))"
	@while IFS= read -r line; do \
		case "$$line" in ''|\#*) continue ;; esac; \
		export "$$line"; \
	done < $(ENV_FILE); \
	$(BIN_DIR)/$(BIN_NAME)

# --- run-db: start local PostgreSQL (one-time setup) ---
# Data persists in named volume "$(DB_VOLUME)" across container restarts.
# Idempotent: skips if container already exists.
# Use DOCKER=podman if using Podman. Override the DB name with DB_NAME=...
run-db:
	@if $(DOCKER) ps -a --format '{{.Names}}' | grep -q '^$(DB_CONTAINER)$$'; then \
		$(DOCKER) start $(DB_CONTAINER) 2>/dev/null || true; \
		echo ">> $(DB_CONTAINER) already exists, started"; \
	else \
		$(DOCKER) run -d --name $(DB_CONTAINER) \
			-e POSTGRES_USER=postgres \
			-e POSTGRES_PASSWORD=passw0rd \
			-e POSTGRES_DB=$(DB_NAME) \
			-p 5432:5432 \
			-v $(DB_VOLUME):/var/lib/postgresql/data \
			--restart=unless-stopped \
			$(DB_IMAGE) && \
		echo ">> $(DB_CONTAINER) created and started"; \
	fi

# --- setup-vscode: generate $(VSCODE_DIR)/launch.json for Go debugging ---
# Non-destructive: an existing launch.json is left untouched.
setup-vscode:
	@mkdir -p $(VSCODE_DIR)
	@if [ -f $(VSCODE_DIR)/launch.json ]; then \
		echo ">> $(VSCODE_DIR)/launch.json already exists, leaving it untouched"; \
	else \
		printf '%s\n' \
			'{' \
			'    "version": "0.2.0",' \
			'    "configurations": [' \
			'        {' \
			'            "name": "Debug cpi-delivery",' \
			'            "type": "go",' \
			'            "request": "launch",' \
			'            "mode": "debug",' \
			'            "program": "$${workspaceFolder}/main.go",' \
			'            "envFile": "$${workspaceFolder}/$(ENV_FILE)"' \
			'        }' \
			'    ]' \
			'}' > $(VSCODE_DIR)/launch.json; \
		echo ">> wrote $(VSCODE_DIR)/launch.json"; \
	fi

# --- sync-env: pull VCAP_SERVICES from deployed CF app and write $(ENV_FILE) ---
#
# Prerequisites:
#   - cf CLI logged in and targeting the correct org/space
#   - jq installed
#   - App must be deployed (pulls env from running app)
#
# What it does:
#   1. Checks CF CLI login status
#   2. Fetches VCAP_SERVICES and VCAP_APPLICATION from the deployed app via CF API
#   3. Writes $(ENV_FILE) (format compatible with VS Code launch.json envFile)
#
# Note: credentials change on each cf deploy/restage. Re-run after deploy.
#
# Usage:
#   make sync-env                        # uses default app name
#   make sync-env CF_APP=my-app-name     # override app name
sync-env: _check-cf
	@echo ">> pulling env from deployed app '$(CF_APP)'..."
	@APP_GUID=$$(cf app $(CF_APP) --guid) && \
	cf curl /v3/apps/$$APP_GUID/env > /tmp/.cpi-delivery-env.json && \
	VCAP_SERVICES=$$(jq -c '.system_env_json.VCAP_SERVICES' /tmp/.cpi-delivery-env.json) && \
	VCAP_APP=$$(jq -c '.application_env_json.VCAP_APPLICATION | del(.application_uris) | del(.uris)' /tmp/.cpi-delivery-env.json) && \
	printf 'VCAP_SERVICES=%s\nVCAP_APPLICATION=%s\nVITE_DEV_URL=http://localhost:5173\nOTEL_SDK_DISABLED=true\nLOCAL_POSTGRES_URI=postgres://postgres:passw0rd@127.0.0.1:5432/$(DB_NAME)?sslmode=disable\n' "$$VCAP_SERVICES" "$$VCAP_APP" > $(ENV_FILE) && \
	rm -f /tmp/.cpi-delivery-env.json && \
	echo ">> $(ENV_FILE) written successfully (from app '$(CF_APP)')"

_check-cf:
	@cf target >/dev/null 2>&1 || (echo "ERROR: cf CLI not logged in. Run: cf login -a <api> -o <org> -s <space>" && exit 1)
	@echo ">> CF target: $$(cf target | grep -E 'org:|space:' | tr '\n' ' ')"
