[![REUSE status](https://api.reuse.software/badge/github.com/SAP/cloud-integration-delivery-orchestrator-srv)](https://api.reuse.software/info/github.com/SAP/cloud-integration-delivery-orchestrator-srv)

# cloud-integration-delivery-orchestrator-srv

## About this project

Go backend service for SAP Cloud Integration multi-tenant delivery orchestration — provides the REST API, WebSocket push, and XSUAA authentication, compiled into the published Docker image.

This is one of the source repositories behind the [Cloud Integration Delivery Orchestrator](https://github.com/SAP/cloud-integration-delivery-orchestrator) (deployment & docs). The companion frontend lives in [cloud-integration-delivery-orchestrator-ui](https://github.com/SAP/cloud-integration-delivery-orchestrator-ui); its build output is embedded into this service's Docker image at release time.

## Architecture

A single Go binary (Gin HTTP server on `:8080`) that also serves the embedded web UI.

- **Layers**: `handler` (HTTP routing, RBAC scope checks) → `service` (delivery orchestration, transport & sync logic) → `db` (GORM models, PostgreSQL).
- **`pkg/`** holds the integration clients and cross-cutting concerns: `cpi` (CPI OData), `tms` (Transport Management), `cas` (Content Agent Service), `cf` (Cloud Foundry), `xsuaa`/`auth` (XSUAA login + scope-based RBAC), `notify` (WebSocket push), `lifecycle` (state machines), `otel` (observability), `errcode`, `env`, `github`.
- **Frontend embedding**: in production the compiled UI is served from `web.DistFS` (`go:embed`). In development, when `VITE_DEV_URL` is set, the server reverse-proxies non-API routes to the Vite dev server so HMR keeps working while XSUAA session handling stays identical to production.
- **Auth**: routes are guarded by scope suffix (e.g. `DeliveryRequest.Operate`), so the XSUAA `xsappname` can change without touching route code.

## Local Development

Requirements: Go 1.25+, a container runtime (Podman or Docker) for local PostgreSQL, the `cf` CLI, and `jq`.

Local debugging is driven by the `Makefile`:

```bash
make run-db      # start a local PostgreSQL container (one-time)
make sync-env    # pull VCAP_SERVICES from a deployed CF app into .env (re-run after each cf deploy)
make run         # build and start the backend on http://localhost:8080 (loads .env)
```

`make sync-env` requires the `cf` CLI to be logged in and targeting the org/space of a deployed app; it writes a `.env` file with `VCAP_SERVICES`, `VCAP_APPLICATION`, `LOCAL_POSTGRES_URI`, and `VITE_DEV_URL`. To debug the frontend alongside the backend, run the UI's Vite dev server (`npm run dev`, port 5173) — the backend proxies to it via `VITE_DEV_URL`.

Other targets: `make setup-vscode` (generates a VS Code Go debug launch config), `make fmt`, `make test`, `make build`, `make clean`. See the `Makefile` header comments for details and overridable variables.

## Support, Feedback, Contributing

This project is open to feature requests/suggestions, bug reports etc. via [GitHub issues](https://github.com/SAP/cloud-integration-delivery-orchestrator-srv/issues). Contribution and feedback are encouraged and always welcome. For more information about how to contribute, the project structure, as well as additional contribution information, see our [Contribution Guidelines](CONTRIBUTING.md).

## Security / Disclosure
If you find any bug that may be a security problem, please follow our instructions at [in our security policy](https://github.com/SAP/cloud-integration-delivery-orchestrator-srv/security/policy) on how to report it. Please do not create GitHub issues for security-related doubts or problems.

## Code of Conduct

We as members, contributors, and leaders pledge to make participation in our community a harassment-free experience for everyone. By participating in this project, you agree to abide by its [Code of Conduct](https://github.com/SAP/.github/blob/main/CODE_OF_CONDUCT.md) at all times.

## Licensing

Copyright 2026 SAP SE or an SAP affiliate company and cloud-integration-delivery-orchestrator-srv contributors. Please see our [LICENSE](LICENSE) for copyright and license information. Detailed information including third-party components and their licensing/copyright information is available [via the REUSE tool](https://api.reuse.software/info/github.com/SAP/cloud-integration-delivery-orchestrator-srv).
