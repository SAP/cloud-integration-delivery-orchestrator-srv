## Local Development

### Prerequisites

- Go 1.24+ and `make` installed
- Docker or Podman (for local PostgreSQL)
- CF CLI logged in (for `make sync-env`)
- `jq` installed (for `make sync-env`)
- Configure `GOPROXY` if needed: `export GOPROXY=https://goproxy.cn`

### 1. Start PostgreSQL locally

```bash
make run-db

# If using Podman:
make run-db DOCKER=podman
```

Idempotent — if the container already exists, it just starts it. Data persists in a named Docker volume.

### 2. Generate `.env` from CF service keys

The application reads `VCAP_SERVICES` environment variable at startup (via `go-cfenv`).
For local development, VS Code loads `.env` via `launch.json`.

To generate a stable `.env` (credentials survive CF redeploys):

```bash
make sync-env
```

This creates CF service keys (once) and assembles `.env` from their credentials.
Service keys are independent of app bindings — they don't change when you `cf deploy`.

### 3. Run with VS Code (recommended)

The project uses `.vscode/launch.json` with `envFile` pointing to `.env`:

```jsonc
// .vscode/launch.json (already configured)
{
  "configurations": [
    {
      "name": "Launch Package",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/main.go",
      "envFile": "${workspaceFolder}/.env"
    }
  ]
}
```

Press F5 to start with debugger. The application listens at `0.0.0.0:9000`.

### 4. Frontend (optional)

If developing frontend locally alongside backend:

```bash
# In mmt-devops-ui-cpi-delivery/
npm run dev
```

Vite dev server starts at `http://localhost:5173`, proxied through Go backend via `VITE_DEV_URL` in `.env`.

---

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Compile Go binary (verify compilation) |
| `make fmt` | Format all Go source files |
| `make test` | Run all tests |
| `make run-db` | Start local PostgreSQL (one-time, idempotent) |
| `make sync-env` | Generate `.env` from CF service keys (stable credentials) |
| `make clean` | Remove build artifacts |

Use `DOCKER=podman` with any target that uses containers (e.g., `make run-db DOCKER=podman`).

---

## Connect to Remote DB Locally

PostgreSQL on CF cannot be connected to directly. Use CF SSH tunnel:

```bash
cf ssh -L localhost:8866:<db-hostname>:<db-port> cpi-delivery -N
```

Then connect to `localhost:8866` with the credentials from your service key.
