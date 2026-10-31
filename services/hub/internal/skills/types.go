// Package skills owns the skill registry: persistence (Store),
// ConfigMap rendering (Reconciler), and the Service facade that wires
// the two together for handlers.
package skills

import "time"

// Skill is the canonical skill record from the database.
type Skill struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Body        string    `json:"body"`
	Tags        []string  `json:"tags"`
	ManagedBy   string    `json:"managedBy"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// SkillSummary is the listing projection: same as Skill without
// the (potentially large) body. UsedBy counts how many AgentImage
// CRs reference the skill via spec.enabledSkills; it is derived at
// list time (not stored) and left 0 when no lister is configured.
type SkillSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	// ManagedBy mirrors Skill.ManagedBy so list views can render externally
	// managed skills read-only without fetching each detail.
	ManagedBy string    `json:"managedBy"`
	UsedBy    int       `json:"usedBy"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Referrer identifies an AgentImage CR that references a skill by ID.
// Returned by Service.Delete when the delete is refused.
type Referrer struct {
	AgentImageName string `json:"agentImageName"`
	// AgentImageID is the image's API id (its resource name) — the route key
	// the console links a referrer onto. Same value as AgentImageName today;
	// carried explicitly so views never have to assume it.
	AgentImageID string `json:"agentImageId,omitempty"`
}
