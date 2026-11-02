// Package config reads the AI console's settings from the environment.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string
	StaticDir  string
	// DocsDir holds the built documentation site, served at /docs/ to signed-in users.
	DocsDir string

	// ControlHubURL is control-hub's base URL, without the /api suffix.
	ControlHubURL string

	DatabaseURL   string
	EncryptionKey []byte

	// SharedAnthropicKey is used for users who have not stored a personal key.
	// Empty means those users cannot chat until they add one.
	SharedAnthropicKey string
	Model              string
	Effort             string

	KnowledgeGraphDir string

	MonitorEnabled  bool
	MonitorInterval time.Duration
	// ServiceToken is a control-hub token the monitors use. It needs read access
	// to every realm, so in practice an admin token. The console only ever
	// reads with it: the control-hub client rejects any non-GET request.
	ServiceToken string
	// PublicURL is the console's address, for links in Slack (PUBLIC_URL).
	PublicURL string
	// Weekly digest to Slack: on by default, Monday 04:00 UTC (09:30 IST).
	DigestEnabled bool
	DigestWeekday int // 0 = Sunday
	DigestHourUTC int
	// AdminEmails are console admins in addition to control-hub admins
	// (CONSOLE_ADMIN_EMAILS, comma separated). Console admin rights cover the
	// console itself: root causes, learnings, settings. Anything that calls
	// control-hub still runs with the user's own control-hub token.
	AdminEmails []string
	// AthenaToken is the Authorization value for control planes' Athena API
	// (base64 email:password, as control-hub's ATHENA_AUTH_TOKEN). The
	// console only ever sends GETs to Athena's read views with it.
	AthenaToken string
	// DevServiceToken is the same for control-hub dev; also read-only.
	DevServiceToken string
	StuckAfter      time.Duration
	CertWarnDays    int
	Realms          []string

	SlackWebhookURL string
	SlackMute       bool
	// SlackBotToken and SlackSigningSecret turn on the Copilot in Slack
	// (POST /api/slack/events); both are needed.
	SlackBotToken      string
	SlackSigningSecret string

	// PodNamespace and ControlHubPodSelector locate control-hub's pods for the
	// logs tool. Empty namespace disables the tool.
	PodNamespace          string
	ControlHubPodSelector string
	// ControlHubLogsNamespace is where control-hub's pods run, for log search.
	// Defaults to POD_NAMESPACE; set it to "" to turn log search off (e.g. when
	// CONTROLHUB_URL is another namespace's control-hub that the console cannot read).
	ControlHubLogsNamespace string

	CookieSecure bool

	// DataDir is the persistent volume root for the log worker's files.
	DataDir           string
	LogScanInterval   time.Duration
	LogScanNamespaces []string
	// PatternIgnoreServices are workloads left out of error-pattern mining.
	PatternIgnoreServices []string
	HitRetentionDays      int

	// Certificate inventory (internal/certs).
	CertScanInterval     time.Duration
	CertSecretNamespaces []string
	CertACMRegions       []string
	CertGCPProjectFilter string
	// AuditGCPProjects are read for "who changed what" (Cloud Audit Logs:
	// GKE Kubernetes audit and GCP resources); empty disables audit_search.
	AuditGCPProjects []string
	// CertRepairEnabled lets admins ask control-hub to restore a certificate's
	// renewal DNS records. It is the one write allowed alongside READ_ONLY, and it
	// runs with the admin's own control-hub token.
	CertRepairEnabled bool

	// Workload cost (internal/cost).
	CostEnabled       bool
	CostBackfillDays  int
	CostRetentionDays int

	// ReadOnly turns off every write the console can make (Copilot actions).
	// Required when CONTROLHUB_URL points at a production control-hub.
	ReadOnly bool
	// ControlHubLabel names the connected control-hub in the UI, e.g. "control-hub-prod".
	ControlHubLabel string
}

