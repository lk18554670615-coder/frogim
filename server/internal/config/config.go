package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/wukongplugin"
)

type Config struct {
	Addr, Mode, JWTSecret, AdminKey, DatabaseURL, RedisURL, PushProvider string
	Environment                                                          string
	PushWebhookURL, PushWebhookToken                                     string
	GetuiAppID, GetuiAppKey, GetuiMasterSecret                           string
	APNSVoIPKeyID, APNSVoIPTeamID, APNSVoIPBundleID, APNSVoIPKeyFile     string
	APNSVoIPSandbox                                                      bool
	OTPWebhookURL, OTPWebhookToken                                       string
	AdminEmail, AdminPasswordHash, AdminTOTPSecret, AdminID, AdminRole   string
	S3Endpoint, S3PublicEndpoint, S3AndroidPublicEndpoint                string
	S3AccessKey, S3SecretKey, S3Bucket                                   string
	S3Region                                                             string
	DevOTPCode                                                           string
	AllowedOrigins                                                       []string
	S3Secure, S3PublicSecure                                             bool
	DevMode                                                              bool
	TrustProxy                                                           bool
	AdminSharedKeyEnabled                                                bool
	DevAllowContainerBind                                                bool
	DevIPTestOnly                                                        bool
	DBMaxConns, DBMinConns                                               int
	WukongInternalRateLimitPerMinute                                     int
	PushWorkers, PushBatchSize, MessageFanoutBatchSize                   int
	MediaMaxBytes                                                        int64
	AccessTTL, RefreshTTL                                                time.Duration
	CallInviteTTL                                                        time.Duration
	LiveKitTokenTTL                                                      time.Duration
	DBMaxConnLifetime, DBMaxConnIdleTime, DBHealthCheckPeriod            time.Duration
	DBStatementTimeout                                                   time.Duration
	RuntimeCleanupInterval, OutboxRetention                              time.Duration
	HTTPLogSuccessSampleRate                                             float64
	WukongEnabled                                                        bool
	WukongAPIURL, WukongManagerURL, WukongManagerToken                   string
	WukongTokenSecret, WukongPolicySecret, WukongGRPCAddr                string
	WukongTCPURL, WukongWSURL                                            string
	WukongPluginDir, WukongPluginTrustedKeys, WukongPluginAllowlist      string
	WukongPluginMaxBytes                                                 int64
	LiveKitEnabled                                                       bool
	LiveKitURL, LiveKitAPIURL, LiveKitAPIKey, LiveKitAPISecret           string
}

