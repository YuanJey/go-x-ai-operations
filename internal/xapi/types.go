package xapi

import (
	"context"
	"time"
)

type CreatePostRequest struct {
	Text string `json:"text"`
}

type CreateReplyRequest struct {
	Text           string `json:"text"`
	ParentPostID   string `json:"parent_post_id"`
	ConversationID string `json:"conversation_id,omitempty"`
}

type PostResult struct {
	PostID      string    `json:"post_id"`
	Text        string    `json:"text"`
	PublishedAt time.Time `json:"published_at"`
	DryRun      bool      `json:"dry_run"`
}

type Mention struct {
	PostID            string    `json:"post_id"`
	AuthorID          string    `json:"author_id"`
	ConversationID    string    `json:"conversation_id"`
	Text              string    `json:"text"`
	CreatedAt         time.Time `json:"created_at"`
	ReferencedPostIDs []string  `json:"referenced_post_ids,omitempty"`
}

type MentionPage struct {
	Mentions    []Mention `json:"mentions"`
	NextCursor  string    `json:"next_cursor,omitempty"`
	ResultCount int       `json:"result_count"`
}

type OwnPost struct {
	PostID      string      `json:"post_id"`
	Text        string      `json:"text"`
	PublishedAt time.Time   `json:"published_at"`
	Metrics     PostMetrics `json:"metrics"`
}

type PostMetrics struct {
	Impressions int `json:"impressions"`
	Likes       int `json:"likes"`
	Replies     int `json:"replies"`
	Reposts     int `json:"reposts"`
	Quotes      int `json:"quotes"`
}

type AccountSnapshot struct {
	ID             string    `json:"id"`
	Username       string    `json:"username"`
	Name           string    `json:"name"`
	FollowersCount int       `json:"followers_count"`
	FollowingCount int       `json:"following_count"`
	PostCount      int       `json:"post_count"`
	CapturedAt     time.Time `json:"captured_at"`
}

type Client interface {
	CreatePost(ctx context.Context, request CreatePostRequest) (PostResult, error)
	CreateReply(ctx context.Context, request CreateReplyRequest) (PostResult, error)
	GetMentions(ctx context.Context, userID, cursor string) (MentionPage, error)
	GetOwnPosts(ctx context.Context, userID string, since time.Time) ([]OwnPost, error)
	GetAccount(ctx context.Context) (AccountSnapshot, error)
}

type Error struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	Uncertain  bool
	ResetAt    *time.Time
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) JobRetry() (bool, time.Time) {
	if e.Uncertain || !e.Retryable {
		return false, time.Time{}
	}
	if e.ResetAt != nil {
		return true, *e.ResetAt
	}
	return true, time.Time{}
}
