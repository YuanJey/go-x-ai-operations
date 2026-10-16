package engagement

import (
	"context"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/jobs"
	"github.com/YuanJey/go-x-ai-operations/internal/xapi"
)

type Collector struct {
	accountID     string
	accountUserID string
	accountHandle string
	client        xapi.Client
	repository    *Repository
	jobs          *jobs.Repository
}

func NewCollector(
	accountID, accountUserID, accountHandle string,
	client xapi.Client,
	repository *Repository,
	jobRepository *jobs.Repository,
) *Collector {
	return &Collector{
		accountID: accountID, accountUserID: accountUserID,
		accountHandle: accountHandle, client: client,
		repository: repository, jobs: jobRepository,
	}
}

func (c *Collector) Sync(
	ctx context.Context,
	ruleVersionID string,
) (int, error) {
	if c.accountUserID == "" {
		snapshot, err := c.client.GetAccount(ctx)
		if err != nil {
			return 0, err
		}
		c.accountUserID = snapshot.ID
	}
	ownPosts, err := c.client.GetOwnPosts(ctx, c.accountUserID, time.Time{})
	if err != nil {
		return 0, err
	}
	ownPostIDs := make(map[string]bool, len(ownPosts))
	for _, post := range ownPosts {
		ownPostIDs[post.PostID] = true
	}
	page, err := c.client.GetMentions(ctx, c.accountUserID, "")
	if err != nil {
		return 0, err
	}
	created := 0
	for _, external := range page.Mentions {
		eligibility := CheckEligibility(EligibilityInput{
			AccountHandle: c.accountHandle, AccountPostIDs: ownPostIDs,
			PostID: external.PostID, AuthorID: external.AuthorID,
			Text: external.Text, ReferencedPostIDs: external.ReferencedPostIDs,
		})
		mention, inserted, err := c.repository.SaveMention(
			ctx, c.accountID, "", external, eligibility,
		)
		if err != nil {
			return created, err
		}
		if !inserted || !eligibility.Eligible {
			continue
		}
		if _, err := c.jobs.Enqueue(ctx, MentionJob(mention, ruleVersionID)); err != nil {
			return created, fmt.Errorf("enqueue reply workflow: %w", err)
		}
		created++
	}
	return created, nil
}
