package content

import "time"

const (
	KindPost  = "post"
	KindReply = "reply"

	StateDraft        = "draft"
	StateApprovedAuto = "approved_auto"
	StateRuleAdmitted = "rule_admitted"
	StatePublished    = "published"
	StateRejected     = "rejected"
)

type Item struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"account_id"`
	TopicID       *string   `json:"topic_id,omitempty"`
	RuleVersionID string    `json:"rule_version_id"`
	Kind          string    `json:"kind"`
	Body          string    `json:"body"`
	BodyHash      string    `json:"body_hash"`
	Language      string    `json:"language"`
	State         string    `json:"state"`
	ApprovalJSON  string    `json:"approval_json,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Approval struct {
	Approved            bool     `json:"approved"`
	Reasons             []string `json:"reasons"`
	FactCheckPassed     bool     `json:"fact_check_passed"`
	PolicyPassed        bool     `json:"policy_passed"`
	SimilarityPassed    bool     `json:"similarity_passed"`
	DeterministicPassed bool     `json:"deterministic_passed"`
}
