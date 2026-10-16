package rules

import "sort"

const (
	TriggerQuota    = "quota"
	TriggerInterval = "interval"
	TriggerCron     = "cron"
	TriggerDelayed  = "delayed"
)

type ActionDefinition struct {
	Name           string
	RequiredScopes []string
	TriggerKinds   []string
}

type Registry struct {
	definitions map[string]ActionDefinition
}

func DefaultRegistry() Registry {
	items := []ActionDefinition{
		{Name: "source_collect", TriggerKinds: []string{TriggerInterval}},
		{Name: "topic_generate", TriggerKinds: []string{TriggerQuota}},
		{Name: "publish_post", RequiredScopes: []string{"tweet.write"}, TriggerKinds: []string{TriggerQuota}},
		{Name: "sync_mentions", RequiredScopes: []string{"tweet.read", "users.read"}, TriggerKinds: []string{TriggerInterval}},
		{Name: "publish_reply", RequiredScopes: []string{"tweet.write"}, TriggerKinds: []string{TriggerQuota}},
		{Name: "sync_account_metrics", RequiredScopes: []string{"tweet.read", "users.read"}, TriggerKinds: []string{TriggerInterval}},
		{Name: "sync_post_metrics", RequiredScopes: []string{"tweet.read"}, TriggerKinds: []string{TriggerDelayed}},
		{Name: "update_strategy", TriggerKinds: []string{TriggerCron}},
	}
	definitions := make(map[string]ActionDefinition, len(items))
	for _, item := range items {
		definitions[item.Name] = item
	}
	return Registry{definitions: definitions}
}

func (r Registry) Get(name string) (ActionDefinition, bool) {
	item, ok := r.definitions[name]
	return item, ok
}

func (r Registry) Names() []string {
	names := make([]string, 0, len(r.definitions))
	for name := range r.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
