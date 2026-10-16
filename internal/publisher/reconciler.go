package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/xapi"
)

type Reconciler struct {
	accountID  string
	userID     string
	client     xapi.Client
	operations *OperationRepository
}

func NewReconciler(
	accountID, userID string,
	client xapi.Client,
	operations *OperationRepository,
) *Reconciler {
	return &Reconciler{
		accountID: accountID, userID: userID,
		client: client, operations: operations,
	}
}

func (r *Reconciler) Reconcile(ctx context.Context) (int, error) {
	if r.userID == "" {
		account, err := r.client.GetAccount(ctx)
		if err != nil {
			return 0, err
		}
		r.userID = account.ID
	}
	operations, err := r.operations.ListUnresolved(ctx, r.accountID, 100)
	if err != nil {
		return 0, err
	}
	resolved := 0
	for _, operation := range operations {
		if operation.State == OperationUncertain {
			if err := r.operations.MarkReconciling(ctx, operation.ID); err != nil {
				return resolved, err
			}
		}
		if operation.SentAt == nil {
			if err := r.operations.Fail(
				ctx, operation.ID, OperationFailedAmbiguous, "",
				"operation has no send timestamp",
			); err != nil {
				return resolved, err
			}
			resolved++
			continue
		}
		posts, err := r.client.GetOwnPosts(ctx, r.userID, operation.SentAt.Add(-5*time.Minute))
		if err != nil {
			return resolved, err
		}
		var request struct {
			Text  string `json:"text"`
			Reply struct {
				ParentPostID string `json:"in_reply_to_tweet_id"`
			} `json:"reply"`
		}
		if err := json.Unmarshal([]byte(operation.RequestJSON), &request); err != nil {
			return resolved, fmt.Errorf("decode operation request: %w", err)
		}
		bodyHash := textHash(request.Text)
		var matches []xapi.OwnPost
		for _, post := range posts {
			if textHash(post.Text) == bodyHash &&
				post.PublishedAt.Before(operation.SentAt.Add(10*time.Minute)) {
				matches = append(matches, post)
			}
		}
		if len(matches) == 1 {
			kind := content.KindPost
			if operation.Action == "publish_reply" {
				kind = content.KindReply
			}
			if err := r.operations.Succeed(
				ctx, operation, matches[0].PostID, matches[0], kind,
				request.Reply.ParentPostID, bodyHash, matches[0].PublishedAt,
			); err != nil {
				return resolved, err
			}
			resolved++
			continue
		}
		reason := "no matching own post found"
		if len(matches) > 1 {
			reason = "multiple matching own posts found"
		}
		if err := r.operations.Fail(
			ctx, operation.ID, OperationFailedAmbiguous, "", reason,
		); err != nil {
			return resolved, err
		}
		resolved++
	}
	return resolved, nil
}

func textHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
