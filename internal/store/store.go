package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *gorm.DB }

// Open connects to Postgres when the URL starts with postgres:// or
// postgresql://, and to a local SQLite file otherwise (local development).
func Open(url string) (*Store, error) {
	var dial gorm.Dialector
	isSQLite := !strings.HasPrefix(url, "postgres://") && !strings.HasPrefix(url, "postgresql://")
	if isSQLite {
		dial = sqlite.Open(sqliteDSN(url))
	} else {
		dial = postgres.Open(url)
	}
	db, err := gorm.Open(dial, &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if isSQLite {
		// SQLite allows one writer at a time. Transactions take the write lock
		// when they begin (_txlock=immediate) and wait for it (busy_timeout),
		// so a COMMIT never fails with SQLITE_BUSY; such a failure left its
		// pooled connection inside an open transaction and broke every later
		// write on it. With WAL, readers on the other connections do not wait
		// for the writer.
		sqlDB, err := db.DB()
		if err != nil {
			return nil, err
		}
		sqlDB.SetMaxOpenConns(4)
	}
	if err := db.AutoMigrate(&Session{}, &UserKey{}, &Chat{}, &TranscriptEntry{}, &ChatItem{},
		&ToolCall{}, &Action{}, &Investigation{}, &Finding{}, &KV{}, &AuditEntry{}, &LogRule{}, &LogRuleHit{}, &GrafanaInstance{}, &GrafanaMapping{},
		&CostClusterDay{}, &CostWorkloadDay{}, &CostContainerDay{}, &Learning{}, &LogPattern{}, &ReviewBatch{}, &RightsizingReview{}); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	st := &Store{db: db}
	st.backfillFindingAcks()
	return st, nil
}

// backfillFindingAcks names who acknowledged findings acknowledged before the
// finding recorded it, from the audit log ("<finding id>: <title>").
func (s *Store) backfillFindingAcks() {
	var fs []Finding
	if s.db.Where("state = ? AND (acknowledged_by IS NULL OR acknowledged_by = '')", "acknowledged").Find(&fs).Error != nil {
		return
	}
	for i := range fs {
		var e AuditEntry
		err := s.db.Where("action = ? AND detail LIKE ?", "acknowledge_finding", fs[i].ID+":%").Order("at DESC").First(&e).Error
		if err != nil {
			continue
		}
		at := e.At
		fs[i].AcknowledgedBy, fs[i].AcknowledgedAt = e.Actor, &at
		_ = s.db.Save(&fs[i]).Error
	}
}

// sqliteDSN adds the pragmas every connection needs, unless the URL sets them:
//   - temp_store=memory: the image has no /tmp, and without it large sorts and
//     transactions fail with "disk I/O error (6410)", SQLITE_IOERR_GETTEMPPATH;
//   - busy_timeout: wait up to 10 s for a lock instead of failing at once;
//   - journal_mode=WAL: reads do not wait for a writer;
//   - _txlock=immediate: transactions take the write lock when they begin.
func sqliteDSN(url string) string {
	for _, p := range []struct{ name, param string }{
		{"temp_store", "_pragma=temp_store(2)"},
		{"busy_timeout", "_pragma=busy_timeout(10000)"},
		{"journal_mode", "_pragma=journal_mode(WAL)"},
		{"_txlock", "_txlock=immediate"},
	} {
		if strings.Contains(url, p.name) {
			continue
		}
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		url += sep + p.param
	}
	return url
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

// --- sessions ---

func (s *Store) CreateSession(sess *Session) error { return s.db.Create(sess).Error }

func (s *Store) GetSession(id string) (*Session, error) {
	var sess Session
	if err := s.db.Where("id = ? AND expires_at > ?", id, time.Now()).First(&sess).Error; err != nil {
		return nil, notFound(err)
	}
	return &sess, nil
}

// LatestSession returns the newest unexpired session of an email, for acting
// as that person outside the browser (the Copilot in Slack).
func (s *Store) LatestSession(email string) (*Session, error) {
	var sess Session
	if err := s.db.Where("LOWER(email) = ? AND expires_at > ?", strings.ToLower(email), time.Now()).
		Order("created_at DESC").First(&sess).Error; err != nil {
		return nil, notFound(err)
	}
	return &sess, nil
}

func (s *Store) DeleteSession(id string) error {
	return s.db.Where("id = ?", id).Delete(&Session{}).Error
}

func (s *Store) DeleteExpiredSessions() error {
	return s.db.Where("expires_at <= ?", time.Now()).Delete(&Session{}).Error
}

// --- Claude keys ---

func (s *Store) GetUserKey(email string) (*UserKey, error) {
	var k UserKey
	if err := s.db.Where("email = ?", email).First(&k).Error; err != nil {
		return nil, notFound(err)
	}
	return &k, nil
}

func (s *Store) PutUserKey(k *UserKey) error { return s.db.Save(k).Error }

func (s *Store) DeleteUserKey(email string) error {
	return s.db.Where("email = ?", email).Delete(&UserKey{}).Error
}

// --- chats ---

func (s *Store) CreateChat(c *Chat) error { return s.db.Create(c).Error }

func (s *Store) GetChat(id string) (*Chat, error) {
	var c Chat
	if err := s.db.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

func (s *Store) ListChats(owner string) ([]Chat, error) {
	var out []Chat
	err := s.db.Where("owner = ?", owner).Order("updated_at DESC").Limit(200).Find(&out).Error
	return out, err
}

func (s *Store) TouchChat(id, title string) error {
	updates := map[string]any{"updated_at": time.Now()}
	if title != "" {
		updates["title"] = title
	}
	return s.db.Model(&Chat{}).Where("id = ?", id).Updates(updates).Error
}

func (s *Store) AppendTranscript(chatID string, params ...any) error {
	for _, p := range params {
		raw, err := json.Marshal(p)
		if err != nil {
			return err
		}
		if err := s.db.Create(&TranscriptEntry{ChatID: chatID, Param: string(raw)}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Transcript(chatID string) ([]TranscriptEntry, error) {
	var out []TranscriptEntry
	err := s.db.Where("chat_id = ?", chatID).Order("id ASC").Find(&out).Error
	return out, err
}

func (s *Store) SaveChatItem(it *ChatItem) error { return s.db.Save(it).Error }

func (s *Store) ChatItems(chatID string) ([]ChatItem, error) {
	var out []ChatItem
	err := s.db.Where("chat_id = ?", chatID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// --- tool calls ---

func (s *Store) SaveToolCall(tc *ToolCall) error { return s.db.Save(tc).Error }

func (s *Store) ToolCallsForChat(chatID string) ([]ToolCall, error) {
	var out []ToolCall
	err := s.db.Where("chat_id = ?", chatID).Order("at ASC").Find(&out).Error
	return out, err
}

// --- actions ---

func (s *Store) SaveAction(a *Action) error { return s.db.Save(a).Error }

func (s *Store) GetAction(id string) (*Action, error) {
	var a Action
	if err := s.db.Where("id = ?", id).First(&a).Error; err != nil {
		return nil, notFound(err)
	}
	return &a, nil
}

// ClaimAction moves an action from proposed to running. It returns false when
// another request already claimed it, so a double click runs it once.
func (s *Store) ClaimAction(id string) (bool, error) {
	res := s.db.Model(&Action{}).Where("id = ? AND status = ?", id, "proposed").Update("status", "running")
	return res.RowsAffected == 1, res.Error
}

func (s *Store) ActionsForChat(chatID string) ([]Action, error) {
	var out []Action
	err := s.db.Where("chat_id = ?", chatID).Order("created_at ASC").Find(&out).Error
	return out, err
}

// --- investigations ---

func (s *Store) SaveInvestigation(inv *Investigation) error { return s.db.Save(inv).Error }

func (s *Store) GetInvestigation(id string) (*Investigation, error) {
	var inv Investigation
	if err := s.db.Where("id = ?", id).First(&inv).Error; err != nil {
		return nil, notFound(err)
	}
	return &inv, nil
}

// ListInvestigations returns every investigation when requester is empty.
func (s *Store) ListInvestigations(requester string) ([]Investigation, error) {
	q := s.db.Order("created_at DESC").Limit(200)
	if requester != "" {
		q = q.Where("requester = ?", requester)
	}
	var out []Investigation
	return out, q.Find(&out).Error
}

func (s *Store) CountInvestigationsSince(t time.Time) (int64, error) {
	var n int64
	err := s.db.Model(&Investigation{}).Where("created_at >= ?", t).Count(&n).Error
	return n, err
}

// --- findings ---

func (s *Store) GetFindingByKey(key string) (*Finding, error) {
	var f Finding
	if err := s.db.Where("key = ?", key).First(&f).Error; err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

func (s *Store) GetFinding(id string) (*Finding, error) {
	var f Finding
	if err := s.db.Where("id = ?", id).First(&f).Error; err != nil {
		return nil, notFound(err)
	}
	return &f, nil
}

func (s *Store) SaveFinding(f *Finding) error { return s.db.Save(f).Error }

// ListFindings filters by state; "all" or "" returns every state.
// "open" includes acknowledged findings, which are still unresolved.
func (s *Store) ListFindings(state string) ([]Finding, error) {
	q := s.db.Order("CASE severity WHEN 'critical' THEN 0 ELSE 1 END").Order("last_seen_at DESC").Limit(500)
	switch state {
	case "open":
		q = q.Where("state IN ?", []string{"open", "acknowledged"})
	case "resolved":
		q = q.Where("state = ?", "resolved")
	}
	var out []Finding
	return out, q.Find(&out).Error
}

// FindingsSince returns findings seen at or after t, in any state.
func (s *Store) FindingsSince(t time.Time) ([]Finding, error) {
	var out []Finding
	return out, s.db.Where("last_seen_at >= ? OR resolved_at >= ?", t, t).Limit(2000).Find(&out).Error
}

// UnresolvedFindings returns open and acknowledged findings, for the monitor to
// resolve the ones whose condition has cleared.
func (s *Store) UnresolvedFindings() ([]Finding, error) { return s.ListFindings("open") }

// --- key/value ---

func (s *Store) GetKV(key string, into any) (bool, error) {
	var kv KV
	if err := s.db.Where("key = ?", key).First(&kv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, json.Unmarshal([]byte(kv.Value), into)
}

func (s *Store) PutKV(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.db.Save(&KV{Key: key, Value: string(raw), UpdatedAt: time.Now()}).Error
}

// --- audit ---

func (s *Store) Audit(actor, action, target, decision, detail string) {
	// Audit writes are best effort: a failure must not fail the user's request,
	// but it should be visible in the logs.
	if err := s.db.Create(&AuditEntry{At: time.Now(), Actor: actor, Action: action,
		Target: target, Decision: decision, Detail: detail}).Error; err != nil {
		log.Printf("audit write failed: %v", err)
	}
}

func (s *Store) ListAudit(limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var out []AuditEntry
	err := s.db.Order("at DESC").Limit(limit).Find(&out).Error
	return out, err
}

// --- log rules ---

func (s *Store) ListLogRules() ([]LogRule, error) {
	var out []LogRule
	return out, s.db.Order("id").Find(&out).Error
}

func (s *Store) GetLogRule(id string) (*LogRule, error) {
	var r LogRule
	if err := s.db.Where("id = ?", id).First(&r).Error; err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

func (s *Store) SaveLogRule(r *LogRule) error { return s.db.Save(r).Error }

func (s *Store) DeleteLogRule(id string) error {
	return s.db.Where("id = ?", id).Delete(&LogRule{}).Error
}

// AddLogHits adds new matches found by the log worker.
func (s *Store) AddLogHits(ruleID, clusterID, ns, pod string, n int) error {
	return s.db.Create(&LogRuleHit{RuleID: ruleID, ClusterID: clusterID, Namespace: ns, Pod: pod, Count: n, SeenAt: time.Now()}).Error
}

// LogHitTotals returns matches per rule since t.
func (s *Store) LogHitTotals(since time.Time) (map[string]int, error) {
	var rows []struct {
		RuleID string
		Total  int
	}
	err := s.db.Model(&LogRuleHit{}).Select("rule_id, SUM(count) AS total").
		Where("seen_at >= ?", since).Group("rule_id").Find(&rows).Error
	out := map[string]int{}
	for _, r := range rows {
		out[r.RuleID] = r.Total
	}
	return out, err
}

func (s *Store) PruneLogHits(before time.Time) error {
	return s.db.Where("seen_at < ?", before).Delete(&LogRuleHit{}).Error
}

// --- Grafana ---

func (s *Store) ListGrafanaInstances() ([]GrafanaInstance, error) {
	var out []GrafanaInstance
	return out, s.db.Order("name").Find(&out).Error
}

func (s *Store) SaveGrafanaInstance(g *GrafanaInstance) error { return s.db.Save(g).Error }

func (s *Store) DeleteGrafanaInstance(name string) error {
	return s.db.Where("name = ?", name).Delete(&GrafanaInstance{}).Error
}

func (s *Store) ListGrafanaMappings() ([]GrafanaMapping, error) {
	var out []GrafanaMapping
	return out, s.db.Order("cluster").Find(&out).Error
}

func (s *Store) SaveGrafanaMapping(m *GrafanaMapping) error { return s.db.Save(m).Error }

func (s *Store) DeleteGrafanaMapping(cluster string) error {
	return s.db.Where("cluster = ?", cluster).Delete(&GrafanaMapping{}).Error
}

// --- Costs ---

// ReplaceCostDay stores a cluster's day, replacing what was there.
func (s *Store) ReplaceCostDay(day *CostClusterDay, workloads []CostWorkloadDay, containers []CostContainerDay) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("day = ? AND cluster = ?", day.Day, day.Cluster).Delete(&CostWorkloadDay{}).Error; err != nil {
			return err
		}
		if err := tx.Where("day = ? AND cluster = ?", day.Day, day.Cluster).Delete(&CostContainerDay{}).Error; err != nil {
			return err
		}
		if len(containers) > 0 {
			if err := tx.CreateInBatches(containers, 200).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("day = ? AND cluster = ?", day.Day, day.Cluster).Delete(&CostClusterDay{}).Error; err != nil {
			return err
		}
		if err := tx.Create(day).Error; err != nil {
			return err
		}
		if len(workloads) == 0 {
			return nil
		}
		return tx.CreateInBatches(workloads, 200).Error
	})
}

// CostDays returns cluster-days between from and to (inclusive, YYYY-MM-DD),
// optionally for one cluster.
func (s *Store) CostDays(from, to, cluster string) ([]CostClusterDay, error) {
	q := s.db.Where("day >= ? AND day <= ?", from, to)
	if cluster != "" {
		q = q.Where("cluster = ?", cluster)
	}
	var out []CostClusterDay
	return out, q.Order("day, cluster").Find(&out).Error
}

// CostWorkloadRows returns per-day workload rows between from and to.
func (s *Store) CostWorkloadRows(from, to, cluster string) ([]CostWorkloadDay, error) {
	q := s.db.Where("day >= ? AND day <= ?", from, to)
	if cluster != "" {
		q = q.Where("cluster = ?", cluster)
	}
	var out []CostWorkloadDay
	return out, q.Find(&out).Error
}

// CostWorkloads sums workload costs between from and to, optionally filtered.
func (s *Store) CostWorkloads(from, to, cluster, namespace string) ([]CostWorkloadDay, error) {
	q := s.db.Model(&CostWorkloadDay{}).
		Select("cluster, namespace, kind, name, SUM(cpu_cost) AS cpu_cost, SUM(mem_cost) AS mem_cost, "+
			"SUM(cpu_request) AS cpu_request, SUM(cpu_usage) AS cpu_usage, SUM(mem_request) AS mem_request, "+
			"SUM(mem_usage) AS mem_usage, MAX(pods) AS pods").
		Where("day >= ? AND day <= ?", from, to)
	if cluster != "" {
		q = q.Where("cluster = ?", cluster)
	}
	if namespace != "" {
		q = q.Where("namespace = ?", namespace)
	}
	var out []CostWorkloadDay
	return out, q.Group("cluster, namespace, kind, name").Order("SUM(cpu_cost + mem_cost) DESC").Find(&out).Error
}

// CostContainers returns container-days between from and to, optionally filtered.
func (s *Store) CostContainers(from, to, cluster, namespace string) ([]CostContainerDay, error) {
	q := s.db.Where("day >= ? AND day <= ?", from, to)
	if cluster != "" {
		q = q.Where("cluster = ?", cluster)
	}
	if namespace != "" {
		q = q.Where("namespace = ?", namespace)
	}
	var out []CostContainerDay
	return out, q.Order("day").Find(&out).Error
}

// ContainerAgg is a workload container's usage over a range: peaks and the
// highest daily p95 across its days, replica-hours, OOM days, and the
// requests and limits of its latest day.
type ContainerAgg struct {
	Cluster, Namespace, Kind, Name, Container string
	Days                                      int
	CPUP95, CPUMax, MemMax                    float64
	ThrottledPct, PodHours                    float64
	OOMDays                                   int
	CPURequest, CPULimit                      float64
	MemRequest, MemLimit                      float64
}

// CostContainerAggs aggregates container-days in the database, so a fleet-wide
// range does not load every row.
func (s *Store) CostContainerAggs(from, to, cluster, namespace string) ([]ContainerAgg, error) {
	filter := func(q *gorm.DB) *gorm.DB {
		q = q.Where("day >= ? AND day <= ?", from, to)
		if cluster != "" {
			q = q.Where("cluster = ?", cluster)
		}
		if namespace != "" {
			q = q.Where("namespace = ?", namespace)
		}
		return q
	}
	agg := filter(s.db.Model(&CostContainerDay{})).
		Select("cluster, namespace, kind, name, container, COUNT(*) AS days, MAX(cpu_p95) AS cpu_p95, MAX(cpu_max) AS cpu_max, " +
			"MAX(mem_max) AS mem_max, MAX(throttled_pct) AS throttled_pct, SUM(pod_hours) AS pod_hours, " +
			"SUM(CASE WHEN oom_killed THEN 1 ELSE 0 END) AS oom_days, MAX(day) AS last_day").
		Group("cluster, namespace, kind, name, container")
	var out []ContainerAgg
	err := s.db.Table("(?) AS a", agg).
		Joins("JOIN cost_container_days c ON c.cluster = a.cluster AND c.namespace = a.namespace AND c.kind = a.kind " +
			"AND c.name = a.name AND c.container = a.container AND c.day = a.last_day").
		Select("a.cluster, a.namespace, a.kind, a.name, a.container, a.days, a.cpu_p95, a.cpu_max, a.mem_max, a.throttled_pct, " +
			"a.pod_hours, a.oom_days, c.cpu_request, c.cpu_limit, c.mem_request, c.mem_limit").
		Scan(&out).Error
	return out, err
}

// SaveLogPatternCounts upserts patterns' counters, keeping their status.
func (s *Store) SaveLogPatternCounts(ps []LogPattern) error {
	if len(ps) == 0 {
		return nil
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"template", "count", "clusters", "hourly", "first_seen", "last_seen", "updated_at"}),
	}).CreateInBatches(ps, 200).Error
}

// ListLogPatterns returns patterns with a status (all when empty), most recent first.
func (s *Store) ListLogPatterns(status string) ([]LogPattern, error) {
	q := s.db.Order("last_seen DESC").Limit(3000)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []LogPattern
	return out, q.Find(&out).Error
}

func (s *Store) GetLogPattern(id string) (*LogPattern, error) {
	var p LogPattern
	if err := s.db.Where("id = ?", id).First(&p).Error; err != nil {
		return nil, notFound(err)
	}
	return &p, nil
}

// SetLogPatternStatus records an admin's decision on a pattern.
func (s *Store) SetLogPatternStatus(id, status, ruleID string) error {
	return s.db.Model(&LogPattern{}).Where("id = ?", id).Updates(map[string]any{"status": status, "rule_id": ruleID}).Error
}

// DeleteLogPatterns removes every pattern with a status.
func (s *Store) DeleteLogPatterns(status string) error {
	return s.db.Where("status = ?", status).Delete(&LogPattern{}).Error
}

// PruneLogPatterns drops patterns not seen since t that nobody acted on.
func (s *Store) PruneLogPatterns(t time.Time) error {
	return s.db.Where("last_seen < ? AND status = ?", t, "new").Delete(&LogPattern{}).Error
}

func (s *Store) SaveReviewBatch(b *ReviewBatch) error { return s.db.Save(b).Error }

// ReviewBatches returns batches with a status (all when empty), newest first.
func (s *Store) ReviewBatches(status string, limit int) ([]ReviewBatch, error) {
	q := s.db.Order("submitted_at DESC").Limit(limit)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var out []ReviewBatch
	return out, q.Find(&out).Error
}

func (s *Store) SaveRightsizingReview(r *RightsizingReview) error { return s.db.Save(r).Error }

func (s *Store) GetRightsizingReview(key string) (*RightsizingReview, error) {
	var r RightsizingReview
	if err := s.db.Where("key = ?", key).First(&r).Error; err != nil {
		return nil, notFound(err)
	}
	return &r, nil
}

func (s *Store) RightsizingReviews(batchID string) ([]RightsizingReview, error) {
	q := s.db.Limit(5000)
	if batchID != "" {
		q = q.Where("batch_id = ?", batchID)
	}
	var out []RightsizingReview
	return out, q.Find(&out).Error
}

// CostDaysDone lists the days stored for a cluster.
func (s *Store) CostDaysDone(cluster string) (map[string]bool, error) {
	var days []string
	err := s.db.Model(&CostClusterDay{}).Where("cluster = ?", cluster).Pluck("day", &days).Error
	out := map[string]bool{}
	for _, d := range days {
		out[d] = true
	}
	return out, err
}

// PruneCosts drops days before day (YYYY-MM-DD).
func (s *Store) PruneCosts(day string) error {
	if err := s.db.Where("day < ?", day).Delete(&CostWorkloadDay{}).Error; err != nil {
		return err
	}
	if err := s.db.Where("day < ?", day).Delete(&CostContainerDay{}).Error; err != nil {
		return err
	}
	return s.db.Where("day < ?", day).Delete(&CostClusterDay{}).Error
}
