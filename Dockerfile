# Build context is this directory (ai-console/). Run `make image`, which first
# copies control-hub's .knowledgegraph into ./knowledgegraph.

FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# The documentation site (docs/*.md → static HTML), served at /docs/.
FROM python:3.12-alpine AS docs
RUN pip install --no-cache-dir --retries 10 --timeout 120 mkdocs-material==9.5.39
WORKDIR /docs
COPY mkdocs.yml ./
COPY docs/ docs/
RUN mkdocs build --strict -d /site

FROM golang:1.26-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/ai-console ./cmd/ai-console && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/control-hub-mcp ./cmd/control-hub-mcp

# distroless/static ships CA certificates (for the Claude API) and no shell.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/ /app/
COPY --from=web /web/dist /app/web
COPY --from=docs /site /app/docs
COPY knowledgegraph/ /app/knowledgegraph/
ENV LISTEN_ADDR=:8090 \
    STATIC_DIR=/app/web \
    DOCS_DIR=/app/docs \
    KG_DIR=/app/knowledgegraph
EXPOSE 8090
USER nonroot:nonroot
ENTRYPOINT ["/app/ai-console"]
