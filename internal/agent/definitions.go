package agent

import (
	"encoding/json"

	"github.com/YuanJey/go-x-ai-operations/internal/llm"
)

func TopicDefinition() Definition {
	return definition(
		"topic",
		"topic-v1",
		"Select one useful topic from the supplied account strategy and evidence. Do not invent facts.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["title","pillar","audience_question","score"],
			"properties":{
				"title":{"type":"string","minLength":1,"maxLength":160},
				"pillar":{"type":"string","minLength":1,"maxLength":80},
				"audience_question":{"type":"string","minLength":1,"maxLength":240},
				"score":{"type":"number","minimum":0,"maximum":1}
			}
		}`,
	)
}

func PostWriterDefinition() Definition {
	return definition(
		"post_writer",
		"post-writer-v1",
		"Write exactly two concise X post candidates grounded only in the supplied topic and evidence. List every factual claim.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["candidates"],
			"properties":{
				"candidates":{
					"type":"array","minItems":2,"maxItems":2,
					"items":{
						"type":"object","additionalProperties":false,
						"required":["body","claims"],
						"properties":{
							"body":{"type":"string","minLength":1,"maxLength":280},
							"claims":{"type":"array","items":{"type":"string","maxLength":240}}
						}
					}
				}
			}
		}`,
	)
}

func FactCheckDefinition() Definition {
	return guardDefinition(
		"fact_check",
		"fact-check-v1",
		"Verify every factual claim against the supplied evidence. Reject unsupported claims.",
	)
}

func PolicyDefinition() Definition {
	return guardDefinition(
		"policy",
		"policy-v1",
		"Apply the supplied platform and account policy. You may only tighten policy, never waive it.",
	)
}

func SimilarityDefinition() Definition {
	return guardDefinition(
		"similarity",
		"similarity-v1",
		"Compare the candidate to the supplied recent content. Reject semantic duplication or templated repetition.",
	)
}

func PostEditorDefinition() Definition {
	return definition(
		"post_editor",
		"post-editor-v1",
		"Edit the approved candidate for clarity without adding facts, URLs, mentions, or claims.",
		`{
			"type":"object",
			"additionalProperties":false,
			"required":["body"],
			"properties":{"body":{"type":"string","minLength":1,"maxLength":280}}
		}`,
	)
}

func ReplyTriageDefinition() Definition {
	return definition(
		"mention_triage",
		"mention-triage-v1",
		"Classify the eligible summoned mention. Sensitive or abusive requests should not receive an automated reply.",
		`{
			"type":"object","additionalProperties":false,
			"required":["category","sensitive"],
			"properties":{
				"category":{"enum":["question","feedback","collaboration","abuse","no_reply"]},
				"sensitive":{"type":"boolean"}
			}
		}`,
	)
}

func ReplyDecisionDefinition() Definition {
	return definition(
		"reply_decision",
		"reply-decision-v1",
		"Choose reply, ignore, or escalate. Ignore low-value requests; escalate sensitive requests.",
		`{
			"type":"object","additionalProperties":false,
			"required":["decision","reason"],
			"properties":{
				"decision":{"enum":["reply","ignore","escalate"]},
				"reason":{"type":"string","minLength":1,"maxLength":240}
			}
		}`,
	)
}

func ReplyWriterDefinition() Definition {
	return definition(
		"reply_writer",
		"reply-writer-v1",
		"Write one direct reply grounded in the current thread. Do not add URLs, marketing language, or new participants.",
		`{
			"type":"object","additionalProperties":false,
			"required":["body","claims"],
			"properties":{
				"body":{"type":"string","minLength":1,"maxLength":280},
				"claims":{"type":"array","items":{"type":"string","maxLength":240}}
			}
		}`,
	)
}

func ConversationGuardDefinition() Definition {
	return guardDefinition(
		"conversation_guard",
		"conversation-guard-v1",
		"Verify the reply is consistent with the thread, does not expose private data, and mentions no new participants.",
	)
}

func MetricsAnalystDefinition() Definition {
	return definition(
		"metrics_analyst",
		"metrics-analyst-v1",
		"Analyze the supplied account and post metrics. Distinguish observed values from estimates. Recommend content-pillar weights, but never change action quotas or schedules.",
		`{
			"type":"object","additionalProperties":false,
			"required":["observations","recommended_pillar_weights","confidence"],
			"properties":{
				"observations":{"type":"array","items":{"type":"string","maxLength":300}},
				"recommended_pillar_weights":{
					"type":"object","additionalProperties":{"type":"number","minimum":0,"maximum":1}
				},
				"confidence":{"type":"number","minimum":0,"maximum":1}
			}
		}`,
	)
}

func ExperimentDefinition() Definition {
	return definition(
		"experiment",
		"experiment-v1",
		"Evaluate whether the supplied evidence is sufficient for a strategy recommendation. Never claim a winner with insufficient samples.",
		`{
			"type":"object","additionalProperties":false,
			"required":["recommendation","reason","sample_sufficient"],
			"properties":{
				"recommendation":{"enum":["continue","stop","expand","insufficient_data"]},
				"reason":{"type":"string","minLength":1,"maxLength":400},
				"sample_sufficient":{"type":"boolean"}
			}
		}`,
	)
}

func guardDefinition(agentType, promptVersion, prompt string) Definition {
	return definition(
		agentType,
		promptVersion,
		prompt,
		`{
			"type":"object","additionalProperties":false,
			"required":["passed","reasons"],
			"properties":{
				"passed":{"type":"boolean"},
				"reasons":{"type":"array","items":{"type":"string","maxLength":240}}
			}
		}`,
	)
}

func definition(agentType, promptVersion, prompt, schema string) Definition {
	return Definition{
		Type:                agentType,
		PromptVersion:       promptVersion,
		SystemPrompt:        prompt,
		InputSchemaVersion:  "1",
		OutputSchemaVersion: "1",
		OutputSchema:        json.RawMessage(schema),
		Required:            llm.Capabilities{JSONObject: true},
		MaxOutputTokens:     1024,
		Temperature:         0.2,
	}
}
