// The platform is a separate executable and database. It never starts business
// workers, WuKongIM, media storage or enterprise API handlers.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/linli/im/server/internal/deployment"
	"github.com/linli/im/server/internal/platform"
	"github.com/linli/im/server/internal/tenancy"
	"github.com/redis/go-redis/v9"
)

func main() {
	if err := run(); err != nil {
		slog.Error("platform startup/runtime failed; check local configuration and dependencies")
		os.Exit(1)
	}
}
func run() error {
	if err := validatePlatformRuntime(os.Getenv); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv("PLATFORM_DATABASE_URL") == "" || os.Getenv("PLATFORM_REDIS_URL") == "" {
		return errors.New("platform persistence required")
	}
	tlsConfig, err := tenancy.TLSConfig(os.Getenv("PLATFORM_CA_FILE"), os.Getenv("PLATFORM_CERT_FILE"), os.Getenv("PLATFORM_KEY_FILE"))
	if err != nil {
		return err
	}
	if err = tenancy.ValidateLocalIdentity(tlsConfig, tenancy.PlatformIdentity); err != nil {
		return err
	}
	db, err := platform.OpenWithAuthority(ctx, os.Getenv("PLATFORM_DATABASE_URL"), tenancy.CertificateFingerprint(tlsConfig))
	if err != nil {
		return err
	}
	defer db.Close()
	if err = db.BootstrapAdmin(ctx, os.Getenv("PLATFORM_ADMIN_USERNAME"), os.Getenv("PLATFORM_ADMIN_PASSWORD_HASH")); err != nil {
		return err
	}
	opts, err := redis.ParseURL(os.Getenv("PLATFORM_REDIS_URL"))
	if err != nil {
		return err
	}
	cache := redis.NewClient(opts)
	defer cache.Close()
	if err = cache.Ping(ctx).Err(); err != nil {
		return err
	}
	var entries []struct {
		TenantID   string `json:"tenantId"`
		ControlURL string `json:"controlUrl"`
	}
	peerFile, err := os.Open(os.Getenv("PLATFORM_PEERS_FILE"))
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(peerFile)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&entries)
	peerFile.Close()
	if err != nil {
		return err
	}
	peers := platform.EnterpriseRPC{Peers: map[string]*tenancy.RPC{}}
	for _, entry := range entries {
		if !tenancy.ValidID(entry.TenantID) || peers.Peers[entry.TenantID] != nil {
			return tenancy.ErrInvalid
		}
		rpc, err := tenancy.NewRPC(entry.ControlURL, tlsConfig, tenancy.EnterpriseIdentity(entry.TenantID))
		if err != nil {
			return err
		}
		peers.Peers[entry.TenantID] = rpc
	}
	origins := strings.Split(os.Getenv("PLATFORM_WEB_ORIGIN"), ",")
	// Development permits literal loopback HTTP for Vite; production requires
	// explicit non-loopback HTTPS origins and a dedicated deployment profile.
	for _, origin := range origins {
		if tenancy.ValidateBaseURL(origin, os.Getenv("PLATFORM_ENV") == "development") != nil {
			return tenancy.ErrInvalid
		}
	}
	api := &platform.API{Store: db, Limiter: platform.RedisLimiter{Client: cache}, WebOrigins: origins, Peers: peers, GatewaySecret: os.Getenv("PLATFORM_GATEWAY_SECRET")}
	if path := os.Getenv("PLATFORM_AGENTS_FILE"); path != "" {
		f, openErr := os.Open(path)
		if openErr != nil {
			return errors.New("agent configuration unavailable")
		}
		api.Agents.Peers, err = deployment.ReadPeers(f)
		f.Close()
		if err != nil {
			return err
		}
	}
	if path := os.Getenv("PLATFORM_DEPLOYMENT_CATALOG_FILE"); path != "" {
		if !filepath.IsAbs(path) {
			return tenancy.ErrInvalid
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return errors.New("deployment catalog unavailable")
		}
		api.DeploymentCatalog, err = deployment.ReadCatalog(f)
		f.Close()
		if err != nil {
			return err
		}
	}
	api.Push, err = configuredPlatformPush(db)
	if err != nil {
		return err
	}
	if base := os.Getenv("PLATFORM_OTP_WEBHOOK_URL"); base != "" {
		api.OTP, err = platform.NewWebhookOTP(base, os.Getenv("PLATFORM_OTP_WEBHOOK_TOKEN"))
		if err != nil {
			return err
		}
	}
	if base := os.Getenv("PLATFORM_PASSWORD_RESET_SMS_URL"); base != "" {
		api.RecoverySMS, err = platform.NewWebhookRecoverySMS(base, os.Getenv("PLATFORM_PASSWORD_RESET_SMS_TOKEN"))
		if err != nil {
			return err
		}
	}
	publicAddr := os.Getenv("PLATFORM_ADDR")
	if publicAddr == "" {
		publicAddr = "127.0.0.1:8090"
	}
	controlAddr := os.Getenv("PLATFORM_CONTROL_ADDR")
	if controlAddr == "" {
		controlAddr = ":8443"
	}
	public := &http.Server{Addr: publicAddr, Handler: api.PublicHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	control := &http.Server{Addr: controlAddr, Handler: api.InternalHandler(), TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	worker := platform.Worker{Store: db, Enterprise: peers}
	go worker.Run(ctx)
	go (platform.CredentialWorker{Store: db, Enterprise: peers}).Run(ctx)
	go (platform.AccessWorker{Store: db, Enterprise: peers}).Run(ctx)
	go (platform.LegacyBanExpiryWorker{Store: db}).Run(ctx)
	go (platform.RealmWorker{Store: db, Enterprise: peers}).Run(ctx)
	go (platform.DeploymentWorker{Store: db, Agent: api.Agents, Catalog: api.DeploymentCatalog, Readiness: peers.CheckReadiness}).Run(ctx)
	go (platform.BackupWorker{Store: db, Agent: api.Agents, Catalog: api.DeploymentCatalog, Readiness: peers.CheckReadiness}).Run(ctx)
	go (platform.MaintenanceWorker{Store: db, Agent: api.Agents, Catalog: api.DeploymentCatalog, Readiness: peers.CheckReadiness}).Run(ctx)
	errorsCh := make(chan error, 2)
	go func() { errorsCh <- public.ListenAndServe() }()
	go func() { errorsCh <- control.ListenAndServeTLS("", "") }()
	select {
	case <-ctx.Done():
	case err = <-errorsCh:
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = public.Shutdown(shutdown)
	_ = control.Shutdown(shutdown)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
