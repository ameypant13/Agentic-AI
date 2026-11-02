// Package monitor polls control-hub on a schedule and turns problems into
// findings before anyone reports them: failed or stuck provisioning, failed
// post-deploy, and certificates close to expiry.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/config"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/controlhub"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/secret"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/store"
	"github.com/SimpleDataLabsInc/control-hub/ai-console/internal/tools"
)

const (
	kindCPFailed         = "cp_failed"
	kindCPStuck          = "cp_stuck"
	kindPostDeployFailed = "postdeploy_failed"
	kindEPFailed         = "ep_failed"
	kindCertExpiry       = "cert_expiry"

	kvFleet     = "monitor:fleet"
	kvFirstSeen = "monitor:first-seen"
	kvBaselined = "monitor:baselined"
)

// Definitions describes the monitors for the UI.
func Definitions(cfg *config.Config) []map[string]any {
	every := cfg.MonitorInterval.String()
	notify := "Slack"
	if cfg.SlackWebhookURL == "" || cfg.SlackMute {
		notify = "Console only"
	}
	defs := []struct{ key, name, cond string }{
		{kindCPFailed, "Failed provisioning", "control plane in CREATION_FAILED, DELETION_FAILED or UPDATE_FAILED"},
		{kindCPStuck, "Stuck provisioning", fmt.Sprintf("CREATING, DELETING or UPDATING for more than %s", cfg.StuckAfter)},
		{kindPostDeployFailed, "Post-deploy failed", "control plane or execution plane postDeployStatus = failed"},
		{kindEPFailed, "Execution plane failed", "SAP execution plane in a *_FAILED status"},
		{kindLogErrors, "Log parser", "Error-severity log rule hits in the prophecy namespace of any cluster (recent lines)"},
		{kindCertExpiry, "Certificate expiry", fmt.Sprintf("certificate served by an operational control plane expires in under %d days", cfg.CertWarnDays)},
	}
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		out = append(out, map[string]any{"key": d.key, "name": d.name, "condition": d.cond,
			"interval": every, "notify": notify, "enabled": cfg.MonitorEnabled})
	}
	return out
}

// Fleet is the snapshot the last run saw, served by /api/fleet.
type Fleet struct {
	Total    int            `json:"total"`
	ByStatus map[string]int `json:"byStatus"`
	ByRealm  map[string]int `json:"byRealm"`
	At       time.Time      `json:"at"`
}

func LoadFleet(st *store.Store) (*Fleet, error) {
	var f Fleet
	ok, err := st.GetKV(kvFleet, &f)
	if err != nil || !ok {
		return nil, err
	}
	return &f, nil
}

type Monitor struct {
	cfg   *config.Config
	st    *store.Store
	ch    *controlhub.Client
	slack *Slack
	logs  *LogScan
}

func New(cfg *config.Config, st *store.Store, ch *controlhub.Client) *Monitor {
	return &Monitor{cfg: cfg, st: st, ch: ch, slack: &Slack{WebhookURL: cfg.SlackWebhookURL, Mute: cfg.SlackMute}}
}

