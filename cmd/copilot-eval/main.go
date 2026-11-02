// Command copilot-eval runs the Copilot on the recorded cases in evals/cases
// and writes a Markdown report. Tool calls are replayed from each case, so
// only the Claude API is called (ANTHROPIC_API_KEY). Every run costs tokens:
// run it when the prompt, the tools or the model change.
//
//	go run ./cmd/copilot-eval -cases evals/cases -out evals/report.md
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/athena"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/eval"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

func main() {
	casesDir := flag.String("cases", "evals/cases", "directory of case YAML files")
	only := flag.String("only", "", "comma-separated case IDs to run")
	model := flag.String("model", "claude-opus-5-5", "Claude model")
	effort := flag.String("effort", "high", "reasoning effort")
	kgDir := flag.String("kg", "knowledgegraph", "knowledge-graph directory (make kg)")
	parallel := flag.Int("parallel", 3, "cases run at once")
	out := flag.String("out", "evals/report.md", "Markdown report path (a .json is written next to it)")
	flag.Parse()

	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		log.Fatal("set ANTHROPIC_API_KEY")
	}
	cases, err := eval.Load(*casesDir)
	if err != nil {
		log.Fatal(err)
	}
	if *only != "" {
		want := map[string]bool{}
		for _, id := range strings.Split(*only, ",") {
			want[strings.TrimSpace(id)] = true
		}
		var kept []eval.Case
		for _, c := range cases {
			if want[c.ID] {
				kept = append(kept, c)
			}
		}
		cases = kept
	}
	if len(cases) == 0 {
		log.Fatal("no cases")
	}
	kg := tools.LoadKnowledgeGraph(*kgDir)
	if kg == nil {
		log.Printf("no knowledge graph in %s; running without it (make kg)", *kgDir)
	}

	results := make([]eval.Result, len(cases))
	sem := make(chan struct{}, max(1, *parallel))
	var wg sync.WaitGroup
	for i, c := range cases {
		wg.Add(1)
		go func(i int, c eval.Case) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = eval.Run(context.Background(), c, NewAgent(kg, *model, *effort), key)
			status := "pass"
			if !results[i].Pass {
				status = "FAIL " + strings.Join(results[i].Failures, "; ") + results[i].Error
			}
			log.Printf("%-28s %s (%d tool calls)", c.ID, status, len(results[i].ToolCalls))
		}(i, c)
	}
	wg.Wait()

	report := eval.Report(results, *model, *effort)
	if err := os.WriteFile(*out, []byte(report), 0o644); err != nil {
		log.Fatal(err)
	}
	raw, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(strings.TrimSuffix(*out, ".md")+".json", raw, 0o644)
	fmt.Println(strings.SplitN(report, "\n\n", 4)[2])
	fmt.Println("report:", *out)
}

// NewAgent builds an agent with every tool registered. Tools never run: the
// case replays their outputs. Their dependencies are placeholders.
func NewAgent(kg *tools.KnowledgeGraph, model, effort string) eval.NewAgent {
	return func(st *store.Store) *agent.Agent {
		none := func(context.Context, string) (string, error) { return "", nil }
		return agent.New(st, controlhub.New("http://127.0.0.1:1"), kg, nil, model, effort).
			WithReadOnly(true).
			WithLogs(&tools.LogReader{}).
			WithMonitoring(grafana.NewHub(), none).
			WithCertificates(func(context.Context, string, bool) (string, error) { return "", nil }).
			WithCosts(func(context.Context, string, string, string, int) (string, error) { return "", nil }).
			WithKube(func(context.Context, string, string) (*kube.Cluster, error) { return nil, nil }).
			WithAthena(athena.New("eval"))
	}
}
