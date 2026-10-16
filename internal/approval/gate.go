package approval

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
)

type GuardResults struct {
	FactCheckPassed    bool
	PolicyPassed       bool
	SimilarityPassed   bool
	ConversationPassed bool
}

type Gate struct {
	maxCharacters int
}

func NewGate(maxCharacters int) *Gate {
	if maxCharacters <= 0 {
		maxCharacters = 280
	}
	return &Gate{maxCharacters: maxCharacters}
}

func (g *Gate) EvaluatePost(
	body string,
	document rules.Document,
	guards GuardResults,
) content.Approval {
	reasons := g.deterministicChecks(body, document)
	if !guards.FactCheckPassed {
		reasons = append(reasons, "fact check blocked the content")
	}
	if !guards.PolicyPassed {
		reasons = append(reasons, "policy check blocked the content")
	}
	if !guards.SimilarityPassed {
		reasons = append(reasons, "similarity check blocked the content")
	}
	return content.Approval{
		Approved:            len(reasons) == 0,
		Reasons:             reasons,
		FactCheckPassed:     guards.FactCheckPassed,
		PolicyPassed:        guards.PolicyPassed,
		SimilarityPassed:    guards.SimilarityPassed,
		DeterministicPassed: len(g.deterministicChecks(body, document)) == 0,
	}
}

func (g *Gate) EvaluateReply(
	body string,
	document rules.Document,
	guards GuardResults,
	eligible bool,
) content.Approval {
	result := g.EvaluatePost(body, document, guards)
	if !eligible {
		result.Reasons = append(result.Reasons, "mention is not eligible for a reply")
	}
	if !guards.ConversationPassed {
		result.Reasons = append(result.Reasons, "conversation guard blocked the reply")
	}
	for _, token := range strings.Fields(body) {
		if strings.HasPrefix(token, "http://") || strings.HasPrefix(token, "https://") {
			result.Reasons = append(result.Reasons, "replies cannot contain external URLs")
			break
		}
	}
	result.Approved = len(result.Reasons) == 0
	result.DeterministicPassed = result.DeterministicPassed && eligible && guards.ConversationPassed
	return result
}

func (g *Gate) deterministicChecks(body string, document rules.Document) []string {
	var reasons []string
	body = strings.TrimSpace(body)
	if body == "" {
		reasons = append(reasons, "content body is empty")
	}
	if utf8.RuneCountInString(body) > g.maxCharacters {
		reasons = append(reasons, fmt.Sprintf("content exceeds %d characters", g.maxCharacters))
	}
	lower := strings.ToLower(body)
	for _, topic := range document.Account.ForbiddenTopics {
		if topic != "" && strings.Contains(lower, strings.ToLower(topic)) {
			reasons = append(reasons, "content contains forbidden topic: "+topic)
		}
	}
	if !document.ContentPolicy.AllowURLs {
		for _, token := range strings.Fields(body) {
			if parsed, err := url.Parse(token); err == nil && parsed.Scheme != "" && parsed.Host != "" {
				reasons = append(reasons, "URLs are disabled")
				break
			}
		}
	}
	return reasons
}
