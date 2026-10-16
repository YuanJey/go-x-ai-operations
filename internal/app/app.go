package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/account"
	"github.com/YuanJey/go-x-ai-operations/internal/agent"
	"github.com/YuanJey/go-x-ai-operations/internal/alert"
	"github.com/YuanJey/go-x-ai-operations/internal/analytics"
	"github.com/YuanJey/go-x-ai-operations/internal/approval"
	"github.com/YuanJey/go-x-ai-operations/internal/audit"
	"github.com/YuanJey/go-x-ai-operations/internal/backup"
	"github.com/YuanJey/go-x-ai-operations/internal/config"
	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/credentials"
	"github.com/YuanJey/go-x-ai-operations/internal/engagement"
	"github.com/YuanJey/go-x-ai-operations/internal/httpapi"
	"github.com/YuanJey/go-x-ai-operations/internal/jobs"
	"github.com/YuanJey/go-x-ai-operations/internal/llm"
	"github.com/YuanJey/go-x-ai-operations/internal/metering"
	"github.com/YuanJey/go-x-ai-operations/internal/oauth"
	"github.com/YuanJey/go-x-ai-operations/internal/orchestrator"
	"github.com/YuanJey/go-x-ai-operations/internal/outbox"
	"github.com/YuanJey/go-x-ai-operations/internal/publisher"
	"github.com/YuanJey/go-x-ai-operations/internal/quota"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	"github.com/YuanJey/go-x-ai-operations/internal/scheduler"
	"github.com/YuanJey/go-x-ai-operations/internal/source"
	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
	"github.com/YuanJey/go-x-ai-operations/internal/topic"
	"github.com/YuanJey/go-x-ai-operations/internal/worker"
	"github.com/YuanJey/go-x-ai-operations/internal/workflow"
	"github.com/YuanJey/go-x-ai-operations/internal/xapi"
)

type App struct {
	Config     config.Config
	Store      *sqlitestore.Store
	HTTP       *httpapi.Server
	Scheduler  *scheduler.Scheduler
	Worker     *worker.Runner
	Outbox     *outbox.Dispatcher
	Reconciler *publisher.Reconciler
	Backup     *backup.Service
	Alerts     *alert.Service
	logger     *slog.Logger
}

