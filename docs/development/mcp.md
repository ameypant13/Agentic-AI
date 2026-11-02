# MCP server for Claude Code

`cmd/control-hub-mcp` serves the Copilot's read-only tools as an MCP server over stdio, so you can use them from
Claude Code on your own machine. It runs with **your** control-hub token.

## What it serves

- Every tool from `tools.Registry` whose dependency is configured: the control-hub tools, the network tools, the
  knowledge graph (with `KG_DIR`), `athena_read` (with `ATHENA_AUTH_TOKEN`) and the Grafana tools (with
  `GRAFANA_<NAME>_URL` / `_TOKEN`).
- Two write tools that the Copilot never gets: `repair_certificate` (restore the DNS records a certificate's
  automatic renewal needs) and `renew_wildcard_certificate` (renew `*.app.prophecy.ai` in ACM and sync it). control-hub
  allows them for admins only; run them as a dry run first. Claude Code asks you to confirm each call.

It has no control-hub log search (that needs the in-cluster service account), no cluster (`k8s_*`) tools, and none of
the console's own data (issues, certificates inventory, costs, learnings).

Every tool is annotated read-only except the two certificate tools.

## Set it up

1. Build it:

    ```bash
    cd ai-console
    make mcp           # → bin/control-hub-mcp
    ```

2. Port-forward control-hub, or use any control-hub URL you can reach:

    ```bash
    kubectl -n control-hub-dev port-forward svc/control-hub 8003:8003
    ```

3. Register it with Claude Code:

    ```bash
    claude mcp add control-hub -- env CONTROLHUB_URL=http://localhost:8003 \
      CONTROLHUB_TOKEN=<your control-hub token> KG_DIR=<repo>/.knowledgegraph \
      $(pwd)/bin/control-hub-mcp
    ```

Without `CONTROLHUB_URL` it defaults to `http://localhost:8083`; set it explicitly.

## Environment

| Variable | Meaning |
|---|---|
| `CONTROLHUB_TOKEN` | Required. Your control-hub token. |
| `CONTROLHUB_URL` | control-hub's base URL (default `http://localhost:8083`). |
| `KG_DIR` | Knowledge graph directory with `INDEX.md`. |
| `ATHENA_AUTH_TOKEN` | Enables `athena_read`. |
| `GRAFANA_<NAME>_URL`, `GRAFANA_<NAME>_TOKEN` | Enable the Grafana tools. |

Logs go to stderr; stdout carries the MCP protocol.
