package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// KnowledgeGraph serves the control-hub knowledge base (.knowledgegraph/)
// shipped in the image. INDEX.md goes into the system prompt; nodes are read
// on demand.
type KnowledgeGraph struct {
	dir   string
	nodes map[string]bool
}

// LoadKnowledgeGraph returns nil when the directory has no INDEX.md, which
// disables the tools rather than failing startup.
func LoadKnowledgeGraph(dir string) *KnowledgeGraph {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	kg := &KnowledgeGraph{dir: dir, nodes: map[string]bool{}}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			kg.nodes[e.Name()] = true
		}
	}
	if !kg.nodes["INDEX.md"] {
		return nil
	}
	return kg
}

func (kg *KnowledgeGraph) Index() string {
	raw, _ := os.ReadFile(filepath.Join(kg.dir, "INDEX.md"))
	return string(raw)
}

func (kg *KnowledgeGraph) Nodes() []string {
	out := make([]string, 0, len(kg.nodes))
	for n := range kg.nodes {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (kg *KnowledgeGraph) Read(name string) (string, error) {
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	if !kg.nodes[name] { // only files that exist in the directory, so no path traversal
		return "", fmt.Errorf("unknown node %q; available: %s", name, strings.Join(kg.Nodes(), ", "))
	}
	raw, err := os.ReadFile(filepath.Join(kg.dir, name))
	return string(raw), err
}

func kgTools() []Tool {
	return []Tool{{
		Name: "read_knowledge_node",
		Description: "Read one node of the control-hub knowledge base (how control-hub works: create/delete flows, " +
			"networking/DNS/certs, Helm, cloud providers, known gotchas). INDEX.md in your instructions says which node " +
			"answers which question. Read the node before guessing how a step works.",
		Properties: map[string]any{"node": str("File name, e.g. \"08-networking-dns-certs.md\".")},
		Required:   []string{"node"},
		Label:      "Consulted the platform notes",
		Run: func(_ context.Context, env *Env, input json.RawMessage) (string, error) {
			in, err := decode[struct {
				Node string `json:"node"`
			}](input)
			if err != nil {
				return "", err
			}
			return env.KG.Read(in.Node)
		},
	}}
}
