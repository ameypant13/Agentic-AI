// Package store holds the AI console's own tables. Control-hub's data is never
// stored here; it is always read live through control-hub's API.
package store

import "time"

// Session is a signed-in browser. The control-hub token is kept encrypted and
// every control-hub call made for this user goes out with it, so control-hub's
// own permission checks apply.
type Session struct {
	ID          string `gorm:"primaryKey"`
	Email       string `gorm:"index"`
	Username    string
	IsAdmin     bool
	TokenSealed string
	ExpiresAt   time.Time `gorm:"index"`
	CreatedAt   time.Time
}

// UserKey is a user's personal Claude API key, encrypted.
type UserKey struct {
	Email     string `gorm:"primaryKey"`
	KeySealed string
	Last4     string
	UpdatedAt time.Time
}

type Chat struct {
	ID        string `gorm:"primaryKey"`
	Owner     string `gorm:"index"`
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TranscriptEntry is one message of the conversation exactly as it was sent to
// the Claude API (a BetaMessageParam as JSON). The transcript is append-only:
// entries are never edited, so thinking blocks stay valid across turns.
type TranscriptEntry struct {
	ID        uint   `gorm:"primaryKey"`
	ChatID    string `gorm:"index"`
	Param     string `gorm:"type:text"`
	CreatedAt time.Time
}

// ChatItem is what the UI renders: one user message or one assistant reply.
// It is kept apart from the transcript so tool results never reach a
// non-admin's browser.
type ChatItem struct {
	ID              string `gorm:"primaryKey"`
	ChatID          string `gorm:"index"`
	Role            string
	Text            string `gorm:"type:text"`
	InvestigationID string
	KeySource       string
	InputTokens     int64
	OutputTokens    int64
	CreatedAt       time.Time
}

// ToolCall is one tool execution. It is the evidence trail of an investigation.
type ToolCall struct {
	ID         string `gorm:"primaryKey"`
	ChatID     string `gorm:"index"`
	ChatItemID string `gorm:"index"`
	Tool       string
	Label      string
	Input      string `gorm:"type:text"`
	Summary    string `gorm:"type:text"`
	OK         *bool
	At         time.Time
}

// Action is a state-changing operation the Copilot proposed. It runs only
// when a user clicks it, with that user's control-hub token.
type Action struct {
	ID          string `gorm:"primaryKey"`
	ChatID      string `gorm:"index"`
	ChatItemID  string `gorm:"index"`
	Kind        string
	Title       string
	Description string
	TargetID    string
	TargetName  string
	Params      string `gorm:"type:text"`
	// Plan is the dry run (agent.Plan as JSON): what would run and why it is safe.
	Plan        string `gorm:"type:text"`
	Status      string
	Result      string `gorm:"type:text"`
	ProposedFor string
	ExecutedBy  string
	ExecutedAt  *time.Time
	// ReportedToModel is set once the Copilot has been told the outcome.
	ReportedToModel bool
	CreatedAt       time.Time
}

// Investigation is the Copilot's write-up of a diagnosis. RootCause and
// RecommendedFix are shown to admins only.
type Investigation struct {
	ID                  string `gorm:"primaryKey"`
	ChatID              string `gorm:"index"`
	Requester           string `gorm:"index"`
	Title               string
	EntityID            string
	EntityName          string
	Category            string
	Confidence          string
	SummaryForRequester string `gorm:"type:text"`
	RootCause           string `gorm:"type:text"`
	RecommendedFix      string `gorm:"type:text"`
	CreatedAt           time.Time
}

// Finding is a problem a monitor detected. Key identifies the condition
// (kind + entity) so a monitor run updates the same row instead of adding one.
type Finding struct {
	ID              string `gorm:"primaryKey"`
	Key             string `gorm:"uniqueIndex"`
	Kind            string
	Severity        string
	Title           string
	Detail          string `gorm:"type:text"`
	EntityType      string
	EntityID        string
	EntityName      string
	Realm           string
	Provider        string
	Region          string
	State           string `gorm:"index"`
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	ResolvedAt      *time.Time
	InvestigationID string
	// Who acknowledged the finding, and when; cleared when it comes back.
	AcknowledgedBy     string // email
	AcknowledgedByName string // control-hub username
	AcknowledgedAt     *time.Time
}

// KV is small persistent monitor state: first-seen times, the fleet snapshot.
type KV struct {
	Key       string `gorm:"primaryKey"`
	Value     string `gorm:"type:text"`
	UpdatedAt time.Time
}

type AuditEntry struct {
	ID       uint      `gorm:"primaryKey"`
	At       time.Time `gorm:"index"`
	Actor    string
	Action   string
	Target   string
	Decision string
	Detail   string `gorm:"type:text"`
}

// LogRule is an admin change to the log rule library: a custom rule, or an
// override of a built-in one (same ID). Disabled built-ins are rows with
// Enabled=false. RuleJSON holds the logrules.Rule.
type LogRule struct {
	ID        string `gorm:"primaryKey"`
	RuleJSON  string `gorm:"type:text"`
	Enabled   bool
	UpdatedBy string
	UpdatedAt time.Time
}

// LogRuleHit counts one rule's matches in one pod, per scan window, for the
// "hits in 24 h" column and the parser monitor.
type LogRuleHit struct {
	ID        uint   `gorm:"primaryKey"`
	RuleID    string `gorm:"index"`
	ClusterID string `gorm:"index"`
	Namespace string
	Pod       string
	Count     int
	SeenAt    time.Time `gorm:"index"`
}

// GrafanaInstance is a Grafana added from Settings (instances from the
// secret are not stored). The service-account token is encrypted.
type GrafanaInstance struct {
	Name        string `gorm:"primaryKey"`
	URL         string
	TokenSealed string
	Last4       string
	UpdatedBy   string
	UpdatedAt   time.Time
}

// GrafanaMapping overrides where a control-hub cluster's metrics and logs live.
type GrafanaMapping struct {
	Cluster     string    `gorm:"primaryKey" json:"cluster"` // control-hub cluster name
	Instance    string    `json:"instance"`
	PromCluster string    `json:"promCluster"`
	LokiCluster string    `json:"lokiCluster"`
	UpdatedBy   string    `json:"updatedBy"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CostClusterDay is one cluster's compute cost for one UTC day, from the cost
// worker (internal/cost). Today's row is recomputed every hour.
type CostClusterDay struct {
	ID       uint   `gorm:"primaryKey" json:"-"`
	Day      string `gorm:"uniqueIndex:cost_cluster_day;size:10" json:"day"` // YYYY-MM-DD (UTC)
	Cluster  string `gorm:"uniqueIndex:cost_cluster_day" json:"cluster"`     // the Grafana/metrics cluster name
	Instance string `json:"instance"`                                        // Grafana instance
	Tenant   string `json:"tenant"`                                          // control-hub cluster name, when known

	NodeCost      float64 `json:"nodeCost"`      // what the nodes cost at list price
	AllocatedCost float64 `json:"allocatedCost"` // assigned to workloads
	IdleCost      float64 `json:"idleCost"`      // node cost no workload requested or used
	NodeHours     float64 `json:"nodeHours"`
	Nodes         int     `json:"nodes"`
	// EstimatedNodes are nodes priced with default per-vCPU/GiB rates because
	// their instance type or list price was unknown.
	EstimatedNodes int       `json:"estimatedNodes"`
	NodeTypes      string    `gorm:"type:text" json:"nodeTypes"` // JSON {"m6a.xlarge": 3}
	Hours          float64   `json:"hours"`                      // length of the day covered so far
	ComputedAt     time.Time `json:"computedAt"`
	// Rightsizing is set once right-sizing data was collected for the day (or
	// tried and failed), so the backfill does not redo the day forever.
	Rightsizing bool `json:"-"`
}

// CostWorkloadDay is one workload's cost for one day.
type CostWorkloadDay struct {
	ID        uint   `gorm:"primaryKey" json:"-"`
	Day       string `gorm:"index:cost_wl_day;size:10" json:"day"`
	Cluster   string `gorm:"index:cost_wl_day" json:"cluster"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"` // Deployment, StatefulSet, DaemonSet, CronJob, Job, Pod
	Name      string `json:"name"`

	CPUCost float64 `json:"cpuCost"`
	MemCost float64 `json:"memCost"`
	// Core-hours and GiB-hours: requested, used, and allocated (the larger of the two).
	CPURequest float64 `json:"cpuRequestCoreHours"`
	CPUUsage   float64 `json:"cpuUsageCoreHours"`
	MemRequest float64 `json:"memRequestGiBHours"`
	MemUsage   float64 `json:"memUsageGiBHours"`
	Pods       int     `json:"pods"`
}

// CostContainerDay is one workload container's resource usage for a day, per
// replica (the largest replica): the input of right-sizing recommendations.
type CostContainerDay struct {
	ID           uint    `gorm:"primaryKey" json:"-"`
	Day          string  `gorm:"index:cost_ct_day;size:10" json:"day"`
	Cluster      string  `gorm:"index:cost_ct_day" json:"cluster"`
	Namespace    string  `json:"namespace"`
	Kind         string  `json:"kind"`
	Name         string  `json:"name"`
	Container    string  `json:"container"`
	CPUP95       float64 `json:"cpuP95"` // cores
	CPUMax       float64 `json:"cpuMax"`
	MemMax       float64 `json:"memMax"` // bytes
	CPURequest   float64 `json:"cpuRequest"`
	CPULimit     float64 `json:"cpuLimit"`
	MemRequest   float64 `json:"memRequest"`
	MemLimit     float64 `json:"memLimit"`
	OOMKilled    bool    `json:"oomKilled"`
	ThrottledPct float64 `json:"throttledPct"`
	PodHours     float64 `json:"podHours"`
	Pods         int     `json:"pods"`
}

// LogPattern is an error message template no log rule matched (internal/patterns).
// The miner owns the counters; Status and RuleID are the admin's decision.
type LogPattern struct {
	ID        string `gorm:"primaryKey"`
	Service   string `gorm:"index"`
	Template  string `gorm:"type:text"`
	Example   string `gorm:"type:text"`
	Count     int
	Clusters  string `gorm:"type:text"` // JSON {cluster: lines}
	Hourly    string `gorm:"type:text"` // JSON {unix hour: lines}
	FirstSeen time.Time
	LastSeen  time.Time `gorm:"index"`
	Status    string    `gorm:"index"` // new, ignored, ruled
	RuleID    string
	UpdatedAt time.Time
}

// ReviewBatch is one Claude batch reviewing right-sizing recommendations.
type ReviewBatch struct {
	ID          string `gorm:"primaryKey"`
	SubmittedBy string // whose Claude key the poller uses
	SubmittedAt time.Time
	Status      string `gorm:"index"` // in_progress, ended, failed
	Requests    int
	Succeeded   int
	Errored     int
	EndedAt     *time.Time
	Error       string
}

// RightsizingReview is Claude's latest verdict on one recommendation, keyed by
// cluster|namespace|kind|name|container.
type RightsizingReview struct {
	Key        string `gorm:"primaryKey"`
	CustomID   string `gorm:"index"`
	BatchID    string `gorm:"index"`
	Status     string // pending, done, failed
	Verdict    string // safe, adjust, risky
	Reason     string `gorm:"type:text"`
	CPURequest float64
	MemRequest float64
	MemLimit   float64
	ReviewedAt *time.Time
}