func Load() (*Config, error) {
	c := &Config{
		ListenAddr:            env("LISTEN_ADDR", ":8090"),
		StaticDir:             env("STATIC_DIR", "./web/dist"),
		DocsDir:               env("DOCS_DIR", "./site"),
		ControlHubURL:         strings.TrimRight(env("CONTROLHUB_URL", "http://control-hub:8003"), "/"),
		DatabaseURL:           env("DATABASE_URL", "file:ai-console.db"),
		SharedAnthropicKey:    os.Getenv("ANTHROPIC_API_KEY"),
		Model:                 env("CLAUDE_MODEL", "claude-opus-5-5"),
		Effort:                env("CLAUDE_EFFORT", "high"),
		KnowledgeGraphDir:     env("KG_DIR", "./knowledgegraph"),
		MonitorEnabled:        envBool("MONITOR_ENABLED", true),
		ServiceToken:          os.Getenv("CONTROLHUB_SERVICE_TOKEN"),
		AthenaToken:           os.Getenv("ATHENA_AUTH_TOKEN"),
		AdminEmails:           list(os.Getenv("CONSOLE_ADMIN_EMAILS")),
		PublicURL:             env("PUBLIC_URL", "https://controlhub.app.prophecy.ai"),
		DigestEnabled:         envBool("DIGEST_ENABLED", true),
		DigestWeekday:         envInt("DIGEST_WEEKDAY", 1),
		DigestHourUTC:         envInt("DIGEST_HOUR_UTC", 4),
		DevServiceToken:       os.Getenv("CONTROLHUB_DEV_SERVICE_TOKEN"),
		CertWarnDays:          envInt("CERT_WARN_DAYS", 5),
		Realms:                strings.Split(env("REALMS", "sap,dedicated-saas,public-saas"), ","),
		SlackWebhookURL:       os.Getenv("SLACK_WEBHOOK_URL"),
		SlackMute:             envBool("SLACK_MUTE", false),
		SlackBotToken:         os.Getenv("SLACK_BOT_TOKEN"),
		SlackSigningSecret:    os.Getenv("SLACK_SIGNING_SECRET"),
		PodNamespace:          os.Getenv("POD_NAMESPACE"),
		ControlHubPodSelector: env("CONTROLHUB_POD_SELECTOR", "app=control-hub"),
		CookieSecure:          envBool("COOKIE_SECURE", true),
		ReadOnly:              envBool("READ_ONLY", false),
		DataDir:               env("DATA_DIR", "./data"),
		LogScanNamespaces:     strings.Split(env("LOG_SCAN_NAMESPACES", "prophecy"), ","),
		PatternIgnoreServices: list(env("PATTERN_IGNORE_SERVICES", "sla-tests,e2e-tests")),
		HitRetentionDays:      envInt("HIT_RETENTION_DAYS", 30),
		ControlHubLabel:       env("CONTROLHUB_LABEL", "control-hub"),
		CertSecretNamespaces:  list(env("CERT_SECRET_NAMESPACES", "control-hub-prod")),
		// control-hub's AWS user may only read ACM in the US regions.
		CertACMRegions:       list(env("CERT_ACM_REGIONS", "us-east-1,us-east-2,us-west-1,us-west-2")),
		CertGCPProjectFilter: os.Getenv("CERT_GCP_PROJECT_FILTER"),
		AuditGCPProjects:     list(env("AUDIT_GCP_PROJECTS", "prophecy-cloud")),
		CertRepairEnabled:    envBool("CERT_REPAIR_ENABLED", false),
		CostEnabled:          envBool("COST_ENABLED", true),
		CostBackfillDays:     envInt("COST_BACKFILL_DAYS", 30),
		CostRetentionDays:    envInt("COST_RETENTION_DAYS", 400),
	}
	if _, ok := os.LookupEnv("CERT_GCP_PROJECT_FILTER"); !ok {
		c.CertGCPProjectFilter = "id:sap-*"
	}

	c.ControlHubLogsNamespace = c.PodNamespace
	if v, ok := os.LookupEnv("CONTROLHUB_LOGS_NAMESPACE"); ok {
		c.ControlHubLogsNamespace = v
	}

	var err error
	if c.MonitorInterval, err = envDuration("MONITOR_INTERVAL", 5*time.Minute); err != nil {
		return nil, err
	}
	if c.StuckAfter, err = envDuration("STUCK_AFTER", 45*time.Minute); err != nil {
		return nil, err
	}
	if c.LogScanInterval, err = envDuration("LOG_SCAN_INTERVAL", 60*time.Second); err != nil {
		return nil, err
	}
	if c.CertScanInterval, err = envDuration("CERT_SCAN_INTERVAL", 6*time.Hour); err != nil {
		return nil, err
	}

	raw := os.Getenv("ENCRYPTION_KEY")
	if raw == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY is required (base64 of 32 random bytes: openssl rand -base64 32)")
	}
	if c.EncryptionKey, err = base64.StdEncoding.DecodeString(raw); err != nil || len(c.EncryptionKey) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be base64 of exactly 32 bytes")
	}

	if c.MonitorEnabled && c.ServiceToken == "" {
		return nil, fmt.Errorf("CONTROLHUB_SERVICE_TOKEN is required when MONITOR_ENABLED=true")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// list splits a comma-separated value, dropping empty entries.
func list(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func envBool(key string, def bool) bool {
	v, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}

func envInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil {
		return def
	}
	return v
}

func envDuration(key string, def time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, raw, err)
	}
	return d, nil
}

// IsConsoleAdmin reports whether email is in CONSOLE_ADMIN_EMAILS.
func (c *Config) IsConsoleAdmin(email string) bool {
	for _, a := range c.AdminEmails {
		if strings.EqualFold(a, strings.TrimSpace(email)) {
			return true
		}
	}
	return false
}
