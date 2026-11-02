# Working on these docs

This site is MkDocs with the Material theme. Sources are `ai-console/docs/*.md`, configured by `ai-console/mkdocs.yml`.
The console serves the built site at `/docs/`.

## Preview locally

You need `mkdocs-material==9.5.39`. Use a virtual environment outside the repository:

```bash
python3 -m venv ~/.venvs/mkdocs
~/.venvs/mkdocs/bin/pip install mkdocs-material==9.5.39
export PATH="$HOME/.venvs/mkdocs/bin:$PATH"
```

Then, in `ai-console/`:

| Command | What it does |
|---|---|
| `make docs` | `mkdocs serve`: live preview with reload on http://127.0.0.1:8000. |
| `make docs-build` | `mkdocs build --strict -d site`: the static site in `./site` (git-ignored). Fails on any warning, as the image build does. |

To see the docs as the console serves them, run `make docs-build`, then `make run`, sign in, and open
http://localhost:8090/docs/. The server reads the site from `DOCS_DIR` (default `./site`).

## How it is served

- **Build:** the Dockerfile's `docs` stage (`python:3.12-alpine`) installs `mkdocs-material==9.5.39` and runs
  `mkdocs build --strict -d /site`. The final image copies it to `/app/docs` and sets `DOCS_DIR=/app/docs`. A warning
  in the docs fails the image build.
- **Route:** `/docs` redirects to `/docs/`, and everything under `/docs/` is served by `docsHandler`
  (`internal/server/server.go`).
- **Signed-in users only:** a request without a valid console session is redirected to `/login?next=<the docs URL>`,
  and back after sign-in.
- **Directories** are served only through their `index.html`; there are no directory listings, and a path that does
  not exist is a `404`. That is why the default `use_directory_urls: true` works: `/docs/concepts/architecture/` serves
  `concepts/architecture/index.html`.
- Responses are sent with `Cache-Control: no-cache`.
- **Links in the UI:** the sidebar's **Documentation** entry and the command palette's "Documentation" page open
  `/docs/` in a new tab.

Deploying new docs is a normal image rollout ([Upgrading](../operations/upgrading.md)).

## Writing

- Keep every link relative (`../concepts/security.md`); the site is served under `/docs/` and `site_url` is empty.
- Get facts from the code: environment variables from `internal/config/config.go`, routes from `internal/server`,
  tool names from `internal/tools` and `internal/agent`.
- Never include secret values; use placeholders such as `<CONTROLHUB_ADMIN_TOKEN>`.
- Diagrams use Mermaid fences (` ```mermaid `).
- Add new pages to `nav` in `mkdocs.yml`; `--strict` reports pages missing from it only as information, but broken
  links fail the build.
