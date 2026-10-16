// Package tasklogs provides storage and retrieval of structured log entries
// published by agents during task execution. Entries are persisted in
// Postgres so the hub's observability endpoints work without an external
// log backend required.
package tasklogs

import "time"

// Level values for a log entry.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Entry is a single structured log line published by an agent.
type Entry struct {
	ID            int64          `json:"id"`
	InvocationID  string         `json:"invocationId,omitempty"`
	CorrelationID string         `json:"correlationId,omitempty"`
	AgentName     string         `json:"agentName"`
	Level         string         `json:"level"`
	Message       string         `json:"message"`
	Fields        map[string]any `json:"fields,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
}

// ListOptions filters and paginates List results.
type ListOptions struct {
	AgentName string
	// AgentNames restricts results to any of these agents. Used to scope the
	// log view to the agents a caller may read; an empty slice applies no
	// restriction, so callers that mean "no agents at all" must not query.
	AgentNames []string
	Level      string
	Since      time.Time
	Until      time.Time
	Limit      int
}

// TokenSubjectUsageRow is one (agent_name, repo, event_type, model) group of
// the four token components pi reports plus the priced cost, summed over a
// window. The accounting rule is TokenUsageRow's: callers that need a single
// figure sum all four components (see api.tokenTotal).
//
// Rows written before migration 0024 carry no subject — Repo and EventType
// are "" there. They are still consumption, so readers must render their
// subject as unknown rather than drop them, or the table stops adding up
// against the token summary over the same window.
type TokenSubjectUsageRow struct {
	AgentName        string
	Repo             string
	EventType        string
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
}

// ConversationListOptions filters conversation message queries.
type ConversationListOptions struct {
	// AgentName restricts results to a single agent.
	AgentName string
	// AgentNames restricts results to any of these agents, for scoping to the
	// agents a caller may read. Empty applies no restriction.
	AgentNames    []string
	InvocationID  string
	CorrelationID string
	// Limit caps the number of messages returned. <= 0 means the default.
	Limit int
}

// ConversationMessage is a single message in an agent conversation,
// captured from the pi RPC event stream (assistant responses).
//
// The four token fields mirror the components pi reports (usage.input
// excludes prompt-cache traffic; totalTokens is the sum of all four), so a
// window summed from these rows is comparable with the Prometheus
// agent_tokens_used_total figures. Older runtimes that predate cache
// reporting leave the cache fields at zero.
type ConversationMessage struct {
	ID               int64  `json:"id"`
	InvocationID     string `json:"invocationId,omitempty"`
	CorrelationID    string `json:"correlationId,omitempty"`
	AgentName        string `json:"agentName"`
	Role             string `json:"role"`
	Content          string `json:"content"`
	Model            string `json:"model,omitempty"`
	InputTokens      int    `json:"inputTokens,omitempty"`
	OutputTokens     int    `json:"outputTokens,omitempty"`
	CacheReadTokens  int    `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens int    `json:"cacheWriteTokens,omitempty"`
	// CostUSD is pi's usage.cost.total for this message, priced from the
	// model's cost block in the generated models.json (spec.llm.cost). Zero
	// for runtimes that predate cost reporting and for models priced at zero.
	CostUSD    float64 `json:"costUsd,omitempty"`
	StopReason string  `json:"stopReason,omitempty"`
	// Subject labels the token tables group by (issue #410): the normalised
	// event context the runner held when the turn ran, reported on the same
	// payload as the token components so the counter and the row cannot
	// disagree. Rows written before migration 0024 keep the zero values —
	// readers must render those as "unknown", never drop the row.
	Repo        string    `json:"repo,omitempty"`
	Org         string    `json:"org,omitempty"`
	EventType   string    `json:"eventType,omitempty"`
	IssueNumber int       `json:"issueNumber,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// TokenUsageByIssueFilter selects rows for Store.TokenUsageByIssue. Zero
// values mean "no restriction" — except IssueNumber, which is a pointer
// because issue_number = 0 is a real value (rows whose subject was unknown
// when written, pre-migration-0024) and must never double as the no-filter
// marker.
type TokenUsageByIssueFilter struct {
	// Since/Until bound the window as [since, until); a zero bound is
	// unbounded, which reads "everything the store still retains" — the
	// conversations table has its own retention loop (ConversationRetention),
	// the same semantics TokenUsageSince documents.
	Since time.Time
	Until time.Time
	// AgentName and Repo are exact matches; "" applies no filter.
	AgentName string
	Repo      string
	// IssueNumber is an exact match; nil applies no filter.
	IssueNumber *int
}

// TokenIssueUsageRow is one (agent_name, repo, issue_number, model) group of
// the four token components pi reports plus the priced cost, summed over a
// window. The accounting rule is TokenUsageRow's: callers that need a single
// figure sum all four components (see api.tokenTotal).
//
// Rows written before migration 0024 carry no subject — Repo is "" and
// IssueNumber is 0 there. They are still consumption, so readers must render
// their subject as unknown rather than drop them, or the rows stop adding up
// against the token summary over the same window.
type TokenIssueUsageRow struct {
	AgentName        string
	Repo             string
	IssueNumber      int
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	CostUSD          float64
}
