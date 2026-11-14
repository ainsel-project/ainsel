package skills

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"

	sharedskills "github.com/ainsel-project/ainsel/shared/api/skills"
)

// AgentLister reads and writes the Agent CRs that select a skill by ID.
//
// Selection lives on the agent alone: an AgentImage is a runtime profile and
// carries no skill list, so two agents on the same runtime can mount different
// libraries.
type AgentLister interface {
	ListReferrers(ctx context.Context, skillID string) ([]Referrer, error)
	// UsageCounts lists Agent CRs once and tallies how many agents select
	// each skill ID. A CR that lists the same skill ID more than once counts
	// once for that skill. It doubles as the enabled set: a skill with a
	// count of zero is selected by nobody and need not be delivered.
	UsageCounts(ctx context.Context) (map[string]int, error)
	// Assign adds a skill ID to an agent's spec.skills.
	Assign(ctx context.Context, skillID, agentName string) error
	// Unassign removes a skill ID from an agent's spec.skills.
	Unassign(ctx context.Context, skillID, agentName string) error
}

// ReferrerKindAgent is the Referrer.Kind value for an agent that selects a
// skill. Skills are per-agent, so this is the only kind a skill referrer has.
const ReferrerKindAgent = "agent"

// ManagedBy values. "user" rows are console-owned; "plugin" rows are written
// by the plugin skill syncer for skills a plugin manifest declares.
const (
	ManagedByUser   = "user"
	ManagedByPlugin = "plugin"
)

// ErrPluginManaged is the sentinel wrapped by every PluginManagedError, so a
// caller can test for plugin ownership without knowing the plugin id.
var ErrPluginManaged = errors.New("skill is managed by a plugin")

// PluginManagedError reports a skill row owned by a plugin. Its message is the
// exact wording the API answers with (409), so the handler passes it through
// verbatim.
type PluginManagedError struct {
	PluginID string
}

func (e PluginManagedError) Error() string {
	return fmt.Sprintf("managed by plugin %s", e.PluginID)
}

func (e PluginManagedError) Unwrap() error { return ErrPluginManaged }

// IsManaged reports whether a managedBy value marks the skill externally
// managed. Empty means console-managed (equivalent to "user"); any other
// value names an external author whose resource the console must not mutate.
// The rule is value-agnostic on purpose: "plugin" is only the value the hub's
// own syncer writes today, but "helm" or "gitops" rows are protected by the
// same rule without another code change.
func IsManaged(managedBy string) bool {
	return managedBy != "" && managedBy != ManagedByUser
}

// ManagedError is returned when a row is externally managed under a manager
// other than a plugin (see PluginManagedError for plugin-owned rows). Its
// message is the exact 409 wording the API answers with.
type ManagedError struct {
	Manager string
}

func (e ManagedError) Error() string {
	return fmt.Sprintf("skill is managed by %s", e.Manager)
}

// managedRefusal is the error an externally managed row answers user
// mutations with: the plugin wording for plugin-owned rows (naming the
// owning plugin), the generic manager wording otherwise.
func managedRefusal(row *Skill) error {
	if row.ManagedBy == ManagedByPlugin {
		return PluginManagedError{PluginID: pluginIDFromTags(row.Tags)}
	}
	return ManagedError{Manager: row.ManagedBy}
}

// pluginTagPrefix marks the tag that names a plugin skill's owner. The syncer
// writes exactly ["plugin:<id>"], and it — not the id — is the record of
// ownership a refusal quotes back.
const pluginTagPrefix = "plugin:"

// pluginIDFromTags recovers the owning plugin id from a row's tags. It returns
// the empty string for a row without the tag, which only a row not written by
// the syncer can be.
func pluginIDFromTags(tags []string) string {
	for _, t := range tags {
		if strings.HasPrefix(t, pluginTagPrefix) {
			return strings.TrimPrefix(t, pluginTagPrefix)
		}
	}
	return ""
}

// Service orchestrates Store + Reconciler + AgentLister.
type Service struct {
	store            *Store
	rec              *Reconciler
	agents AgentLister

	mu sync.Mutex
	// undelivered remembers why each skill was last reported as not
	// mounted, so a failure that repeats on the next retry is not logged
	// again. See logDeliveryChanges.
	undelivered map[string]string
}

// NewService wires a Service against its dependencies.
func NewService(s *Store, r *Reconciler, a AgentLister) *Service {
	return &Service{store: s, rec: r, agents: a}
}

