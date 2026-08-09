package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
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
	S3Endpoint, S3PublicEndpoint, S3AccessKey, S3SecretKey, S3Bucket     string
	S3Region                                                             string
	DevOTPCode                                                           string
	AllowedOrigins                                                       []string
	RTCSTUNURLs, RTCTURNURLs                                             []string
	RTCTURNUsername, RTCTURNCredential                                   string
	S3Secure, S3PublicSecure                                             bool
	DevMode                                                              bool
	TrustProxy                                                           bool
	AdminSharedKeyEnabled                                                bool
	DevAllowContainerBind                                                bool
	DevIPTestOnly                                                        bool
	WSMaxPerUser, WSMaxPerIP, WSMaxConnections                           int
	DBMaxConns, DBMinConns                                               int
	PushWorkers, PushBatchSize, MessageFanoutBatchSize                   int
	MediaMaxBytes                                                        int64
	AccessTTL, RefreshTTL                                                time.Duration
	CallInviteTTL                                                        time.Duration
	DBMaxConnLifetime, DBMaxConnIdleTime, DBHealthCheckPeriod            time.Duration
	DBStatementTimeout                                                   time.Duration
	RuntimeCleanupInterval, SyncRetention, OutboxRetention               time.Duration
	HTTPLogSuccessSampleRate                                             float64
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
		S3Endpoint: os.Getenv("IM_S3_ENDPOINT"), S3PublicEndpoint: os.Getenv("IM_S3_PUBLIC_ENDPOINT"), S3AccessKey: os.Getenv("IM_S3_ACCESS_KEY"), S3SecretKey: os.Getenv("IM_S3_SECRET_KEY"), S3Bucket: value("IM_S3_BUCKET", "nexachat-media"), S3Region: value("IM_S3_REGION", "us-east-1"), S3Secure: boolValue("IM_S3_SECURE", false), S3PublicSecure: boolValue("IM_S3_PUBLIC_SECURE", false),
		DevMode: boolValue("IM_DEV_MODE", false), TrustProxy: boolValue("IM_TRUST_PROXY", false), DevAllowContainerBind: boolValue("IM_DEV_ALLOW_CONTAINER_BIND", false), DevIPTestOnly: boolValue("IM_IP_TEST_ONLY", false), DevOTPCode: os.Getenv("IM_DEV_OTP_CODE"), AllowedOrigins: csv("IM_ALLOWED_ORIGINS"),
		RTCSTUNURLs: csv("IM_RTC_STUN_URLS"), RTCTURNURLs: csv("IM_RTC_TURN_URLS"), RTCTURNUsername: os.Getenv("IM_RTC_TURN_USERNAME"), RTCTURNCredential: os.Getenv("IM_RTC_TURN_CREDENTIAL"),
		WSMaxPerUser: intValue("IM_WS_MAX_PER_USER", 5), WSMaxPerIP: intValue("IM_WS_MAX_PER_IP", 20), WSMaxConnections: intValue("IM_WS_MAX_CONNECTIONS", 10000),
		DBMaxConns: intValue("IM_DB_MAX_CONNS", 20), DBMinConns: intValue("IM_DB_MIN_CONNS", 2),
		DBMaxConnLifetime: duration("IM_DB_MAX_CONN_LIFETIME", time.Hour), DBMaxConnIdleTime: duration("IM_DB_MAX_CONN_IDLE_TIME", 15*time.Minute), DBHealthCheckPeriod: duration("IM_DB_HEALTH_CHECK_PERIOD", time.Minute), DBStatementTimeout: duration("IM_DB_STATEMENT_TIMEOUT", 15*time.Second),
		PushWorkers: intValue("IM_PUSH_WORKERS", 16), PushBatchSize: intValue("IM_PUSH_BATCH_SIZE", 200), MessageFanoutBatchSize: intValue("IM_MESSAGE_FANOUT_BATCH_SIZE", 500),
		RuntimeCleanupInterval: duration("IM_RUNTIME_CLEANUP_INTERVAL", time.Hour), SyncRetention: duration("IM_SYNC_RETENTION", 30*24*time.Hour), OutboxRetention: duration("IM_OUTBOX_RETENTION", 7*24*time.Hour),
		HTTPLogSuccessSampleRate: floatValue("IM_HTTP_LOG_SUCCESS_SAMPLE_RATE", 0.01),
		MediaMaxBytes:            int64Value("IM_MEDIA_MAX_BYTES", 100<<20),
		AccessTTL:                duration("IM_ACCESS_TTL", 15*time.Minute), RefreshTTL: duration("IM_REFRESH_TTL", 30*24*time.Hour),
		CallInviteTTL: duration("IM_CALL_INVITE_TTL", 30*time.Second),
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
	wsMaxConnections := c.WSMaxConnections
	if wsMaxConnections == 0 {
		wsMaxConnections = 10000
	}
	if c.WSMaxPerUser < 1 || c.WSMaxPerUser > 20 || c.WSMaxPerIP < 1 || c.WSMaxPerIP > 200 || wsMaxConnections < 1 || wsMaxConnections > 1000000 {
		return errors.New("invalid WebSocket connection budget")
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
	cleanupInterval, syncRetention, outboxRetention := c.RuntimeCleanupInterval, c.SyncRetention, c.OutboxRetention
	if cleanupInterval == 0 {
		cleanupInterval = time.Hour
	}
	if syncRetention == 0 {
		syncRetention = 30 * 24 * time.Hour
	}
	if outboxRetention == 0 {
		outboxRetention = 7 * 24 * time.Hour
	}
	if cleanupInterval < time.Minute || syncRetention < 24*time.Hour || outboxRetention < time.Hour {
		return errors.New("invalid runtime data retention configuration")
	}
	if c.HTTPLogSuccessSampleRate < 0 || c.HTTPLogSuccessSampleRate > 1 {
		return errors.New("IM_HTTP_LOG_SUCCESS_SAMPLE_RATE must be between 0 and 1")
	}
	if c.MediaMaxBytes < 1<<20 || c.MediaMaxBytes > 2<<30 {
		return errors.New("IM_MEDIA_MAX_BYTES must be between 1 MiB and 2 GiB")
	}
	if c.CallInviteTTL != 0 && (c.CallInviteTTL < 15*time.Second || c.CallInviteTTL > 2*time.Minute) {
		return errors.New("IM_CALL_INVITE_TTL must be between 15s and 2m")
	}
	if c.Mode == "full" && !c.DevMode {
		if !hasURLPrefix(c.RTCSTUNURLs, "stun:") || (!hasURLPrefix(c.RTCTURNURLs, "turn:") && !hasURLPrefix(c.RTCTURNURLs, "turns:")) {
			return errors.New("production full mode requires STUN and TURN URLs")
		}
		if c.RTCTURNUsername == "" || len(c.RTCTURNCredential) < 16 {
			return errors.New("production full mode requires TURN username and a credential of at least 16 bytes")
		}
	}
	return nil
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

func hasURLPrefix(values []string, prefix string) bool {
	for _, candidate := range values {
		if strings.HasPrefix(strings.ToLower(candidate), prefix) {
			return true
		}
	}
	return false
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
