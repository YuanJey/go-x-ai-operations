package engagement

import "time"

type Mention struct {
	ID             string    `json:"id"`
	AccountID      string    `json:"account_id"`
	XPostID        string    `json:"x_post_id"`
	AuthorID       string    `json:"author_id"`
	AuthorHandle   string    `json:"author_handle,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Text           string    `json:"text"`
	ParticipantIDs []string  `json:"participant_ids"`
	Summoned       bool      `json:"summoned"`
	SummonEvidence string    `json:"summon_evidence,omitempty"`
	ReceivedAt     time.Time `json:"received_at"`
	State          string    `json:"state"`
}

type EligibilityInput struct {
	AccountHandle     string
	AccountPostIDs    map[string]bool
	PostID            string
	AuthorID          string
	Text              string
	ReferencedPostIDs []string
	ParticipantIDs    []string
}

type Eligibility struct {
	Eligible bool   `json:"eligible"`
	Evidence string `json:"evidence,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type ReplyDecision struct {
	ID        string    `json:"id"`
	MentionID string    `json:"mention_id"`
	Decision  string    `json:"decision"`
	Reason    string    `json:"reason"`
	RiskJSON  string    `json:"risk_json"`
	CreatedAt time.Time `json:"created_at"`
}
