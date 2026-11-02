// Command control-hub-mcp exposes the AI console's read-only control-hub
// tools as an MCP server over stdio, so Claude Code (or any MCP client) can
// investigate control-hub with the same tools the Copilot uses.
//
// Environment:
//
//	CONTROLHUB_URL    control-hub base URL (default http://localhost:8083)
//	CONTROLHUB_TOKEN  your control-hub token (required); control-hub enforces its permissions
//	KG_DIR            optional path to control-hub's .knowledgegraph directory
//	GRAFANA_<NAME>_URL / GRAFANA_<NAME>_TOKEN  optional Grafana instances for metric and log queries
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/athena"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

func main() {
	log.SetOutput(os.Stderr) // stdout carries the MCP protocol
	token := os.Getenv("CONTROLHUB_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "control-hub-mcp: CONTROLHUB_TOKEN is required")
		os.Exit(2)
	}
	baseURL := os.Getenv("CONTROLHUB_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8083"
	}

	env := &tools.Env{CH: controlhub.New(baseURL), Token: token, Probe: tools.DefaultProbePolicy()}
	if t := os.Getenv("ATHENA_AUTH_TOKEN"); t != "" {
		env.Athena = athena.New(t)
	}
	if dir := os.Getenv("KG_DIR"); dir != "" {
		if env.KG = tools.LoadKnowledgeGraph(dir); env.KG == nil {
			log.Printf("no INDEX.md in %s; knowledge-graph tool disabled", dir)
		}
	}

	hub := grafana.NewHub()
	hub.FromEnv() // GRAFANA_<NAME>_URL + GRAFANA_<NAME>_TOKEN enable metric and log queries
	if len(hub.Clients()) > 0 {
		hub.Refresh(context.Background())
		env.Grafana = hub
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "control-hub-mcp", Title: "Control Hub", Version: "0.2.0"}, nil)
	// Everything is read-only except the two certificate tools, which control-hub
	// allows for admins only and which the MCP client confirms per call.
	for _, tl := range append(tools.Registry(env), tools.CertRepairTool(), tools.WildcardRenewTool()) {
		register(server, env, tl)
	}
	log.Printf("control-hub-mcp: serving over stdio, target=%s", baseURL)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("control-hub-mcp: %v", err)
	}
}

func register(server *mcp.Server, env *tools.Env, tl tools.Tool) {
	notDestructive, openWorld := false, true
	schema := map[string]any{"type": "object", "properties": tl.Properties}
	if len(tl.Required) > 0 {
		schema["required"] = tl.Required
	}
	server.AddTool(&mcp.Tool{
		Name:        tl.Name,
		Description: tl.Description,
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{Title: tl.Label, ReadOnlyHint: !tl.Writes, IdempotentHint: tl.Writes,
			DestructiveHint: &notDestructive, OpenWorldHint: &openWorld},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		out, err := tl.Run(ctx, env, json.RawMessage(req.Params.Arguments))
		if err != nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}, IsError: true}, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil
	})
}