// Reconciler exposes the underlying reconciler; primarily for tests.
func (s *Service) Reconciler() *Reconciler {
	return s.rec
}

// CreateRequest is the API-shape input to Create.
type CreateRequest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	GroupID     string   `json:"groupId"`
	Description string   `json:"description"`
	Body        string   `json:"body"`
	Tags        []string `json:"tags"`
}

// ValidationError is returned on invalid input. handlers map it to HTTP 400.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation: %s: %s", e.Field, e.Message)
}

// ErrInUse is returned by Delete when the skill has referrers.
type ErrInUse struct {
	Referrers []Referrer
}

func (e *ErrInUse) Error() string {
	return fmt.Sprintf("skill is in use by %d agent(s)", len(e.Referrers))
}

var slugRegex = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const (
	maxIDLen          = 64
	maxNameLen        = 200
	maxDescriptionLen = 2000
	maxBodyLen        = 100_000
	maxTags           = 10
	maxTagLen         = 50
)

func validateID(id string) error {
	if id == "" {
		return &ValidationError{Field: "id", Message: "is required"}
	}
	if len(id) > maxIDLen {
		return &ValidationError{Field: "id", Message: fmt.Sprintf("must be <= %d chars", maxIDLen)}
	}
	if !slugRegex.MatchString(id) {
		return &ValidationError{Field: "id", Message: "must be lowercase alphanumeric with hyphens, no leading/trailing/consecutive hyphens"}
	}
	return nil
}

func validateName(name string) error {
	if name == "" {
		return &ValidationError{Field: "name", Message: "is required"}
	}
	if len(name) > maxNameLen {
		return &ValidationError{Field: "name", Message: fmt.Sprintf("must be <= %d chars", maxNameLen)}
	}
	return nil
}

func validateDescription(d string) error {
	if len(d) > maxDescriptionLen {
		return &ValidationError{Field: "description", Message: fmt.Sprintf("must be <= %d chars", maxDescriptionLen)}
	}
	return nil
}

func validateBody(body string) error {
	if len(body) > maxBodyLen {
		return &ValidationError{Field: "body", Message: fmt.Sprintf("must be <= %d chars", maxBodyLen)}
	}
	return nil
}

func validateTags(tags []string) ([]string, error) {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(strings.ToLower(t))
		if t == "" {
			continue
		}
		if len(t) > maxTagLen {
			return nil, &ValidationError{Field: "tags", Message: fmt.Sprintf("each tag must be <= %d chars", maxTagLen)}
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		normalized = append(normalized, t)
	}
	// Count after normalization so empty/duplicate entries don't count toward
	// the limit (e.g. 11 empty strings normalize to zero tags, not a rejection).
	if len(normalized) > maxTags {
		return nil, &ValidationError{Field: "tags", Message: fmt.Sprintf("must have <= %d tags", maxTags)}
	}
	return normalized, nil
}

// RenderSkillMD builds the full SKILL.md content: YAML frontmatter
// followed by the body. It is the single place the hub turns a registry
// row into the file an agent reads, so the ConfigMap mount and the skill
// catalogue MCP cannot drift in how the same skill is presented.
//
// The description is emitted as a double-quoted YAML scalar via %q, which
// handles the escaping. Plain scalars break on ": " — which is how skill
// descriptions are routinely written ("Read pull requests: metadata,
// commits, ...") — and that invalid YAML cost discovery for every skill
// whose summary happened to contain a colon.
func RenderSkillMD(sk *Skill) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %q\n---\n%s", sk.ID, sk.Description, sk.Body)
}

// renderBestEffort writes one skill to the shared ConfigMap without
// letting delivery failure undo the write. Postgres is the source of
// truth and Converge re-applies whatever is missing, so a skill that is
// stored but not yet mounted is a degraded state rather than an invalid
// one.
func (s *Service) renderBestEffort(ctx context.Context, op string, sk *Skill) {
	if s.rec == nil {
		return
	}
	if err := s.rec.Ensure(ctx, sk); err != nil {
		slog.Warn("skills: configmap render failed; convergence pass will retry",
			"op", op, "skill_id", sk.ID, "err", err)
	}
}

