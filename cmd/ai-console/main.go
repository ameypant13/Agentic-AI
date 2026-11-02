// Command ai-console serves the Control Hub AI Console: the web app, its API,
// the Copilot, and the background monitors.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/agent"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/athena"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/audit"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/certs"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/config"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/cost"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/grafana"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/kube"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/metricrules"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/monitor"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/parser"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/patterns"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/server"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	st, err := store.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	box, err := secret.NewBox(cfg.EncryptionKey)
	if err != nil {
		log.Fatalf("encryption: %v", err)
	}
	// The service tokens are read-only: the client refuses writes with them.
	ch := controlhub.New(cfg.ControlHubURL).WithReadOnlyTokens(cfg.ServiceToken, cfg.DevServiceToken)

	kg := tools.LoadKnowledgeGraph(cfg.KnowledgeGraphDir)
	if kg == nil {
		log.Printf("knowledge graph not found in %s; the Copilot will run without it", cfg.KnowledgeGraphDir)
	}
	logs := tools.NewInClusterLogReader(cfg.ControlHubLogsNamespace, cfg.ControlHubPodSelector)
	if logs == nil {
		log.Printf("control-hub log search is off (not in a cluster, or CONTROLHUB_LOGS_NAMESPACE empty)")
	}
	lib, err := parser.NewLibrary(st)
	if err != nil {
		log.Fatalf("log rules: %v", err)
	}
	ag := agent.New(st, ch, kg, logs, cfg.Model, cfg.Effort).WithReadOnly(cfg.ReadOnly)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	registry := kube.NewRegistry(kube.CredentialsFromEnv())
	worker, err := parser.NewWorker(parser.WorkerConfig{Dir: cfg.DataDir, Interval: cfg.LogScanInterval,
		Namespaces: cfg.LogScanNamespaces, RetentionDays: cfg.HitRetentionDays, PatternIgnore: cfg.PatternIgnoreServices}, lib, st, registry)
	if err != nil {
		log.Fatalf("log worker: %v", err)
	}
	hub := grafana.NewHub()
	hub.FromEnv()
	if n := hub.MappingsFromEnv(); n > 0 {
		log.Printf("grafana: %d cluster mappings from GRAFANA_MAPPINGS", n)
	}
	if err := server.LoadGrafanaSettings(st, box, hub); err != nil {
		log.Printf("grafana settings: %v", err)
	}
	hub.Start(ctx, 10*time.Minute)
	miner := patterns.NewMiner()
	miner.Load(st)
	worker.WithGrafana(hub).WithPatterns(miner).Start(ctx)
	metrics, err := metricrules.NewWorker(hub, filepath.Join(cfg.DataDir, "metrics"), cfg.LogScanInterval)
	if err != nil {
		log.Fatalf("metric rules: %v", err)
	}
	metrics.Start(ctx)
	ag.WithMonitoring(hub, server.IssuesFunc(hub, metrics, worker))
	ag.WithKube(server.KubeAccess(ch, registry, hub))
	if cfg.AthenaToken != "" {
		ag.WithAthena(athena.New(cfg.AthenaToken))
	} else {
		log.Printf("athena: ATHENA_AUTH_TOKEN not set; athena_read is off")
	}
	if len(cfg.AuditGCPProjects) > 0 {
		if s, err := audit.New(ctx, cfg.AuditGCPProjects); err != nil {
			log.Printf("audit: off: %v", err)
		} else {
			ag.WithAudit(s)
		}
	}
	certWorker, err := certs.NewWorker(certs.RealSources(certs.Options{CH: ch, Token: cfg.ServiceToken, Realms: cfg.Realms,
		SecretNamespaces: cfg.CertSecretNamespaces, ACMRegions: cfg.CertACMRegions,
		AWSKeyID: os.Getenv("AWS_PROD_ACCESS_KEY_ID"), AWSSecret: os.Getenv("AWS_PROD_SECRET_ACCESS_KEY"),
		GCPProjectFilter: cfg.CertGCPProjectFilter}), filepath.Join(cfg.DataDir, "certs"), cfg.CertScanInterval)
	if err != nil {
		log.Fatalf("certificates: %v", err)
	}
	certWorker.Start(ctx)
	ag.WithCertificates(func(_ context.Context, q string, problemsOnly bool) (string, error) {
		return certWorker.Describe(q, problemsOnly)
	})
	var costWorker *cost.Worker
	if cfg.CostEnabled {
		prices := cost.NewPrices(filepath.Join(cfg.DataDir, "cost"), os.Getenv("AWS_PROD_ACCESS_KEY_ID"), os.Getenv("AWS_PROD_SECRET_ACCESS_KEY"))
		costWorker = cost.NewWorker(cost.Config{BackfillDays: cfg.CostBackfillDays, RetentionDays: cfg.CostRetentionDays}, hub, st, prices)
		server.CostSources(ch, cfg.ServiceToken, hub, registry, costWorker)
		costWorker.Start(ctx)
		ag.WithCosts(server.CostsFunc(st))
	}
	if cfg.MonitorEnabled {
		monitor.New(cfg, st, ch).WithLogScan(monitor.LogScan{Worker: worker}).Start(ctx)
	}

	app := server.New(cfg, server.Deps{Store: st, CH: ch, Box: box, Agent: ag, Kube: registry, Rules: lib,
		LogWork: worker, Grafana: hub, Metrics: metrics, Certs: certWorker, Costs: costWorker})
	app.StartDigest(ctx)
	app.StartReviewPoller(ctx)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: chat replies stream for minutes.
	}
	go func() {
		log.Printf("ai-console listening on %s (control-hub %s, model %s, read-only %v)", cfg.ListenAddr, cfg.ControlHubURL, cfg.Model, cfg.ReadOnly)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
}
