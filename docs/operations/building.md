# Building

All commands run in `ai-console/`.

## Make targets

| Target | What it does |
|---|---|
| `make kg` | Copies the control-hub knowledge graph (`*.md` from `.knowledgegraph/` at the repository root, or from the main checkout when you are in a worktree) into `./knowledgegraph`. Fails if neither exists. |
| `make build` | `go build ./...` and `go vet ./...`. |
| `make test` | `go test ./...` (unit and integration tests with a fake control-hub and a fake Claude API). |
| `make web` | `npm ci` and `npm run build` in `web/` (type-check and bundle into `web/dist`). |
| `make run` | `make kg`, then runs the server locally with `KG_DIR=./knowledgegraph STATIC_DIR=./web/dist COOKIE_SECURE=false`. See [Running locally](../development/local.md). |
| `make mcp` | Builds `bin/control-hub-mcp`, the [MCP server](../development/mcp.md). |
| `make image` | `make kg`, then `docker build --platform linux/amd64 -t $(IMAGE):$(TAG) .` |
| `make push` | `make image`, then `docker push $(IMAGE):$(TAG)`. |
| `make eval` | `make kg`, then runs the Copilot evaluation (costs tokens; needs `ANTHROPIC_API_KEY`). See [Tests and evals](../development/testing.md). |
| `make docs` | `mkdocs serve`: live preview of this manual on http://127.0.0.1:8000. Needs `mkdocs-material==9.5.39`. |
| `make docs-build` | `mkdocs build --strict -d site`: the static manual in `./site`, which `make run` serves at `/docs/`. See [Working on these docs](../development/docs.md). |

Variables:

| Variable | Default |
|---|---|
| `IMAGE` | `gcr.io/prophecy-cloud/control-hub-ai-console` |
| `TAG` | `dev` |
| `EVAL_ARGS` | empty; passed to the eval runner (for example `-only pg16-public-schema`) |

## Verify a change

```bash
make build test            # Go: vet, unit and integration tests
cd web && npm run build    # UI: type-check and bundle
```

!!! warning
    `tsc --noEmit` alone is not a check in `web/`; it always passes. Use `npm run build`.

## The image

The `Dockerfile` has four stages:

1. **web** (`node:22-alpine`): `npm ci` and `npm run build`.
2. **docs** (`python:3.12-alpine`): installs `mkdocs-material==9.5.39` and runs `mkdocs build --strict -d /site`. A
   warning in the docs (for example a broken link) fails the image build.
3. **go** (`golang:1.26-alpine`): builds `ai-console` and `control-hub-mcp` as static binaries (`CGO_ENABLED=0`,
   `-trimpath`). SQLite uses a pure-Go driver, so no cgo is needed.
4. **runtime** (`gcr.io/distroless/static-debian12:nonroot`): CA certificates (for the Claude API) and no shell.

The runtime image contains:

| Path | Contents |
|---|---|
| `/app/ai-console` | The server (the entrypoint) |
| `/app/control-hub-mcp` | The MCP server |
| `/app/web` | The built UI (`STATIC_DIR`) |
| `/app/docs` | This manual, built with MkDocs (`DOCS_DIR`), served at `/docs/` |
| `/app/knowledgegraph` | The knowledge graph (`KG_DIR`) |

It listens on `:8090` (`LISTEN_ADDR`) and runs as `nonroot:nonroot`.

The build context is `ai-console/`. Always build with `make image` or `make push`, not plain `docker build`, so the
knowledge graph is copied in first.

## Build and push

```bash
cd ai-console
make push TAG=<tag>
```

`make push` builds for `linux/amd64` even on an Apple Silicon Mac.

!!! tip "Reusing a tag"
    While testing, reusing one tag (for example `TAG=ai-console-test`) and running `kubectl rollout restart` is
    enough to roll out a new build, as long as the container has `imagePullPolicy: Always` (the current deployment
    does). For releases, use a new tag and update the Deployment's image.
