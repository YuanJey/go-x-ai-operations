package xapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseSize = 4 << 20

type TokenSource interface {
	AccessToken(ctx context.Context) (string, error)
}

type HTTPClient interface {
	Do(request *http.Request) (*http.Response, error)
}

type HTTPAdapter struct {
	baseURL    string
	client     HTTPClient
	tokens     TokenSource
	accountID  string
	rateLimits *RateLimitStore
}

func (a *HTTPAdapter) ConfigureRateLimits(
	accountID string,
	store *RateLimitStore,
) {
	a.accountID = accountID
	a.rateLimits = store
}

func NewHTTPAdapter(baseURL string, client HTTPClient, tokens TokenSource) *HTTPAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPAdapter{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
		tokens:  tokens,
	}
}

func (a *HTTPAdapter) CreatePost(
	ctx context.Context,
	request CreatePostRequest,
) (PostResult, error) {
	return a.create(ctx, map[string]any{"text": request.Text})
}

func (a *HTTPAdapter) CreateReply(
	ctx context.Context,
	request CreateReplyRequest,
) (PostResult, error) {
	return a.create(ctx, map[string]any{
		"text":  request.Text,
		"reply": map[string]string{"in_reply_to_tweet_id": request.ParentPostID},
	})
}

func (a *HTTPAdapter) create(
	ctx context.Context,
	input map[string]any,
) (PostResult, error) {
	var output struct {
		Data struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := a.doJSON(ctx, http.MethodPost, "/tweets", nil, input, &output); err != nil {
		return PostResult{}, err
	}
	if output.Data.ID == "" {
		return PostResult{}, &Error{Message: "X returned an empty post ID"}
	}
	return PostResult{
		PostID: output.Data.ID, Text: output.Data.Text,
		PublishedAt: time.Now().UTC(),
	}, nil
}

func (a *HTTPAdapter) GetMentions(
	ctx context.Context,
	userID, cursor string,
) (MentionPage, error) {
	query := url.Values{
		"tweet.fields": {"author_id,conversation_id,created_at,referenced_tweets"},
		"max_results":  {"100"},
	}
	if cursor != "" {
		query.Set("pagination_token", cursor)
	}
	var output struct {
		Data []struct {
			ID             string    `json:"id"`
			AuthorID       string    `json:"author_id"`
			ConversationID string    `json:"conversation_id"`
			Text           string    `json:"text"`
			CreatedAt      time.Time `json:"created_at"`
			Referenced     []struct {
				ID string `json:"id"`
			} `json:"referenced_tweets"`
		} `json:"data"`
		Meta struct {
			NextToken   string `json:"next_token"`
			ResultCount int    `json:"result_count"`
		} `json:"meta"`
	}
	if err := a.doJSON(
		ctx, http.MethodGet, "/users/"+url.PathEscape(userID)+"/mentions",
		query, nil, &output,
	); err != nil {
		return MentionPage{}, err
	}
	page := MentionPage{
		NextCursor: output.Meta.NextToken, ResultCount: output.Meta.ResultCount,
	}
	for _, item := range output.Data {
		mention := Mention{
			PostID: item.ID, AuthorID: item.AuthorID,
			ConversationID: item.ConversationID, Text: item.Text,
			CreatedAt: item.CreatedAt,
		}
		for _, reference := range item.Referenced {
			mention.ReferencedPostIDs = append(mention.ReferencedPostIDs, reference.ID)
		}
		page.Mentions = append(page.Mentions, mention)
	}
	return page, nil
}

func (a *HTTPAdapter) GetOwnPosts(
	ctx context.Context,
	userID string,
	since time.Time,
) ([]OwnPost, error) {
	query := url.Values{
		"tweet.fields": {"created_at,public_metrics"},
		"max_results":  {"100"},
	}
	if !since.IsZero() {
		query.Set("start_time", since.UTC().Format(time.RFC3339))
	}
	var output struct {
		Data []struct {
			ID            string    `json:"id"`
			Text          string    `json:"text"`
			CreatedAt     time.Time `json:"created_at"`
			PublicMetrics struct {
				Impressions int `json:"impression_count"`
				Likes       int `json:"like_count"`
				Replies     int `json:"reply_count"`
				Reposts     int `json:"retweet_count"`
				Quotes      int `json:"quote_count"`
			} `json:"public_metrics"`
		} `json:"data"`
	}
	if err := a.doJSON(
		ctx, http.MethodGet, "/users/"+url.PathEscape(userID)+"/tweets",
		query, nil, &output,
	); err != nil {
		return nil, err
	}
	posts := make([]OwnPost, 0, len(output.Data))
	for _, item := range output.Data {
		posts = append(posts, OwnPost{
			PostID: item.ID, Text: item.Text, PublishedAt: item.CreatedAt,
			Metrics: PostMetrics{
				Impressions: item.PublicMetrics.Impressions,
				Likes:       item.PublicMetrics.Likes,
				Replies:     item.PublicMetrics.Replies,
				Reposts:     item.PublicMetrics.Reposts,
				Quotes:      item.PublicMetrics.Quotes,
			},
		})
	}
	return posts, nil
}

func (a *HTTPAdapter) GetAccount(ctx context.Context) (AccountSnapshot, error) {
	query := url.Values{"user.fields": {"public_metrics"}}
	var output struct {
		Data struct {
			ID            string `json:"id"`
			Username      string `json:"username"`
			Name          string `json:"name"`
			PublicMetrics struct {
				FollowersCount int `json:"followers_count"`
				FollowingCount int `json:"following_count"`
				PostCount      int `json:"tweet_count"`
			} `json:"public_metrics"`
		} `json:"data"`
	}
	if err := a.doJSON(ctx, http.MethodGet, "/users/me", query, nil, &output); err != nil {
		return AccountSnapshot{}, err
	}
	return AccountSnapshot{
		ID: output.Data.ID, Username: output.Data.Username, Name: output.Data.Name,
		FollowersCount: output.Data.PublicMetrics.FollowersCount,
		FollowingCount: output.Data.PublicMetrics.FollowingCount,
		PostCount:      output.Data.PublicMetrics.PostCount,
		CapturedAt:     time.Now().UTC(),
	}, nil
}

func (a *HTTPAdapter) doJSON(
	ctx context.Context,
	method, path string,
	query url.Values,
	input any,
	output any,
) error {
	token, err := a.tokens.AccessToken(ctx)
	if err != nil {
		return &Error{Message: "load access token: " + err.Error()}
	}
	endpoint := method + " " + path
	if a.rateLimits != nil {
		if err := a.rateLimits.Admit(ctx, a.accountID, endpoint, time.Now()); err != nil {
			return err
		}
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	target := a.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(request)
	if err != nil {
		return &Error{Message: err.Error(), Retryable: false, Uncertain: method == http.MethodPost}
	}
	defer response.Body.Close()
	if a.rateLimits != nil {
		_ = a.rateLimits.Observe(ctx, a.accountID, endpoint, map[string]string{
			"x-rate-limit-limit":     response.Header.Get("x-rate-limit-limit"),
			"x-rate-limit-remaining": response.Header.Get("x-rate-limit-remaining"),
			"x-rate-limit-reset":     response.Header.Get("x-rate-limit-reset"),
		}, time.Now())
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize))
	if err != nil {
		return &Error{Message: err.Error(), Uncertain: method == http.MethodPost}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		classified := classifyError(response, responseBody)
		if xError, ok := classified.(*Error); ok &&
			method == http.MethodPost &&
			response.StatusCode >= 500 {
			xError.Uncertain = true
		}
		return classified
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return &Error{Message: "decode X response: " + err.Error(), Uncertain: method == http.MethodPost}
	}
	return nil
}

func classifyError(response *http.Response, body []byte) error {
	message := http.StatusText(response.StatusCode)
	code := ""
	var envelope struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Type   string `json:"type"`
		Errors []struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Detail != "" {
			message = envelope.Detail
		} else if envelope.Title != "" {
			message = envelope.Title
		} else if len(envelope.Errors) > 0 {
			message = envelope.Errors[0].Message
			code = strconv.Itoa(envelope.Errors[0].Code)
		}
	}
	var resetAt *time.Time
	if reset := response.Header.Get("x-rate-limit-reset"); reset != "" {
		if unix, err := strconv.ParseInt(reset, 10, 64); err == nil {
			value := time.Unix(unix, 0).UTC()
			resetAt = &value
		}
	}
	status := response.StatusCode
	return &Error{
		StatusCode: status, Code: code, Message: fmt.Sprintf("X API: %s", message),
		Retryable: status == http.StatusTooManyRequests || status >= 500,
		ResetAt:   resetAt,
	}
}