// Start runs the monitors every interval until ctx ends.
func (m *Monitor) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(m.cfg.MonitorInterval)
		defer t.Stop()
		for {
			if err := m.RunOnce(ctx); err != nil {
				log.Printf("monitor run failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

// observation is one condition seen in this run.
type observation struct {
	key, kind, severity, title, detail string
	entityType, entityID, entityName   string
	realm, provider, region            string
}

func (m *Monitor) RunOnce(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	firstSeen := map[string]time.Time{}
	if _, err := m.st.GetKV(kvFirstSeen, &firstSeen); err != nil {
		return err
	}
	seenNow := map[string]time.Time{}
	fleet := Fleet{ByStatus: map[string]int{}, ByRealm: map[string]int{}, At: time.Now()}
	var obs []observation
	var operational []tools.ControlPlaneSummary
	listedRealms := map[string]bool{}

	for _, realm := range m.cfg.Realms {
		cps, err := tools.ListControlPlanes(ctx, m.ch, m.cfg.ServiceToken, realm, "")
		if err != nil {
			log.Printf("monitor: list %s control planes: %v", realm, err)
			continue
		}
		listedRealms[realm] = true
		for _, cp := range cps {
			fleet.Total++
			fleet.ByStatus[cp.Status]++
			fleet.ByRealm[realm]++
			obs = append(obs, m.checkControlPlane(cp, firstSeen, seenNow)...)
			if cp.Status == "OPERATIONAL" && cp.URL != "" {
				operational = append(operational, cp)
			}
		}
	}
	if len(listedRealms) == 0 {
		return fmt.Errorf("could not list control planes in any realm; check CONTROLHUB_SERVICE_TOKEN")
	}
	// checked records which finding sources ran, so reconcile only resolves
	// findings whose check actually ran this time.
	checked := map[string]bool{}
	for realm := range listedRealms {
		checked[realm] = true
	}
	if listedRealms["sap"] {
		eps, err := m.checkExecutionPlanes(ctx)
		if err != nil {
			log.Printf("monitor: execution planes: %v", err)
		} else {
			checked["execution_planes"] = true
		}
		obs = append(obs, eps...)
	}
	certObs, unreachable := m.checkCerts(ctx, operational)
	obs = append(obs, certObs...)
	obs = append(obs, m.checkLogs(ctx, checked)...)

	if err := m.st.PutKV(kvFirstSeen, seenNow); err != nil {
		return err
	}
	if err := m.st.PutKV(kvFleet, fleet); err != nil {
		return err
	}
	return m.reconcile(obs, checked, unreachable)
}

func (m *Monitor) checkControlPlane(cp tools.ControlPlaneSummary, firstSeen, seenNow map[string]time.Time) []observation {
	base := observation{entityType: "control_plane", entityID: cp.ID, entityName: firstNonEmpty(cp.CustomerName, cp.ID),
		realm: cp.Realm, provider: cp.Provider, region: cp.Region}
	var out []observation

	switch cp.Status {
	case "CREATION_FAILED", "DELETION_FAILED", "UPDATE_FAILED":
		o := base
		o.key, o.kind, o.severity = kindCPFailed+":"+cp.ID, kindCPFailed, "critical"
		o.title = map[string]string{"CREATION_FAILED": "Provisioning failed", "DELETION_FAILED": "Deletion failed",
			"UPDATE_FAILED": "Update failed"}[cp.Status]
		o.detail = failureDetail(cp)
		out = append(out, o)
	case "CREATING", "DELETING", "UPDATING":
		k := cp.ID + ":" + cp.Status
		since, ok := firstSeen[k]
		if !ok {
			since = time.Now()
		}
		seenNow[k] = since
		if time.Since(since) > m.cfg.StuckAfter {
			o := base
			o.key, o.kind, o.severity = kindCPStuck+":"+cp.ID, kindCPStuck, "warning"
			o.title = "Stuck in " + cp.Status
			o.detail = fmt.Sprintf("No change for over %s (first seen %s).",
				time.Since(since).Round(time.Minute), since.UTC().Format("2 Jan 15:04 MST"))
			out = append(out, o)
		}
	}
	if cp.PostDeployStatus == "failed" {
		o := base
		o.key, o.kind, o.severity = kindPostDeployFailed+":"+cp.ID, kindPostDeployFailed, "warning"
		o.title, o.detail = "Post-deploy failed", "Control plane post-deploy ended in failed."
		out = append(out, o)
	}
	return out
}

func failureDetail(cp tools.ControlPlaneSummary) string {
	if fr := cp.FailureReason; fr != nil {
		msg, _ := fr["message"].(string)
		action, _ := fr["action"].(string)
		ref, _ := fr["reference"].(string)
		parts := []string{}
		for _, p := range []string{msg, action} {
			if p != "" {
				parts = append(parts, strings.TrimSuffix(p, ".")+".")
			}
		}
		if ref != "" {
			parts = append(parts, "Job "+ref+".")
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	if cp.StatusMessage != "" {
		return cp.StatusMessage
	}
	return "No failure reason recorded."
}

func (m *Monitor) checkExecutionPlanes(ctx context.Context) ([]observation, error) {
	raw, err := m.ch.Get(ctx, m.cfg.ServiceToken, "/api/execution-planes", url.Values{"realm": {"sap"}, "limit": {"2000"}})
	if err != nil {
		return nil, err
	}
	var eps []struct {
		// GormID absorbs control-hub's numeric "ID" (gorm.Model); without it
		// Go's case-insensitive matching puts that number into ID and fails.
		GormID any    `json:"ID"`
		ID     string `json:"id"`
		Config struct {
			CustomerName string `json:"customerName"`
			Message      string `json:"message"`
			Request      struct {
				Name     string `json:"name"`
				Provider string `json:"provider"`
				Region   string `json:"region"`
			} `json:"request"`
		} `json:"config"`
		State struct {
			Status           string `json:"status"`
			PostDeployStatus string `json:"postDeployStatus"`
		} `json:"state"`
	}
	if err := json.Unmarshal(raw, &eps); err != nil {
		return nil, fmt.Errorf("decode execution planes: %w", err)
	}
	var out []observation
	for _, ep := range eps {
		base := observation{entityType: "execution_plane", entityID: ep.ID,
			entityName: firstNonEmpty(ep.Config.CustomerName, ep.Config.Request.Name, ep.ID),
			realm:      "sap", provider: ep.Config.Request.Provider, region: ep.Config.Request.Region}
		if strings.HasSuffix(ep.State.Status, "_FAILED") {
			o := base
			o.key, o.kind, o.severity = kindEPFailed+":"+ep.ID, kindEPFailed, "critical"
			o.title = "Execution plane " + strings.ToLower(strings.ReplaceAll(ep.State.Status, "_", " "))
			o.detail = firstNonEmpty(ep.Config.Message, "No failure message recorded.")
			out = append(out, o)
		}
		if ep.State.PostDeployStatus == "failed" {
			o := base
			o.key, o.kind, o.severity = kindPostDeployFailed+":"+ep.ID, kindPostDeployFailed, "warning"
			o.title, o.detail = "Execution plane post-deploy failed", "Execution plane post-deploy ended in failed."
			out = append(out, o)
		}
	}
	return out, nil
}

// checkCerts returns findings for certificates close to expiry, and the keys of
// certificate findings whose host could not be reached (left as they are).
func (m *Monitor) checkCerts(ctx context.Context, cps []tools.ControlPlaneSummary) ([]observation, map[string]bool) {
	var mu sync.Mutex
	var out []observation
	unreachable := map[string]bool{}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, cp := range cps {
		u, err := url.Parse(cp.URL)
		if err != nil || u.Hostname() == "" {
			u, err = url.Parse("https://" + cp.URL)
			if err != nil || u.Hostname() == "" {
				continue
			}
		}
		wg.Add(1)
		go func(cp tools.ControlPlaneSummary, host string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			info, err := tools.FetchCert(ctx, host)
			if err != nil {
				mu.Lock()
				unreachable[kindCertExpiry+":"+cp.ID] = true
				mu.Unlock()
				return
			}
			if info.DaysLeft >= m.cfg.CertWarnDays {
				return
			}
			sev := "warning"
			if info.DaysLeft <= 1 {
				sev = "critical"
			}
			title := fmt.Sprintf("TLS certificate expires in %d days", info.DaysLeft)
			if info.DaysLeft < 0 {
				title = "TLS certificate has expired"
			}
			mu.Lock()
			out = append(out, observation{
				key: kindCertExpiry + ":" + cp.ID, kind: kindCertExpiry, severity: sev, title: title,
				detail: fmt.Sprintf("%s serves a certificate issued by %s that expires %s.", host, info.Issuer,
					info.NotAfter.UTC().Format("2 Jan 2006 15:04 MST")),
				entityType: "control_plane", entityID: cp.ID, entityName: firstNonEmpty(cp.CustomerName, cp.ID),
				realm: cp.Realm, provider: cp.Provider, region: cp.Region,
			})
			mu.Unlock()
		}(cp, u.Hostname())
	}
	wg.Wait()
	return out, unreachable
}

// reconcile opens or refreshes a finding per observation and resolves open
// findings whose condition is gone. Findings whose check did not run this time
// (a realm or the execution plane list failed, a host was unreachable) are left
// alone. The very first run records what is already broken without notifying,
// so a fresh deploy does not flood Slack.
func (m *Monitor) reconcile(obs []observation, checked, keep map[string]bool) error {
	var baselined bool
	if _, err := m.st.GetKV(kvBaselined, &baselined); err != nil {
		return err
	}
	now := time.Now()
	current := map[string]bool{}
	for _, o := range obs {
		current[o.key] = true
		f, err := m.st.GetFindingByKey(o.key)
		isNew := false
		switch {
		case err == store.ErrNotFound:
			f = &store.Finding{ID: "F-" + strings.ToUpper(secret.RandomID(4)), Key: o.key, FirstSeenAt: now, State: "open"}
			isNew = true
		case err != nil:
			return err
		case f.State == "resolved": // the problem came back
			f.State, f.ResolvedAt, f.FirstSeenAt, f.InvestigationID = "open", nil, now, ""
			f.AcknowledgedBy, f.AcknowledgedByName, f.AcknowledgedAt = "", "", nil
			isNew = true
		}
		f.Kind, f.Severity, f.Title, f.Detail = o.kind, o.severity, o.title, o.detail
		f.EntityType, f.EntityID, f.EntityName = o.entityType, o.entityID, o.entityName
		f.Realm, f.Provider, f.Region, f.LastSeenAt = o.realm, o.provider, o.region, now
		if err := m.st.SaveFinding(f); err != nil {
			return err
		}
		if isNew && baselined {
			m.slack.NotifyFinding(f)
		}
	}

	open, err := m.st.UnresolvedFindings()
	if err != nil {
		return err
	}
	for i := range open {
		f := &open[i]
		source := findingSource(f.Kind, f.Realm, f.EntityType, f.EntityID)
		if current[f.Key] || keep[f.Key] || !checked[source] {
			continue
		}
		f.State, f.ResolvedAt = "resolved", &now
		if err := m.st.SaveFinding(f); err != nil {
			return err
		}
	}
	if !baselined {
		return m.st.PutKV(kvBaselined, true)
	}
	return nil
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
