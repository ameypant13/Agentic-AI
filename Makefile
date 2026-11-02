IMAGE ?= gcr.io/prophecy-cloud/control-hub-ai-console
TAG   ?= dev

# The knowledge graph lives at the control-hub repo root. It is untracked for
# now, so in a worktree fall back to the main checkout.
REPO_ROOT := $(shell git rev-parse --show-toplevel)
MAIN_ROOT := $(shell dirname "$$(git rev-parse --path-format=absolute --git-common-dir)")
KG_SRC    := $(firstword $(wildcard $(REPO_ROOT)/.knowledgegraph $(MAIN_ROOT)/.knowledgegraph))

.PHONY: kg build test web run image push mcp eval docs docs-build

kg:
	@test -n "$(KG_SRC)" || (echo "no .knowledgegraph found in $(REPO_ROOT) or $(MAIN_ROOT)" && exit 1)
	rm -rf knowledgegraph && mkdir -p knowledgegraph
	cp $(KG_SRC)/*.md knowledgegraph/
	@echo "copied knowledge graph from $(KG_SRC)"

build:
	go build ./...
	go vet ./...

test:
	go test ./...

web:
	cd web && npm ci && npm run build

# Documentation site: live preview on http://127.0.0.1:8000, or a static build in ./site
# (served at /docs/ by `make run`). Needs `pip install mkdocs-material==9.5.39`.
docs:
	mkdocs serve

docs-build:
	mkdocs build --strict -d site

# Local run against a port-forwarded control-hub; see README.
run: kg
	KG_DIR=./knowledgegraph STATIC_DIR=./web/dist COOKIE_SECURE=false go run ./cmd/ai-console

mcp:
	go build -o bin/control-hub-mcp ./cmd/control-hub-mcp

image: kg
	docker build --platform linux/amd64 -t $(IMAGE):$(TAG) .

push: image
	docker push $(IMAGE):$(TAG)

# Runs the Copilot on evals/cases against Claude (costs tokens; needs ANTHROPIC_API_KEY).
eval: kg
	go run ./cmd/copilot-eval -cases evals/cases -kg knowledgegraph -out evals/report.md $(EVAL_ARGS)