func Build(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	store, err := sqlitestore.Open(ctx, cfg.Storage.DatabasePath)
	if err != nil {
		return nil, err
	}
	fail := func(buildErr error) (*App, error) {
		store.Close()
		return nil, buildErr
	}

	ruleRepository := rules.NewRepository(store.DB)
	ruleService := rules.NewService(
		rules.NewValidator(rules.DefaultRegistry()),
		ruleRepository,
		cfg.Account.ID,
	)
	if _, err := ruleService.Bootstrap(
		ctx, cfg.Rules.BootstrapFile, cfg.Account.Handle, cfg.Account.Timezone,
	); err != nil {
		return fail(err)
	}

	profiles, err := providerProfiles(cfg)
	if err != nil {
		return fail(err)
	}
	llmRepository := llm.NewRepository(store.DB)
	if err := llmRepository.SyncProfiles(ctx, profiles); err != nil {
		return fail(err)
	}
	httpClient := &http.Client{Timeout: cfg.LLM.RequestTimeout.Value()}
	keychain := credentials.NewKeychain()
	gateway, err := llm.NewGateway(llm.GatewayConfig{
		Profiles: profiles, DefaultRoute: cfg.LLM.Routing.Default,
		AgentRoutes:    cfg.LLM.Routing.Agents,
		RequestTimeout: cfg.LLM.RequestTimeout.Value(),
		Secrets:        keychain, Usage: llmRepository,
		Adapters: []llm.Adapter{
			llm.NewOpenAICompatibleAdapter(httpClient),
			llm.NewOllamaAdapter(httpClient),
			llm.NewAnthropicAdapter(httpClient),
			llm.NewGeminiAdapter(httpClient),
		},
	})
	if err != nil {
		return fail(err)
	}

	var xClient xapi.Client
	var oauthManager *oauth.Manager
	if cfg.Runtime.DryRun {
		xClient = xapi.NewDryRunAdapter(cfg.Account.Handle)
	} else {
		oauthManager, err = buildOAuth(cfg, keychain, httpClient)
		if err != nil {
			return fail(err)
		}
		httpAdapter := xapi.NewHTTPAdapter(cfg.X.BaseURL, httpClient, oauthManager)
		httpAdapter.ConfigureRateLimits(
			cfg.Account.ID, xapi.NewRateLimitStore(store.DB),
		)
		xClient = httpAdapter
	}

	jobRepository := jobs.NewRepository(store.DB)
	contentRepository := content.NewRepository(store.DB)
	engagementRepository := engagement.NewRepository(store.DB)
	workflowRepository := workflow.NewRepository(store.DB)
	agentRuntime := agent.NewRuntime(gateway, agent.NewRepository(store.DB))
	approvalGate := approval.NewGate(280)
	postWorkflow := orchestrator.NewPostWorkflow(
		cfg.Account.ID, agentRuntime, workflowRepository,
		contentRepository, approvalGate, ruleRepository,
		topic.NewRepository(store.DB),
	)
	replyWorkflow := orchestrator.NewReplyWorkflow(
		cfg.Account.ID, agentRuntime, workflowRepository, contentRepository,
		engagementRepository, approvalGate, ruleRepository,
	)
	analyticsWorkflow := orchestrator.NewAnalyticsWorkflow(
		store.DB, cfg.Account.ID, agentRuntime, workflowRepository,
	)
	operationRepository := publisher.NewOperationRepository(store.DB)
	quotaCounter := quota.NewCounter(store.DB)
	postPublisher := publisher.NewPostPublisher(
		store.DB, xClient, quotaCounter, operationRepository, cfg.Account.ID,
	)
	replyPublisher := publisher.NewReplyPublisher(
		store.DB, xClient, quotaCounter, operationRepository, cfg.Account.ID,
	)
	meteringRepository := metering.NewRepository(store.DB)
	postPublisher.ConfigureMetering(
		meteringRepository, cfg.Metering.XUnitCost, cfg.Metering.Currency,
	)
	replyPublisher.ConfigureMetering(
		meteringRepository, cfg.Metering.XUnitCost, cfg.Metering.Currency,
	)
	analyticsService := analytics.NewService(store.DB, xClient, cfg.Account.ID)
	alertService := alert.NewService(store.DB, cfg.Account.ID)
	sourceRepository := source.NewRepository(store.DB)
	sourceCollector := source.NewCollector(
		sourceRepository, httpClient, cfg.Account.ID, cfg.Sources,
	)
	mentionCollector := engagement.NewCollector(
		cfg.Account.ID, "", cfg.Account.Handle, xClient,
		engagementRepository, jobRepository,
	)

	runner := worker.New(
		jobRepository, cfg.Runtime.WorkerID,
		cfg.Runtime.LeaseDuration.Value(), cfg.Runtime.PollInterval.Value(),
		cfg.Runtime.BatchSize, logger,
	)
	registerHandlers(handlerDependencies{
		accountID: cfg.Account.ID, jobs: jobRepository,
		rules: ruleRepository, sources: sourceRepository,
		sourceCollector: sourceCollector, mentionCollector: mentionCollector,
		content: contentRepository, engagement: engagementRepository,
		postWorkflow:  postWorkflow,
		replyWorkflow: replyWorkflow, postPublisher: postPublisher,
		replyPublisher: replyPublisher, analytics: analyticsService,
		analyticsWorkflow: analyticsWorkflow, xClient: xClient,
	}, runner)

	schedulerService := scheduler.New(cfg.Account.ID, ruleRepository, jobRepository)
	outboxDispatcher := outbox.NewDispatcher(
		outbox.NewRepository(store.DB), cfg.Runtime.WorkerID+"-outbox",
		cfg.Runtime.LeaseDuration.Value(), cfg.Runtime.BatchSize,
	)
	scheduleFromEvent := outbox.HandlerFunc(func(ctx context.Context, _ outbox.Event) error {
		_, err := schedulerService.Tick(ctx, time.Now())
		return err
	})
	outboxDispatcher.Register("rule.activated", scheduleFromEvent)
	outboxDispatcher.Register("content.approved", scheduleFromEvent)
	reconciler := publisher.NewReconciler(
		cfg.Account.ID, "", xClient, operationRepository,
	)
	var backupService *backup.Service
	if cfg.Backup.Enabled {
		backupService = backup.New(store.DB, cfg.Backup.Directory, cfg.Backup.Retain)
	}
	apiServer, err := httpapi.New(httpapi.Dependencies{
		AccountID: cfg.Account.ID, DryRun: cfg.Runtime.DryRun, StartedAt: time.Now(),
		Account: account.NewService(store.DB), Rules: ruleService,
		Jobs: jobRepository, Content: contentRepository,
		Engagement: engagementRepository, Audit: audit.NewRepository(store.DB),
		Metering: meteringRepository, Analytics: analyticsService,
		Alerts: alertService,
		LLM:    gateway, OAuth: oauthManager, PostWorkflow: postWorkflow,
	}, logger)
	if err != nil {
		return fail(err)
	}
	return &App{
		Config: cfg, Store: store, HTTP: apiServer,
		Scheduler: schedulerService, Worker: runner,
		Outbox: outboxDispatcher, Reconciler: reconciler, logger: logger,
		Backup: backupService,
		Alerts: alertService,
	}, nil
}