// deliverIfEnabled renders a skill only when some agent selects it (the same
// set Converge keeps, via UsageCounts).
//
// The shared ConfigMap is sized to what agents mount, so writing a skill
// nobody has opted into spends ceiling and re-hashes the object, which
// the operator reads as a skill change and answers by restarting every
// skill-bearing agent. Catalogue writes must therefore stay out of it
// until an enable actually asks for the content.
func (s *Service) deliverIfEnabled(ctx context.Context, op string, sk *Skill) {
	if s.rec == nil || s.agents == nil {
		return
	}
	counts, err := s.agents.UsageCounts(ctx)
	if err != nil {
		// Not knowing the enabled set is not a reason to skip delivery:
		// an agent waiting on this content would stall on a transient
		// list failure. Write it; the next pass prunes it if unwanted.
		slog.Warn("skills: enabled-set lookup failed, rendering anyway",
			"op", op, "skill_id", sk.ID, "err", err)
		s.renderBestEffort(ctx, op, sk)
		return
	}
	if counts[sk.ID] < 1 {
		return
	}
	s.renderBestEffort(ctx, op, sk)
}

// Create persists a new skill. It deliberately does not render into the
// shared ConfigMap: nothing can be enabling a skill that did not exist a
// moment ago, so the entry would be pure catalogue weight until some
// agent assigned it. Delivery happens on Assign and on the next
// convergence pass.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Skill, error) {
	if err := validateID(req.ID); err != nil {
		return nil, err
	}
	if err := validateName(req.Name); err != nil {
		return nil, err
	}
	if err := validateDescription(req.Description); err != nil {
		return nil, err
	}
	if err := validateBody(req.Body); err != nil {
		return nil, err
	}
	tags, err := validateTags(req.Tags)
	if err != nil {
		return nil, err
	}

	sk := &Skill{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		Body:        req.Body,
		Tags:        tags,
	}
	if err := s.store.Create(ctx, sk); err != nil {
		return nil, err
	}
	return sk, nil
}

// Get returns a skill by ID.
func (s *Service) Get(ctx context.Context, id string) (*Skill, error) {
	return s.store.Get(ctx, id)
}

// List returns one page of skills (metadata only) plus the total number of
// rows matching the filter, optionally filtered. Each summary is enriched
// with UsedBy, the number of agents that select the skill. Usage is
// best-effort: if no lister is configured or the usage lookup fails, UsedBy
// is left 0 and the skills are still returned. Pagination is pushed into
// the store, so only the page's rows are read and enriched.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]SkillSummary, int, error) {
	summaries, total, err := s.store.List(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	if s.agents == nil {
		return summaries, total, nil
	}
	counts, err := s.agents.UsageCounts(ctx)
	if err != nil {
		slog.Error("skills: usage counts unavailable", "err", err)
		return summaries, total, nil
	}
	for i := range summaries {
		summaries[i].UsedBy = counts[summaries[i].ID]
	}
	return summaries, total, nil
}

// SearchForDiscovery returns skill metadata for the skill MCP, without
// the UsedBy enrichment List performs.
//
// The enrichment is a Kubernetes LIST of every Agent in the
// namespace. That is fine for an admin page loaded occasionally, but this
// method backs an agent-facing tool that a model may call repeatedly
// inside one task, and the answer it computes is one an agent has no use
// for: whether some other agent happens to mount the skill says nothing about
// whether the skill helps with the task at hand. Skipping it keeps the
// read to a single Postgres query.
//
// The full match set is returned: bounding it is the caller's job, and a
// caller that wants to say "there is more" needs to know whether there
// is, which a pre-truncated list cannot show.
func (s *Service) SearchForDiscovery(ctx context.Context, query string, tags []string) ([]SkillSummary, error) {
	summaries, _, err := s.store.List(ctx, ListFilter{Search: query, Tags: tags})
	return summaries, err
}

// Update applies a partial update. It re-renders the ConfigMap only when
// some agent selects the skill: an edit to a catalogue
// entry must not touch the shared, size-capped delivery object.
func (s *Service) Update(ctx context.Context, id string, req UpdateRequest) (*Skill, error) {
	// The stored row's owner decides: an externally managed row is refused,
	// so the mapping from a plugin (or any other author) to its shipped skill
	// has exactly one author.
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if IsManaged(row.ManagedBy) {
		return nil, managedRefusal(row)
	}
	if req.Name != nil {
		if err := validateName(*req.Name); err != nil {
			return nil, err
		}
	}
	if req.Description != nil {
		if err := validateDescription(*req.Description); err != nil {
			return nil, err
		}
	}
	if req.Body != nil {
		if err := validateBody(*req.Body); err != nil {
			return nil, err
		}
	}
	if req.Tags != nil {
		normalized, err := validateTags(*req.Tags)
		if err != nil {
			return nil, err
		}
		req.Tags = &normalized
	}
	updated, err := s.store.Update(ctx, id, req)
	if err != nil {
		return nil, err
	}
	s.deliverIfEnabled(ctx, "update", updated)
	return updated, nil
}

