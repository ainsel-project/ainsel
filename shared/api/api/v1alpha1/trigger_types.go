// Trigger is NOT an installed CRD anymore: triggers live in the hub's Postgres
// tables (services/hub/internal/triggers) and the chart installs no CRD for
// them. The type is kept as the read contract for services/hub/cmd/migrate-triggers,
// which upgrades pre-Postgres clusters by reading leftover Trigger CRs. Do not
// reference it from reconcilers or the hub runtime; retiring migrate-triggers
// retires this file.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ainsel-project/ainsel/shared/api"
)

// TriggerSpec is part of the frozen Trigger read contract for
// cmd/migrate-triggers; triggers now live in hub Postgres. Do not extend.
type TriggerSpec struct {
	DisplayName  string                   `json:"displayName"`
	AgentRef     string                   `json:"agentRef"`
	ConnectorRef string                   `json:"connectorRef"`
	Filters      []ainselapishared.Filter `json:"filters,omitempty"`
}

// TriggerStatus is part of the frozen Trigger read contract for
// cmd/migrate-triggers; triggers now live in hub Postgres. Do not extend.
type TriggerStatus struct {
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
}

const (
	TriggerConditionAgentRefValid     = "AgentRefValid"
	TriggerConditionConnectorRefValid = "ConnectorRefValid"
)

// Trigger is the frozen read contract for cmd/migrate-triggers; triggers now
// live in hub Postgres. Do not extend — retiring migrate-triggers retires
// this type.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Agent",type=string,JSONPath=`.spec.agentRef`
// +kubebuilder:printcolumn:name="Connector",type=string,JSONPath=`.spec.connectorRef`
// +kubebuilder:printcolumn:name="Agent Valid",type=string,JSONPath=`.status.conditions[?(@.type=="AgentRefValid")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

type Trigger struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TriggerSpec   `json:"spec,omitempty"`
	Status TriggerStatus `json:"status,omitempty"`
}

// TriggerList is the list form of the frozen Trigger read contract for
// cmd/migrate-triggers; triggers now live in hub Postgres. Do not extend.
// +kubebuilder:object:root=true
type TriggerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Trigger `json:"items"`
}