func Load() Config {
	return Config{
		Addr: value("IM_ADDR", ":8080"), Mode: value("IM_MODE", "memory"), Environment: environment(), JWTSecret: os.Getenv("IM_JWT_SECRET"),
		AdminKey: os.Getenv("IM_ADMIN_KEY"), DatabaseURL: os.Getenv("IM_DATABASE_URL"), RedisURL: os.Getenv("IM_REDIS_URL"),
		AdminEmail: os.Getenv("IM_ADMIN_EMAIL"), AdminPasswordHash: os.Getenv("IM_ADMIN_PASSWORD_HASH"), AdminTOTPSecret: os.Getenv("IM_ADMIN_TOTP_SECRET"), AdminID: value("IM_ADMIN_ID", "platform-admin"), AdminRole: value("IM_ADMIN_ROLE", "platform_admin"), AdminSharedKeyEnabled: boolValue("IM_ADMIN_SHARED_KEY_ENABLED", false),
		PushProvider:   value("IM_PUSH_PROVIDER", "noop"),
		PushWebhookURL: os.Getenv("IM_PUSH_WEBHOOK_URL"), PushWebhookToken: os.Getenv("IM_PUSH_WEBHOOK_TOKEN"),
		GetuiAppID: os.Getenv("IM_GETUI_APP_ID"), GetuiAppKey: os.Getenv("IM_GETUI_APP_KEY"), GetuiMasterSecret: os.Getenv("IM_GETUI_MASTER_SECRET"),
		APNSVoIPKeyID: os.Getenv("IM_APNS_VOIP_KEY_ID"), APNSVoIPTeamID: os.Getenv("IM_APNS_VOIP_TEAM_ID"), APNSVoIPBundleID: os.Getenv("IM_APNS_VOIP_BUNDLE_ID"), APNSVoIPKeyFile: os.Getenv("IM_APNS_VOIP_PRIVATE_KEY_FILE"), APNSVoIPSandbox: boolValue("IM_APNS_VOIP_SANDBOX", false),
		OTPWebhookURL: os.Getenv("IM_OTP_WEBHOOK_URL"), OTPWebhookToken: os.Getenv("IM_OTP_WEBHOOK_TOKEN"),
		S3Endpoint: os.Getenv("IM_S3_ENDPOINT"), S3PublicEndpoint: os.Getenv("IM_S3_PUBLIC_ENDPOINT"), S3AndroidPublicEndpoint: os.Getenv("IM_S3_ANDROID_PUBLIC_ENDPOINT"), S3AccessKey: os.Getenv("IM_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("IM_S3_SECRET_KEY"), S3Bucket: value("IM_S3_BUCKET", "nexachat-media"), S3Region: value("IM_S3_REGION", "us-east-1"), S3Secure: boolValue("IM_S3_SECURE", false), S3PublicSecure: boolValue("IM_S3_PUBLIC_SECURE", false),
		DevMode: boolValue("IM_DEV_MODE", false), TrustProxy: boolValue("IM_TRUST_PROXY", false), DevAllowContainerBind: boolValue("IM_DEV_ALLOW_CONTAINER_BIND", false), DevIPTestOnly: boolValue("IM_IP_TEST_ONLY", false), DevOTPCode: os.Getenv("IM_DEV_OTP_CODE"), AllowedOrigins: csv("IM_ALLOWED_ORIGINS"),
		DBMaxConns: intValue("IM_DB_MAX_CONNS", 20), DBMinConns: intValue("IM_DB_MIN_CONNS", 2),
		WukongInternalRateLimitPerMinute: intValue("IM_WUKONG_INTERNAL_RATE_LIMIT_PER_MINUTE", 120000),
		DBMaxConnLifetime:                duration("IM_DB_MAX_CONN_LIFETIME", time.Hour), DBMaxConnIdleTime: duration("IM_DB_MAX_CONN_IDLE_TIME", 15*time.Minute), DBHealthCheckPeriod: duration("IM_DB_HEALTH_CHECK_PERIOD", time.Minute), DBStatementTimeout: duration("IM_DB_STATEMENT_TIMEOUT", 15*time.Second),
		PushWorkers: intValue("IM_PUSH_WORKERS", 16), PushBatchSize: intValue("IM_PUSH_BATCH_SIZE", 200), MessageFanoutBatchSize: intValue("IM_MESSAGE_FANOUT_BATCH_SIZE", 500),
		RuntimeCleanupInterval: duration("IM_RUNTIME_CLEANUP_INTERVAL", time.Hour), OutboxRetention: duration("IM_OUTBOX_RETENTION", 7*24*time.Hour),
		HTTPLogSuccessSampleRate: floatValue("IM_HTTP_LOG_SUCCESS_SAMPLE_RATE", 0.01),
		MediaMaxBytes:            int64Value("IM_MEDIA_MAX_BYTES", 100<<20),
		AccessTTL:                duration("IM_ACCESS_TTL", 15*time.Minute), RefreshTTL: duration("IM_REFRESH_TTL", 30*24*time.Hour),
		CallInviteTTL: duration("IM_CALL_INVITE_TTL", 30*time.Second),
		WukongEnabled: boolValue("IM_WUKONG_ENABLED", false), WukongAPIURL: os.Getenv("IM_WUKONG_API_URL"), WukongManagerURL: os.Getenv("IM_WUKONG_MANAGER_URL"), WukongManagerToken: os.Getenv("IM_WUKONG_MANAGER_TOKEN"),
		WukongTokenSecret: os.Getenv("IM_WUKONG_TOKEN_SECRET"), WukongPolicySecret: os.Getenv("IM_WUKONG_POLICY_SECRET"), WukongGRPCAddr: value("IM_WUKONG_GRPC_ADDR", ":6970"), WukongTCPURL: os.Getenv("IM_WUKONG_TCP_URL"), WukongWSURL: os.Getenv("IM_WUKONG_WS_URL"),
		WukongPluginDir: os.Getenv("IM_WUKONG_PLUGIN_DIR"), WukongPluginTrustedKeys: os.Getenv("IM_WUKONG_PLUGIN_TRUSTED_KEYS"), WukongPluginAllowlist: os.Getenv("IM_WUKONG_PLUGIN_ALLOWLIST"), WukongPluginMaxBytes: int64Value("IM_WUKONG_PLUGIN_MAX_BYTES", 64<<20),
		LiveKitEnabled: boolValue("IM_LIVEKIT_ENABLED", false), LiveKitURL: os.Getenv("IM_LIVEKIT_URL"), LiveKitAPIURL: os.Getenv("IM_LIVEKIT_API_URL"), LiveKitAPIKey: os.Getenv("IM_LIVEKIT_API_KEY"), LiveKitAPISecret: os.Getenv("IM_LIVEKIT_API_SECRET"), LiveKitTokenTTL: duration("IM_LIVEKIT_TOKEN_TTL", 5*time.Minute),
	}
}

func (c Config) Validate() error {
	if c.Mode != "memory" && c.Mode != "full" {
		return fmt.Errorf("unsupported IM_MODE %q", c.Mode)
	}
	if len(c.JWTSecret) < 32 {
		return errors.New("IM_JWT_SECRET must contain at least 32 bytes")
	}
	if c.AdminSharedKeyEnabled && len(c.AdminKey) < 24 {
		return errors.New("IM_ADMIN_KEY must contain at least 24 bytes")
	}
	if c.AdminSharedKeyEnabled && !c.DevMode {
		return errors.New("IM_ADMIN_SHARED_KEY_ENABLED is permitted only in development mode")
	}
	if c.AccessTTL < time.Minute || c.AccessTTL > time.Hour {
		return errors.New("IM_ACCESS_TTL must be between 1m and 1h")
	}
	if c.RefreshTTL < time.Hour || c.RefreshTTL > 90*24*time.Hour {
		return errors.New("IM_REFRESH_TTL must be between 1h and 2160h")
	}
	if c.Mode == "full" && c.DatabaseURL == "" {
		return errors.New("IM_DATABASE_URL is required in full mode")
	}
	if c.Mode == "full" && !c.DevMode {
		if c.DevAllowContainerBind || c.DevIPTestOnly {
			return errors.New("development container and IP-test flags are forbidden in production full mode")
		}
		if c.AdminEmail == "" || !strings.HasPrefix(c.AdminPasswordHash, "$2") || c.AdminTOTPSecret == "" {
			return errors.New("production full mode requires admin email, bcrypt hash, and TOTP secret")
		}
		if !strings.HasPrefix(c.OTPWebhookURL, "https://") || len(c.OTPWebhookToken) < 24 {
			return errors.New("production full mode requires an HTTPS OTP webhook and high-entropy token")
		}
		switch c.PushProvider {
		case "webhook":
			if !strings.HasPrefix(c.PushWebhookURL, "https://") || len(c.PushWebhookToken) < 24 {
				return errors.New("production webhook push requires HTTPS and a high-entropy token")
			}
		case "getui":
			if err := c.validateGetui(); err != nil {
				return err
			}
		case "apns_voip":
			if err := c.validateAPNSVoIP(); err != nil {
				return err
			}
		case "getui_apns_voip":
			if err := c.validateGetui(); err != nil {
				return err
			}
			if err := c.validateAPNSVoIP(); err != nil {
				return err
			}
		default:
			return errors.New("production full mode requires webhook, getui, apns_voip, or getui_apns_voip push")
		}
	}
	if c.DevMode {
		if strings.EqualFold(c.Environment, "production") {
			return errors.New("development mode is forbidden when IM_ENV or APP_ENV is production")
		}
		if len(c.DevOTPCode) < 6 {
			return errors.New("IM_DEV_OTP_CODE must be explicitly configured in development mode")
		}
		host, _, err := net.SplitHostPort(c.Addr)
		loopback := err == nil && (host == "127.0.0.1" || host == "localhost" || host == "::1")
		containerDevelopment := err == nil && c.Mode == "full" && c.DevAllowContainerBind && c.DevIPTestOnly && strings.EqualFold(c.Environment, "development") && (host == "" || host == "0.0.0.0" || host == "::")
		if !loopback && !containerDevelopment {
			return errors.New("development mode may bind publicly only for explicit full-mode development containers")
		}
	}
	dbMaxConns, dbMinConns := c.DBMaxConns, c.DBMinConns
	if dbMaxConns == 0 {
		dbMaxConns, dbMinConns = 20, 2
	}
	if dbMaxConns < 4 || dbMaxConns > 500 || dbMinConns < 0 || dbMinConns >= dbMaxConns {
		return errors.New("invalid PostgreSQL connection pool budget")
	}
	dbLifetime, dbIdle, dbHealth := c.DBMaxConnLifetime, c.DBMaxConnIdleTime, c.DBHealthCheckPeriod
	if dbLifetime == 0 {
		dbLifetime = time.Hour
	}
	if dbIdle == 0 {
		dbIdle = 15 * time.Minute
	}
	if dbHealth == 0 {
		dbHealth = time.Minute
	}
	if dbLifetime < time.Minute || dbIdle < time.Minute || dbHealth < 10*time.Second {
		return errors.New("invalid PostgreSQL connection pool timing")
	}
	if c.DBStatementTimeout != 0 && (c.DBStatementTimeout < time.Second || c.DBStatementTimeout > time.Minute) {
		return errors.New("IM_DB_STATEMENT_TIMEOUT must be between 1s and 1m")
	}
	pushWorkers, pushBatch, fanoutBatch := c.PushWorkers, c.PushBatchSize, c.MessageFanoutBatchSize
	if pushWorkers == 0 {
		pushWorkers = 16
	}
	if pushBatch == 0 {
		pushBatch = 200
	}
	if fanoutBatch == 0 {
		fanoutBatch = 500
	}
	if pushWorkers < 1 || pushWorkers > 128 || pushBatch < 1 || pushBatch > 1000 || fanoutBatch < 10 || fanoutBatch > 5000 {
		return errors.New("invalid asynchronous worker configuration")
	}
	cleanupInterval, outboxRetention := c.RuntimeCleanupInterval, c.OutboxRetention
	if cleanupInterval == 0 {
		cleanupInterval = time.Hour
	}
	if outboxRetention == 0 {
		outboxRetention = 7 * 24 * time.Hour
	}
	if cleanupInterval < time.Minute || outboxRetention < time.Hour {
		return errors.New("invalid runtime data retention configuration")
	}
	if c.HTTPLogSuccessSampleRate < 0 || c.HTTPLogSuccessSampleRate > 1 {
		return errors.New("IM_HTTP_LOG_SUCCESS_SAMPLE_RATE must be between 0 and 1")
	}
	if c.WukongInternalRateLimitPerMinute != 0 && (c.WukongInternalRateLimitPerMinute < 60000 || c.WukongInternalRateLimitPerMinute > 600000) {
		return errors.New("IM_WUKONG_INTERNAL_RATE_LIMIT_PER_MINUTE must be between 60000 and 600000")
	}
	if c.MediaMaxBytes < 1<<20 || c.MediaMaxBytes > 2<<30 {
		return errors.New("IM_MEDIA_MAX_BYTES must be between 1 MiB and 2 GiB")
	}
	if strings.TrimSpace(c.S3AndroidPublicEndpoint) != "" && !c.DevMode {
		return errors.New("IM_S3_ANDROID_PUBLIC_ENDPOINT is permitted only in development mode")
	}
	if c.CallInviteTTL != 0 && (c.CallInviteTTL < 15*time.Second || c.CallInviteTTL > 2*time.Minute) {
		return errors.New("IM_CALL_INVITE_TTL must be between 15s and 2m")
	}
	if c.Mode == "full" && !c.WukongEnabled {
		return errors.New("IM_MODE=full requires WuKongIM; the legacy message transport is not deployable")
	}
	if c.WukongEnabled {
		if !isHTTPURL(c.WukongAPIURL) || !isHTTPURL(c.WukongManagerURL) {
			return errors.New("WuKongIM internal API and manager URLs must use http or https")
		}
		if len(c.WukongManagerToken) < 24 || len(c.WukongTokenSecret) < 32 || len(c.WukongPolicySecret) < 32 {
			return errors.New("WuKongIM manager token must contain at least 24 bytes and token and policy secrets at least 32 bytes")
		}
		if c.WukongGRPCAddr == "" || !hasAnyPrefix(c.WukongTCPURL, "tcp://", "tls://") || !hasAnyPrefix(c.WukongWSURL, "ws://", "wss://") {
			return errors.New("WuKongIM requires gRPC, TCP and WebSocket endpoints")
		}
		pluginConfigCount := 0
		for _, value := range []string{c.WukongPluginDir, c.WukongPluginTrustedKeys, c.WukongPluginAllowlist} {
			if strings.TrimSpace(value) != "" {
				pluginConfigCount++
			}
		}
		if pluginConfigCount != 0 && pluginConfigCount != 3 {
			return errors.New("WuKongIM plugin lifecycle requires directory, trusted keys, and an allowlist together")
		}
		if pluginConfigCount == 3 {
			trustedKeys, keyErr := wukongplugin.ParseTrustedKeys(c.WukongPluginTrustedKeys)
			allowlist, allowErr := wukongplugin.ParseAllowlist(c.WukongPluginAllowlist)
			if keyErr != nil || allowErr != nil || len(trustedKeys) == 0 || len(allowlist) == 0 {
				return errors.New("WuKongIM plugin trusted keys and allowlist must be valid and non-empty")
			}
		}
		pluginMaxBytes := c.WukongPluginMaxBytes
		if pluginMaxBytes == 0 {
			pluginMaxBytes = 64 << 20
		}
		if pluginMaxBytes < 1<<20 || pluginMaxBytes > 512<<20 {
			return errors.New("IM_WUKONG_PLUGIN_MAX_BYTES must be between 1 MiB and 512 MiB")
		}
		if c.Mode == "full" && !c.DevMode && pluginConfigCount != 3 {
			return errors.New("production WuKongIM requires the signed plugin lifecycle configuration")
		}
	}
	if c.LiveKitEnabled {
		if !hasAnyPrefix(c.LiveKitURL, "ws://", "wss://") || !isHTTPURL(c.LiveKitAPIURL) || c.LiveKitAPIKey == "" || len(c.LiveKitAPISecret) < 32 {
			return errors.New("LiveKit requires signal/API URLs, an API key and a secret of at least 32 bytes")
		}
		if c.LiveKitTokenTTL < time.Minute || c.LiveKitTokenTTL > 15*time.Minute {
			return errors.New("IM_LIVEKIT_TOKEN_TTL must be between 1m and 15m")
		}
	}
	if c.Mode == "full" && !c.DevMode && !c.LiveKitEnabled {
		return errors.New("production full mode requires LiveKit")
	}
	return nil
}

func isHTTPURL(value string) bool { return hasAnyPrefix(value, "http://", "https://") }

func hasAnyPrefix(value string, prefixes ...string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func (c Config) validateGetui() error {
	if c.GetuiAppID == "" || c.GetuiAppKey == "" || len(c.GetuiMasterSecret) < 16 {
		return errors.New("production Getui push requires app id, app key, and master secret")
	}
	return nil
}

func (c Config) validateAPNSVoIP() error {
	validAppleID := func(value string) bool {
		if len(value) != 10 {
			return false
		}
		for _, r := range value {
			if !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
				return false
			}
		}
		return true
	}
	if !validAppleID(c.APNSVoIPKeyID) || !validAppleID(c.APNSVoIPTeamID) {
		return errors.New("APNs VoIP requires 10-character key id and team id")
	}
	if c.APNSVoIPBundleID == "" || strings.HasSuffix(c.APNSVoIPBundleID, ".voip") || strings.ContainsAny(c.APNSVoIPBundleID, " /\\") {
		return errors.New("APNs VoIP requires the app bundle id without the .voip suffix")
	}
	if c.APNSVoIPKeyFile == "" {
		return errors.New("APNs VoIP requires IM_APNS_VOIP_PRIVATE_KEY_FILE")
	}
	return nil
}

func value(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func boolValue(k string, d bool) bool {
	if v := os.Getenv(k); v != "" {
		b, e := strconv.ParseBool(v)
		if e == nil {
			return b
		}
	}
	return d
}
func duration(k string, d time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		x, e := time.ParseDuration(v)
		if e == nil {
			return x
		}
	}
	return d
}
func intValue(k string, d int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return d
}
func int64Value(k string, d int64) int64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return d
}
func floatValue(k string, d float64) float64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return d
}
func csv(k string) []string {
	var out []string
	for _, v := range strings.Split(os.Getenv(k), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func environment() string {
	imEnv := strings.ToLower(strings.TrimSpace(os.Getenv("IM_ENV")))
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if imEnv == "production" || appEnv == "production" {
		return "production"
	}
	if imEnv != "" {
		return imEnv
	}
	return appEnv
}