// ListManaged returns the full rows of every plugin-owned skill. It is the
// syncer's view of the registry: reconciling against this set alone never
// reads a user skill into the comparison.
func (s *Service) ListManaged(ctx context.Context) ([]Skill, error) {
	return s.store.ListManaged(ctx)
}

// UpsertManaged inserts or updates a plugin-owned skill under the id the
// caller derived from the manifest. Delivery follows the same rule as every
// other write: the content is mounted only when some agent enables it.
func (s *Service) UpsertManaged(ctx context.Context, sk *Skill) error {
	if err := s.store.UpsertManaged(ctx, sk); err != nil {
		return err
	}
	s.deliverIfEnabled(ctx, "plugin-upsert", sk)
	return nil
}

// DeleteManaged removes a plugin-owned row with no ownership check — the
// syncer's path, for rows that stopped being declared. The referrer check the
// user-facing Delete applies is deliberately skipped: an agent selecting a
// skill its plugin withdrew is the plugin's decision, and Converge prunes the
// mounted entry from there.
func (s *Service) DeleteManaged(ctx context.Context, id string) error {
	if err := s.store.DeleteManaged(ctx, id); err != nil {
		return err
	}
	if s.rec != nil {
		if err := s.rec.Delete(ctx, id); err != nil {
			return fmt.Errorf("delete configmap entry: %w", err)
		}
	}
	return nil
}

// Delete checks referrers first and refuses if any exist.
//
// Ordering: the DB row is deleted before the ConfigMap entry. If the
// ConfigMap update fails, the row is gone but the data key lingers. The
// orphaned key is harmless for read paths (no DB row → never listed/got)
// and gets overwritten if the same ID is recreated. A later reconcile
// loop is the intended cleanup path; for v1 we accept the asymmetry.
func (s *Service) Delete(ctx context.Context, id string) error {
	// Ownership first: a user cannot delete a managed skill at all, whatever
	// references it happens to hold.
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if IsManaged(row.ManagedBy) {
		return managedRefusal(row)
	}
	if s.agents != nil {
		refs, err := s.agents.ListReferrers(ctx, id)
		if err != nil {
			return fmt.Errorf("check referrers: %w", err)
		}
		if len(refs) > 0 {
			return &ErrInUse{Referrers: refs}
		}
	}
	if err := s.store.Delete(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	if s.rec != nil {
		if err := s.rec.Delete(ctx, id); err != nil {
			return fmt.Errorf("delete configmap entry: %w", err)
		}
	}
	return nil
}

// ListAssignments returns the agents that select the given skill.
func (s *Service) ListAssignments(ctx context.Context, skillID string) ([]Referrer, error) {
	if s.agents == nil {
		return nil, nil
	}
	return s.agents.ListReferrers(ctx, skillID)
}

// Assign adds the skill to an agent's spec.skills.
func (s *Service) Assign(ctx context.Context, skillID, agentName string) error {
	// Verify the skill exists.
	sk, err := s.store.Get(ctx, skillID)
	if err != nil {
		return err
	}
	if s.agents == nil {
		return fmt.Errorf("agent lister not configured")
	}
	if err := s.agents.Assign(ctx, skillID, agentName); err != nil {
		return err
	}
	// This is the moment the content becomes load-bearing: an agent that
	// just asked for the skill should not wait out a convergence tick to
	// get it. Render now; the pass remains the safety net for CRs edited
	// outside the hub and for renders that fail on size.
	s.renderBestEffort(ctx, "assign", sk)
	return nil
}

// Unassign removes the skill from an agent's spec.skills.
func (s *Service) Unassign(ctx context.Context, skillID, agentName string) error {
	// Verify the skill exists.
	if _, err := s.store.Get(ctx, skillID); err != nil {
		return err
	}
	if s.agents == nil {
		return fmt.Errorf("agent lister not configured")
	}
	return s.agents.Unassign(ctx, skillID, agentName)
}

// ConfigMapName returns the shared ConfigMap name for skills.
func ConfigMapName() string {
	return sharedskills.ConfigMapName
}

// DeliveryReport is the outcome of one Converge pass. Both ID lists are
// sorted: they come out of map iteration, and a report that shuffles
// between passes is useless for comparison or logging.
type DeliveryReport struct {
	Enabled     int      `json:"enabled"`
	Delivered   []string `json:"delivered"`
	Undelivered []string `json:"undelivered"`
}

// Converge reconciles the shared skills ConfigMap to exactly the skills
// that at least one agent enables via spec.skills.
//
// The ConfigMap is a single Kubernetes object and so carries the
// apiserver's 1 MiB ceiling. Treating it as a mirror of the whole
// registry made that ceiling a platform-wide cap on how many skills may
// exist, and — because the operator projects only the enabled keys —
// charged it against skills no agent had ever opted into. Mirroring the
// enabled set instead keeps the object sized to what agents actually
// mount.
//
// Catalogue skills that nothing enables are deliberately not delivered:
// they live in Postgres and are read through the API (and, in the shape
// of issue #300, the skill MCP) rather than mounted everywhere.
func (s *Service) Converge(ctx context.Context) (*DeliveryReport, error) {
	if s.rec == nil || s.agents == nil {
		return nil, fmt.Errorf("skills: converge requires a reconciler and an agent lister")
	}
	counts, err := s.agents.UsageCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("skills: converge enabled skill ids: %w", err)
	}
	summaries, _, err := s.store.List(ctx, ListFilter{})
	if err != nil {
		return nil, err
	}

	keep := make(map[string]*Skill, len(counts))
	for _, sum := range summaries {
		if counts[sum.ID] < 1 {
			continue
		}
		sk, err := s.store.Get(ctx, sum.ID)
		if err != nil {
			// A usage count with no row means an agent selects a
			// deleted skill; the operator projects a key that will never
			// exist. Skip it rather than abort the whole pass.
			slog.Warn("skills: enabled skill has no registry row", "skill_id", sum.ID, "err", err)
			continue
		}
		keep[sum.ID] = sk
	}

	delivered, undelivered, err := s.rec.Converge(ctx, keep)
	if err != nil {
		return nil, err
	}
	report := &DeliveryReport{Enabled: len(keep), Delivered: delivered}
	for id := range undelivered {
		report.Undelivered = append(report.Undelivered, id)
	}
	sort.Strings(report.Delivered)
	sort.Strings(report.Undelivered)
	s.logDeliveryChanges(delivered, undelivered, len(keep))
	return report, nil
}

