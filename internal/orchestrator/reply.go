package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YuanJey/go-x-ai-operations/internal/agent"
	"github.com/YuanJey/go-x-ai-operations/internal/approval"
	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/engagement"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	"github.com/YuanJey/go-x-ai-operations/internal/workflow"
)

type ReplyWorkflow struct {
	accountID  string
	agents     *agent.Runtime
	workflows  *workflow.Repository
	content    *content.Repository
	engagement *engagement.Repository
	gate       *approval.Gate
	rules      *rules.Repository
}

func NewReplyWorkflow(
	accountID string,
	agents *agent.Runtime,
	workflows *workflow.Repository,
	contentRepository *content.Repository,
	engagementRepository *engagement.Repository,
	gate *approval.Gate,
	ruleRepository *rules.Repository,
) *ReplyWorkflow {
	return &ReplyWorkflow{
		accountID: accountID, agents: agents, workflows: workflows,
		content: contentRepository, engagement: engagementRepository,
		gate: gate, rules: ruleRepository,
	}
}

func (w *ReplyWorkflow) Run(
	ctx context.Context,
	ruleVersionID, mentionID string,
	allowRemote bool,
) (content.Item, error) {
	version, err := w.rules.Active(ctx, w.accountID)
	if err != nil {
		return content.Item{}, err
	}
	if version.ID != ruleVersionID {
		return content.Item{}, fmt.Errorf("reply workflow must use active rules")
	}
	mention, err := w.engagement.GetMention(ctx, mentionID)
	if err != nil {
		return content.Item{}, err
	}
	if !mention.Summoned {
		return content.Item{}, fmt.Errorf("mention did not pass summoned-only eligibility")
	}
	run, err := w.workflows.Start(ctx, w.accountID, "reply", ruleVersionID, mention)
	if err != nil {
		return content.Item{}, err
	}
	fail := func(runErr error) (content.Item, error) {
		_ = w.workflows.Fail(ctx, run.ID, runErr)
		return content.Item{}, runErr
	}

	triageResult, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: run.ID, Definition: agent.ReplyTriageDefinition(),
		Input: mention, AllowRemote: allowRemote,
	})
	if err != nil {
		return fail(err)
	}
	var triage struct {
		Category  string `json:"category"`
		Sensitive bool   `json:"sensitive"`
	}
	if err := json.Unmarshal(triageResult.Output, &triage); err != nil {
		return fail(err)
	}
	decisionResult, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: run.ID, Definition: agent.ReplyDecisionDefinition(),
		Input:       map[string]any{"mention": mention, "triage": triage},
		AllowRemote: allowRemote,
	})
	if err != nil {
		return fail(err)
	}
	var decision struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(decisionResult.Output, &decision); err != nil {
		return fail(err)
	}
	riskJSON, _ := json.Marshal(triage)
	if err := w.engagement.SaveDecision(ctx, engagement.ReplyDecision{
		MentionID: mention.ID, Decision: decision.Decision,
		Reason: decision.Reason, RiskJSON: string(riskJSON),
	}); err != nil {
		return fail(err)
	}
	if decision.Decision != "reply" || triage.Sensitive {
		return fail(fmt.Errorf("reply decision is %s: %s", decision.Decision, decision.Reason))
	}

	writerResult, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: run.ID, Definition: agent.ReplyWriterDefinition(),
		Input: map[string]any{
			"mention": mention,
			"voice":   version.Document.Account.Voice,
		},
		AllowRemote: allowRemote,
	})
	if err != nil {
		return fail(err)
	}
	var candidate struct {
		Body   string   `json:"body"`
		Claims []string `json:"claims"`
	}
	if err := json.Unmarshal(writerResult.Output, &candidate); err != nil {
		return fail(err)
	}
	allowedHandles := map[string]bool{
		strings.TrimPrefix(strings.ToLower(version.Document.Account.Handle), "@"): true,
	}
	if mention.AuthorHandle != "" {
		allowedHandles[strings.TrimPrefix(strings.ToLower(mention.AuthorHandle), "@")] = true
	}
	if err := engagement.ValidateReplyParticipants(candidate.Body, allowedHandles); err != nil {
		return fail(err)
	}

	factPassed, err := runReplyGuard(ctx, w.agents, run.ID, agent.FactCheckDefinition(), map[string]any{
		"candidate": candidate, "thread": mention,
	}, allowRemote)
	if err != nil {
		return fail(err)
	}
	policyPassed, err := runReplyGuard(ctx, w.agents, run.ID, agent.PolicyDefinition(), map[string]any{
		"candidate": candidate, "policy": version.Document,
	}, allowRemote)
	if err != nil {
		return fail(err)
	}
	similarityPassed, err := runReplyGuard(ctx, w.agents, run.ID, agent.SimilarityDefinition(), map[string]any{
		"candidate": candidate,
	}, allowRemote)
	if err != nil {
		return fail(err)
	}
	conversationPassed, err := runReplyGuard(ctx, w.agents, run.ID, agent.ConversationGuardDefinition(), map[string]any{
		"candidate": candidate, "thread": mention, "allowed_handles": allowedHandles,
	}, allowRemote)
	if err != nil {
		return fail(err)
	}
	approvalResult := w.gate.EvaluateReply(candidate.Body, version.Document, approval.GuardResults{
		FactCheckPassed: factPassed, PolicyPassed: policyPassed,
		SimilarityPassed: similarityPassed, ConversationPassed: conversationPassed,
	}, mention.Summoned)
	if !approvalResult.Approved {
		return fail(fmt.Errorf("reply approval rejected: %v", approvalResult.Reasons))
	}
	item, err := w.content.SaveApproved(ctx, content.Item{
		AccountID: w.accountID, RuleVersionID: ruleVersionID,
		Kind: content.KindReply, Body: candidate.Body,
		BodyHash: hashBody(candidate.Body), Language: version.Document.Account.Language,
	}, approvalResult)
	if err != nil {
		return fail(err)
	}
	if err := w.engagement.CreateReply(ctx, w.accountID, mention.ID, item.ID); err != nil {
		return fail(err)
	}
	if err := w.workflows.Succeed(ctx, run.ID, item); err != nil {
		return content.Item{}, err
	}
	return item, nil
}

func runReplyGuard(
	ctx context.Context,
	runtime *agent.Runtime,
	workflowID string,
	definition agent.Definition,
	input any,
	allowRemote bool,
) (bool, error) {
	result, err := runtime.Run(ctx, agent.RunRequest{
		WorkflowID: workflowID, Definition: definition,
		Input: input, AllowRemote: allowRemote,
	})
	if err != nil {
		return false, err
	}
	var guard struct {
		Passed bool `json:"passed"`
	}
	if err := json.Unmarshal(result.Output, &guard); err != nil {
		return false, err
	}
	return guard.Passed, nil
}
