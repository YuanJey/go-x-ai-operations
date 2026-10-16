package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/YuanJey/go-x-ai-operations/internal/agent"
	"github.com/YuanJey/go-x-ai-operations/internal/approval"
	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	topicpkg "github.com/YuanJey/go-x-ai-operations/internal/topic"
	"github.com/YuanJey/go-x-ai-operations/internal/workflow"
)

type PostInput struct {
	Evidence      []EvidenceItem `json:"evidence"`
	RecentContent []string       `json:"recent_content,omitempty"`
}

type EvidenceItem struct {
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	SourceURL string `json:"source_url,omitempty"`
}

type PostWorkflow struct {
	accountID string
	agents    *agent.Runtime
	workflows *workflow.Repository
	content   *content.Repository
	gate      *approval.Gate
	rules     *rules.Repository
	topics    *topicpkg.Repository
}

func NewPostWorkflow(
	accountID string,
	agents *agent.Runtime,
	workflows *workflow.Repository,
	contentRepository *content.Repository,
	gate *approval.Gate,
	ruleRepository *rules.Repository,
	topicRepository *topicpkg.Repository,
) *PostWorkflow {
	return &PostWorkflow{
		accountID: accountID,
		agents:    agents,
		workflows: workflows,
		content:   contentRepository,
		gate:      gate,
		rules:     ruleRepository,
		topics:    topicRepository,
	}
}

func (w *PostWorkflow) Run(
	ctx context.Context,
	ruleVersionID string,
	input PostInput,
	allowRemote bool,
) (content.Item, error) {
	version, err := w.rules.Active(ctx, w.accountID)
	if err != nil {
		return content.Item{}, err
	}
	if version.ID != ruleVersionID {
		return content.Item{}, fmt.Errorf("post workflow must use the active rule version")
	}
	run, err := w.workflows.Start(ctx, w.accountID, "post", ruleVersionID, input)
	if err != nil {
		return content.Item{}, err
	}
	fail := func(runErr error) (content.Item, error) {
		_ = w.workflows.Fail(ctx, run.ID, runErr)
		return content.Item{}, runErr
	}
	if len(input.Evidence) == 0 {
		return fail(fmt.Errorf("post workflow requires at least one evidence item"))
	}

	topicResult, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: run.ID, Definition: agent.TopicDefinition(),
		Input: map[string]any{
			"account":  version.Document.Account,
			"pillars":  version.Document.ContentPolicy.Pillars,
			"evidence": input.Evidence,
		},
		AllowRemote: allowRemote,
	})
	if err != nil {
		return fail(err)
	}
	var topic struct {
		Title            string  `json:"title"`
		Pillar           string  `json:"pillar"`
		AudienceQuestion string  `json:"audience_question"`
		Score            float64 `json:"score"`
	}
	if err := json.Unmarshal(topicResult.Output, &topic); err != nil {
		return fail(err)
	}
	if !allowedPillar(version.Document.ContentPolicy.Pillars, topic.Pillar) {
		return fail(fmt.Errorf("topic selected an unsupported content pillar %q", topic.Pillar))
	}
	savedTopic, err := w.topics.Save(ctx, topicpkg.Topic{
		AccountID: w.accountID, Title: topic.Title, Pillar: topic.Pillar,
		Score: topic.Score, Rationale: topic.AudienceQuestion,
	})
	if err != nil {
		return fail(err)
	}

	writerResult, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: run.ID, Definition: agent.PostWriterDefinition(),
		Input: map[string]any{
			"topic":    topic,
			"evidence": input.Evidence,
			"voice":    version.Document.Account.Voice,
			"policy":   version.Document.ContentPolicy,
		},
		AllowRemote: allowRemote,
	})
	if err != nil {
		return fail(err)
	}
	var drafts struct {
		Candidates []struct {
			Body   string   `json:"body"`
			Claims []string `json:"claims"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(writerResult.Output, &drafts); err != nil {
		return fail(err)
	}
	if len(drafts.Candidates) != 2 {
		return fail(fmt.Errorf("post writer returned %d candidates", len(drafts.Candidates)))
	}
	candidate := drafts.Candidates[0]

	factPassed, err := w.runGuard(ctx, run.ID, agent.FactCheckDefinition(), map[string]any{
		"candidate": candidate,
		"evidence":  input.Evidence,
	}, allowRemote)
	if err != nil {
		return fail(err)
	}
	policyPassed, err := w.runGuard(ctx, run.ID, agent.PolicyDefinition(), map[string]any{
		"candidate":      candidate,
		"account_policy": version.Document,
	}, allowRemote)
	if err != nil {
		return fail(err)
	}
	similarityPassed, err := w.runGuard(ctx, run.ID, agent.SimilarityDefinition(), map[string]any{
		"candidate":      candidate,
		"recent_content": input.RecentContent,
	}, allowRemote)
	if err != nil {
		return fail(err)
	}
	if !factPassed || !policyPassed || !similarityPassed {
		return fail(fmt.Errorf(
			"content guards rejected candidate: fact_check=%t policy=%t similarity=%t",
			factPassed, policyPassed, similarityPassed,
		))
	}

	editorResult, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: run.ID, Definition: agent.PostEditorDefinition(),
		Input: map[string]any{
			"candidate": candidate,
			"guards": map[string]bool{
				"fact_check": factPassed,
				"policy":     policyPassed,
				"similarity": similarityPassed,
			},
		},
		AllowRemote: allowRemote,
	})
	if err != nil {
		return fail(err)
	}
	var edited struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(editorResult.Output, &edited); err != nil {
		return fail(err)
	}
	decision := w.gate.EvaluatePost(edited.Body, version.Document, approval.GuardResults{
		FactCheckPassed: factPassed, PolicyPassed: policyPassed,
		SimilarityPassed: similarityPassed,
	})
	if !decision.Approved {
		return fail(fmt.Errorf("content approval rejected: %v", decision.Reasons))
	}
	item, err := w.content.SaveApproved(ctx, content.Item{
		AccountID: w.accountID, RuleVersionID: ruleVersionID,
		TopicID: &savedTopic.ID,
		Kind:    content.KindPost, Body: edited.Body, BodyHash: hashBody(edited.Body),
		Language: version.Document.Account.Language,
	}, decision)
	if err != nil {
		return fail(err)
	}
	if err := w.workflows.Succeed(ctx, run.ID, item); err != nil {
		return content.Item{}, err
	}
	return item, nil
}

func (w *PostWorkflow) runGuard(
	ctx context.Context,
	workflowID string,
	definition agent.Definition,
	input any,
	allowRemote bool,
) (bool, error) {
	result, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: workflowID, Definition: definition,
		Input: input, AllowRemote: allowRemote,
	})
	if err != nil {
		return false, err
	}
	var guard struct {
		Passed  bool     `json:"passed"`
		Reasons []string `json:"reasons"`
	}
	if err := json.Unmarshal(result.Output, &guard); err != nil {
		return false, err
	}
	return guard.Passed, nil
}

func hashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func allowedPillar(pillars []rules.Pillar, name string) bool {
	for _, pillar := range pillars {
		if pillar.Name == name {
			return true
		}
	}
	return false
}