// logDeliveryChanges reports undelivered skills whose situation *changed*
// and nothing else.
//
// Per skill, with the reason, because undelivered is a valid steady state
// and nothing alerts on it: this WARN line is the only place an operator
// learns which skill is not mounted, and that the shared object's size
// ceiling is what said so. It fires when a skill first fails to be
// delivered and again if the error changes. It deliberately does not fire
// on every pass: the delivery loop retries every 30 s while anything is
// undelivered, so an unconditional line would repeat the same message
// ~100k times a day for a full ConfigMap and bury the one that reports a
// new failure. The per-pass aggregate stays with runSkillsDelivery, which
// logs the counts and the retry interval.
//
// A skill that stops being reported without being delivered was disabled
// (or deleted) rather than fixed, so it is forgotten silently; only a skill
// that actually arrived gets the recovery line.
//
// The state is per hub replica. With hub.replicas > 1 two replicas run
// their own pass, so each logs its own first observation of a failure.
//
// Failures on the CRUD paths (Update and Assign, via renderBestEffort)
// are deliberately logged there and not folded in here: they answer a
// user action synchronously, and this dedup only understands whole-pass
// snapshots — calling it with one skill would forget the state of every
// other undelivered one. So a full ConfigMap legitimately produces a
// "configmap render failed" WARN on the write and an "enabled skill not
// delivered" WARN on the next pass. That is the intended shape, not a
// duplicate to delete.
func (s *Service) logDeliveryChanges(delivered []string, undelivered map[string]error, enabled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.undelivered == nil {
		s.undelivered = make(map[string]string)
	}

	ids := make([]string, 0, len(undelivered))
	for id := range undelivered {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		reason := undelivered[id].Error()
		if prev, seen := s.undelivered[id]; seen && prev == reason {
			continue
		}
		slog.Warn("skills: enabled skill not delivered to the configmap",
			"skill_id", id, "err", undelivered[id], "enabled", enabled)
		s.undelivered[id] = reason
	}

	arrived := make(map[string]bool, len(delivered))
	for _, id := range delivered {
		arrived[id] = true
	}
	for id := range s.undelivered {
		if _, still := undelivered[id]; still {
			continue
		}
		if arrived[id] {
			slog.Info("skills: enabled skill delivered after retry",
				"skill_id", id, "enabled", enabled)
		}
		delete(s.undelivered, id)
	}
}