func (a *App) RunBackground(ctx context.Context) {
	go a.runScheduler(ctx)
	go func() {
		if err := a.Worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			a.logger.Error("worker stopped", "error", err)
		}
	}()
	go a.runOutbox(ctx)
	go a.runReconciler(ctx)
	if a.Backup != nil {
		go a.runBackup(ctx)
	}
	go a.runAlerts(ctx)
}

func (a *App) Close() error {
	return a.Store.Close()
}

func (a *App) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		result, err := a.Scheduler.Tick(ctx, time.Now())
		if err != nil {
			a.logger.Error("scheduler tick failed", "error", err)
		} else if result.Enqueued > 0 {
			a.logger.Info("scheduler enqueued jobs", "count", result.Enqueued)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) runOutbox(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := a.Outbox.Dispatch(ctx, time.Now()); err != nil {
			a.logger.Error("outbox dispatch failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		if _, err := a.Reconciler.Reconcile(ctx); err != nil {
			a.logger.Error("operation reconciliation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) runBackup(ctx context.Context) {
	ticker := time.NewTicker(a.Config.Backup.Interval.Value())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			path, err := a.Backup.Create(ctx)
			if err != nil {
				a.logger.Error("database backup failed", "error", err)
			} else {
				a.logger.Info("database backup completed", "path", path)
			}
		}
	}
}

func (a *App) runAlerts(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if _, err := a.Alerts.Evaluate(ctx); err != nil {
			a.logger.Error("alert evaluation failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func providerProfiles(cfg config.Config) ([]llm.Profile, error) {
	profiles := make([]llm.Profile, 0, len(cfg.LLM.Profiles))
	for name, item := range cfg.LLM.Profiles {
		capabilities := llm.Capabilities{}
		for _, capability := range item.Capabilities {
			switch capability {
			case "json_schema":
				capabilities.JSONSchema = true
				capabilities.JSONObject = true
			case "json_object":
				capabilities.JSONObject = true
			case "tool_calling":
				capabilities.ToolCalling = true
			case "vision":
				capabilities.Vision = true
			default:
				return nil, fmt.Errorf("profile %s has unknown capability %s", name, capability)
			}
		}
		profiles = append(profiles, llm.Profile{
			Name: name, Protocol: item.Protocol, BaseURL: item.BaseURL,
			APIKeyRef: item.APIKeyRef, Model: item.Model, Location: item.Location,
			Capabilities: capabilities, Enabled: true,
		})
	}
	return profiles, nil
}

func buildOAuth(
	cfg config.Config,
	keychain *credentials.Keychain,
	client *http.Client,
) (*oauth.Manager, error) {
	if cfg.X.ClientID == "" {
		return nil, fmt.Errorf("x.client_id is required when dry_run is false")
	}
	return oauth.NewManager(oauth.Config{
		ClientID: cfg.X.ClientID, AuthorizeURL: cfg.X.OAuthAuthorizeURL,
		TokenURL: cfg.X.OAuthTokenURL, RedirectURL: cfg.X.OAuthRedirectURL,
		RefreshTokenRef: cfg.X.CredentialRef,
		Scopes:          []string{"tweet.read", "tweet.write", "users.read", "offline.access"},
		HTTPClient:      client,
	}, keychain)
}

type handlerDependencies struct {
	accountID         string
	jobs              *jobs.Repository
	rules             *rules.Repository
	sources           *source.Repository
	sourceCollector   *source.Collector
	mentionCollector  *engagement.Collector
	content           *content.Repository
	engagement        *engagement.Repository
	postWorkflow      *orchestrator.PostWorkflow
	replyWorkflow     *orchestrator.ReplyWorkflow
	postPublisher     *publisher.PostPublisher
	replyPublisher    *publisher.ReplyPublisher
	analytics         *analytics.Service
	analyticsWorkflow *orchestrator.AnalyticsWorkflow
	xClient           xapi.Client
}

func registerHandlers(deps handlerDependencies, runner *worker.Runner) {
	runner.Register("source_collect", worker.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		_, err := deps.sourceCollector.Collect(ctx)
		return err
	}))
	runner.Register("topic_generate", worker.HandlerFunc(func(ctx context.Context, job jobs.Job) error {
		items, err := deps.sources.Recent(ctx, deps.accountID, 12)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		evidence := make([]orchestrator.EvidenceItem, 0, len(items))
		for _, item := range items {
			evidence = append(evidence, orchestrator.EvidenceItem{
				Title: item.Title, Summary: item.Summary, SourceURL: item.URL,
			})
		}
		_, err = deps.postWorkflow.Run(ctx, job.RuleVersionID, orchestrator.PostInput{
			Evidence: evidence,
		}, true)
		return err
	}))
	runner.Register("publish_post", worker.HandlerFunc(func(ctx context.Context, job jobs.Job) error {
		item, err := deps.content.NextApproved(ctx, deps.accountID, content.KindPost, job.RuleVersionID)
		if err != nil {
			return nil
		}
		_, err = deps.postPublisher.Publish(ctx, item.ID, job.RuleVersionID)
		return err
	}))
	runner.Register("sync_mentions", worker.HandlerFunc(func(ctx context.Context, job jobs.Job) error {
		_, err := deps.mentionCollector.Sync(ctx, job.RuleVersionID)
		return err
	}))
	runner.Register("publish_reply", worker.HandlerFunc(func(ctx context.Context, job jobs.Job) error {
		var payload struct {
			MentionID string `json:"mention_id"`
		}
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			return err
		}
		if payload.MentionID == "" {
			return fmt.Errorf("reply job has no mention ID")
		}
		mention, err := deps.engagement.GetMention(ctx, payload.MentionID)
		if err != nil {
			return err
		}
		item, err := deps.replyWorkflow.Run(ctx, job.RuleVersionID, payload.MentionID, true)
		if err != nil {
			return err
		}
		_, err = deps.replyPublisher.Publish(
			ctx, item.ID, job.RuleVersionID, mention.XPostID, mention.Summoned,
		)
		return err
	}))
	runner.Register("sync_account_metrics", worker.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		_, err := deps.analytics.SyncAccount(ctx)
		return err
	}))
	runner.Register("sync_post_metrics", worker.HandlerFunc(func(ctx context.Context, _ jobs.Job) error {
		account, err := deps.xClient.GetAccount(ctx)
		if err != nil {
			return err
		}
		_, err = deps.analytics.SyncPosts(ctx, account.ID, time.Now().Add(-8*24*time.Hour))
		return err
	}))
	runner.Register("update_strategy", worker.HandlerFunc(func(ctx context.Context, job jobs.Job) error {
		_, err := deps.analyticsWorkflow.Run(ctx, job.RuleVersionID, true)
		return err
	}))
}
