package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/config"
	"github.com/linli/im/server/internal/httpapi"
	"github.com/linli/im/server/internal/push"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

func main() {
	configureLogging()
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var persistence store.Persistence = store.Memory{}
	if cfg.Mode == "full" {
		pg, err := store.NewPostgresWithOptions(ctx, cfg.DatabaseURL, store.PostgresOptions{
			MaxConns: int32(cfg.DBMaxConns), MinConns: int32(cfg.DBMinConns),
			MaxConnLifetime: cfg.DBMaxConnLifetime, MaxConnIdleTime: cfg.DBMaxConnIdleTime,
			HealthCheckPeriod: cfg.DBHealthCheckPeriod, StatementTimeout: cfg.DBStatementTimeout,
		})
		if err != nil {
			slog.Error("postgres unavailable", "error", err)
			os.Exit(1)
		}
		persistence = pg
	}
	if cfg.RedisURL != "" && cfg.Mode == "full" {
		wrapped, err := store.NewWithRedis(persistence, cfg.RedisURL)
		if err != nil {
			slog.Error("invalid redis URL", "error", err)
			os.Exit(1)
		}
		persistence = wrapped
	}
	application, err := app.New(ctx, persistence)
	if err != nil {
		slog.Error("load application state", "error", err)
		os.Exit(1)
	}
	defer application.Close()
	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	if outbox, ok := persistence.(store.OutboxStore); ok {
		provider, err := configuredPushProvider(cfg)
		if err != nil {
			slog.Error("push provider unavailable", "error", err)
			os.Exit(1)
		}
		go push.NewDispatcherWithOptions(outbox, provider, push.DispatcherOptions{Workers: cfg.PushWorkers, BatchSize: cfg.PushBatchSize, Interval: 100 * time.Millisecond}).Run(workerCtx)
	}
	if cfg.DevMode {
		if err := application.SeedDemo(); err != nil {
			slog.Error("seed demo", "error", err)
			os.Exit(1)
		}
	}
	api := httpapi.New(cfg, application)
	var webhookServer *wukong.WebhookGRPCServer
	if cfg.WukongEnabled {
		webhookStore, ok := persistence.(wukong.WebhookEventStore)
		if !ok {
			if cfg.Mode == "full" {
				slog.Error("persistent WuKongIM webhook store is unavailable")
				os.Exit(1)
			}
			webhookStore = wukong.NewMemoryWebhookStore()
		}
		webhookServer, err = wukong.ListenWebhookGRPC(cfg.WukongGRPCAddr, webhookStore)
		if err != nil {
			slog.Error("WuKongIM webhook listener unavailable", "error", err)
			os.Exit(1)
		}
		go func() {
			slog.Info("WuKongIM gRPC webhook started", "event", "wukong.webhook.started", "addr", cfg.WukongGRPCAddr)
			if serveErr := webhookServer.Serve(); serveErr != nil {
				slog.Error("WuKongIM webhook server failed", "error", serveErr)
				os.Exit(1)
			}
		}()
		outboxStore, ok := persistence.(wukong.OutboxStore)
		if !ok {
			if cfg.Mode == "full" {
				slog.Error("persistent WuKongIM outbox store is unavailable")
				os.Exit(1)
			}
		} else {
			wukongClient, clientErr := wukong.NewClient(wukong.Config{
				APIURL: cfg.WukongAPIURL, ManagerURL: cfg.WukongManagerURL,
				ManagerToken: cfg.WukongManagerToken, Timeout: 5 * time.Second, MaxRetries: 2,
			})
			if clientErr != nil {
				slog.Error("WuKongIM outbox client unavailable", "error", clientErr)
				os.Exit(1)
			}
			worker, workerErr := wukong.NewOutboxWorker(outboxStore, wukongClient)
			if workerErr != nil {
				slog.Error("WuKongIM outbox worker unavailable", "error", workerErr)
				os.Exit(1)
			}
			go worker.Run(workerCtx)
			reconcileStore, reconcileOK := persistence.(wukong.ReconcileStore)
			if !reconcileOK {
				slog.Error("persistent WuKongIM reconcile store is unavailable")
				os.Exit(1)
			}
			reconciler, reconcileErr := wukong.NewReconciler(reconcileStore, wukongClient)
			if reconcileErr != nil {
				slog.Error("WuKongIM reconciler unavailable", "error", reconcileErr)
				os.Exit(1)
			}
			go reconciler.Run(workerCtx)
		}
	}
	go api.RunMediaCleanup(workerCtx)
	go application.RunCallTimeouts(workerCtx)
	go application.RunFriendRequestTimeouts(workerCtx)
	go application.RunAnnouncementScheduler(workerCtx)
	go application.RunScheduledMessages(workerCtx)
	go application.RunMessageExpirations(workerCtx)
	if cfg.Mode == "memory" && !cfg.WukongEnabled {
		go application.RunMessageFanout(workerCtx, cfg.MessageFanoutBatchSize)
	}
	go application.RunRuntimeCleanup(workerCtx, cfg.RuntimeCleanupInterval, store.RetentionPolicy{Outbox: cfg.OutboxRetention})
	go application.RunBusinessMembershipExpirations(workerCtx)
	go application.RunBanExpirations(workerCtx)
	server := &http.Server{Addr: cfg.Addr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 75 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		slog.Info("IM 服务已启动", "event", "server.started", "addr", cfg.Addr, "mode", cfg.Mode)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	if webhookServer != nil {
		webhookServer.Stop()
	}
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdown); err != nil {
		slog.Error("graceful shutdown", "error", err)
	}
}

func configuredPushProvider(cfg config.Config) (push.Provider, error) {
	getui := func(suppressIOSCalls bool) push.Provider {
		return &push.Getui{
			AppID: cfg.GetuiAppID, AppKey: cfg.GetuiAppKey, MasterSecret: cfg.GetuiMasterSecret,
			SuppressIOSCallsWithVoIP: suppressIOSCalls,
		}
	}
	apnsVoIP := func() (push.Provider, error) {
		privateKey, err := os.ReadFile(cfg.APNSVoIPKeyFile)
		if err != nil {
			return nil, errors.New("cannot read APNs VoIP private key file")
		}
		provider, err := push.NewAPNSVoIP(cfg.APNSVoIPKeyID, cfg.APNSVoIPTeamID, cfg.APNSVoIPBundleID, cfg.APNSVoIPSandbox, privateKey)
		if err != nil {
			return nil, err
		}
		return provider, nil
	}
	switch cfg.PushProvider {
	case "log":
		return push.Log{}, nil
	case "webhook":
		return push.Webhook{URL: cfg.PushWebhookURL, Token: cfg.PushWebhookToken}, nil
	case "getui":
		return getui(false), nil
	case "apns_voip":
		return apnsVoIP()
	case "getui_apns_voip":
		apns, err := apnsVoIP()
		if err != nil {
			return nil, err
		}
		return push.MultiProvider{getui(true), apns}, nil
	default:
		return push.Noop{}, nil
	}
}

func configureLogging() {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("IM_LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})))
}
