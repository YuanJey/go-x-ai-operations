package xapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

type DryRunAdapter struct {
	mu       sync.Mutex
	posts    []OwnPost
	mentions []Mention
	account  AccountSnapshot
}

func NewDryRunAdapter(handle string) *DryRunAdapter {
	return &DryRunAdapter{account: AccountSnapshot{
		ID: "dry-run-user", Username: handle, Name: "Dry Run Account",
		CapturedAt: time.Now().UTC(),
	}}
}

func (a *DryRunAdapter) CreatePost(
	_ context.Context,
	request CreatePostRequest,
) (PostResult, error) {
	return a.create(request.Text)
}

func (a *DryRunAdapter) CreateReply(
	_ context.Context,
	request CreateReplyRequest,
) (PostResult, error) {
	return a.create(request.Text)
}

func (a *DryRunAdapter) create(text string) (PostResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte(text + now.Format(time.RFC3339Nano)))
	postID := "dry_" + hex.EncodeToString(sum[:8])
	a.posts = append(a.posts, OwnPost{PostID: postID, Text: text, PublishedAt: now})
	a.account.PostCount++
	return PostResult{PostID: postID, Text: text, PublishedAt: now, DryRun: true}, nil
}

func (a *DryRunAdapter) GetMentions(
	_ context.Context,
	_, _ string,
) (MentionPage, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	mentions := append([]Mention(nil), a.mentions...)
	return MentionPage{Mentions: mentions, ResultCount: len(mentions)}, nil
}

func (a *DryRunAdapter) GetOwnPosts(
	_ context.Context,
	_ string,
	since time.Time,
) ([]OwnPost, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var posts []OwnPost
	for _, post := range a.posts {
		if since.IsZero() || post.PublishedAt.After(since) {
			posts = append(posts, post)
		}
	}
	return posts, nil
}

func (a *DryRunAdapter) GetAccount(_ context.Context) (AccountSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot := a.account
	snapshot.CapturedAt = time.Now().UTC()
	return snapshot, nil
}

func (a *DryRunAdapter) AddMention(mention Mention) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mentions = append(a.mentions, mention)
}
