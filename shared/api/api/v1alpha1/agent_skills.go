package v1alpha1

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SkillList is Agent.spec.skills: the ids of the skills an agent selects.
//
// It is a plain list on the wire. Agents stored before the list existed hold
// the removed `{items: [...]}` wrapper instead, and the chart's
// migrate-skills-shape hook rewrites them after an upgrade. Until it has, and
// for anything that writes the old shape afterwards, UnmarshalJSON reads the
// wrapper as the same list - so the hub and the operator keep listing agents
// instead of failing on the whole list because one object is unmigrated.
//
// The CRD keeps spec.skills schemaless (x-kubernetes-preserve-unknown-fields)
// for the same reason: with `type: array` the API server prunes the wrapper's
// `items` on read, and the selection is lost before the migration sees it.
// Tighten both once every install has run the migration.
type SkillList []string

// UnmarshalJSON accepts a list of ids, null, or the legacy wrapper
// `{items: [...]}` (including `{}` and `{items: null}`, which mean no skills).
// Anything else - an object with other keys, a string, non-string ids - is an
// error, the same shapes the migration reports instead of rewriting.
func (l *SkillList) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*l = list
		return nil
	}
	var wrapper struct {
		Items *[]string `json:"items"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wrapper); err != nil {
		return fmt.Errorf("spec.skills must be a list of skill ids or the legacy {items: [...]} wrapper: %w", err)
	}
	if wrapper.Items == nil {
		*l = SkillList{}
		return nil
	}
	*l = *wrapper.Items
	return nil
}
