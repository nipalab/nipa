package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"

	"github.com/nipalab/nipa/internal/sshkey"
)

type Config struct {
	DatabaseDSN                    string  `mapstructure:"DATABASE_DSN"`
	ServerAddress                  string  `mapstructure:"SERVER_ADDRESS"`
	ServerPort                     int     `mapstructure:"SERVER_PORT"`
	ShutdownTimeoutSeconds         int     `mapstructure:"SHUTDOWN_TIMEOUT_SECONDS"`
	JWTKey                         string  `mapstructure:"JWT_KEY"`
	LogLevel                       string  `mapstructure:"LOG_LEVEL"`
	LogFormat                      string  `mapstructure:"LOG_FORMAT"`
	PprofEnabled                   bool    `mapstructure:"PPROF_ENABLED"`
	MetricsEnabled                 bool    `mapstructure:"METRICS_ENABLED"`
	TracingEnabled                 bool    `mapstructure:"TRACING_ENABLED"`
	TracingOTLPEndpoint            string  `mapstructure:"TRACING_OTLP_ENDPOINT"`
	TracingOTLPProtocol            string  `mapstructure:"TRACING_OTLP_PROTOCOL"`
	TracingOTLPHeaders             string  `mapstructure:"TRACING_OTLP_HEADERS"`
	TracingStdout                  bool    `mapstructure:"TRACING_STDOUT"`
	TracingSampleRatio             float64 `mapstructure:"TRACING_SAMPLE_RATIO"`
	TracingServiceName             string  `mapstructure:"TRACING_SERVICE_NAME"`
	LogsOTLPEnabled                bool    `mapstructure:"LOGS_OTLP_ENABLED"`
	LogsOTLPEndpoint               string  `mapstructure:"LOGS_OTLP_ENDPOINT"`
	LogsOTLPProtocol               string  `mapstructure:"LOGS_OTLP_PROTOCOL"`
	MetricsOTLPEnabled             bool    `mapstructure:"METRICS_OTLP_ENABLED"`
	MetricsOTLPEndpoint            string  `mapstructure:"METRICS_OTLP_ENDPOINT"`
	MetricsOTLPProtocol            string  `mapstructure:"METRICS_OTLP_PROTOCOL"`
	MetricsOTLPIntervalSeconds     int     `mapstructure:"METRICS_OTLP_INTERVAL_SECONDS"`
	SnowflakeNodeID                int64   `mapstructure:"SNOWFLAKE_NODE_ID"`
	SnowflakeNodeIDSet             bool    `mapstructure:"-"`
	SnowflakeLeaseTTLSecs          int     `mapstructure:"SNOWFLAKE_LEASE_TTL_SECONDS"`
	HasherWorkers                  int     `mapstructure:"HASHER_WORKERS"`
	ChunkStorage                   string  `mapstructure:"CHUNK_STORAGE"`
	ChunkStorageDir                string  `mapstructure:"CHUNK_STORAGE_DIR"`
	ChunkS3Endpoint                string  `mapstructure:"CHUNK_S3_ENDPOINT"`
	ChunkS3Region                  string  `mapstructure:"CHUNK_S3_REGION"`
	ChunkS3Bucket                  string  `mapstructure:"CHUNK_S3_BUCKET"`
	ChunkS3Prefix                  string  `mapstructure:"CHUNK_S3_PREFIX"`
	ChunkS3AccessKeyID             string  `mapstructure:"CHUNK_S3_ACCESS_KEY_ID"`
	ChunkS3SecretAccessKey         string  `mapstructure:"CHUNK_S3_SECRET_ACCESS_KEY"`
	ChunkURLSigningKey             string  `mapstructure:"CHUNK_URL_SIGNING_KEY"`
	AllowInsecureDevSecrets        bool    `mapstructure:"ALLOW_INSECURE_DEV_SECRETS"`
	ChunkPresignTTLSeconds         int     `mapstructure:"CHUNK_PRESIGN_TTL_SECONDS"`
	ChunkMaxPageSize               int     `mapstructure:"CHUNK_MAX_PAGE_SIZE"`
	StorageLedger                  string  `mapstructure:"STORAGE_LEDGER"`
	StorageLedgerDefaultQuotaBytes int64   `mapstructure:"STORAGE_LEDGER_DEFAULT_QUOTA_BYTES"`
	SaaSMode                       bool    `mapstructure:"SAAS_MODE"`
	SaasFreeQuotaBytes             int64   `mapstructure:"SAAS_FREE_QUOTA_BYTES"`
	SaasTermsVersion               string  `mapstructure:"SAAS_TERMS_VERSION"`
	BillingProvider                string  `mapstructure:"BILLING_PROVIDER"`
	BillingFreeSeats               int64   `mapstructure:"BILLING_FREE_SEATS"`
	BillingSeatIncludedBytes       int64   `mapstructure:"BILLING_SEAT_INCLUDED_BYTES"`
	BillingStoragePackBytes        int64   `mapstructure:"BILLING_STORAGE_PACK_BYTES"`
	PaddleEnvironment              string  `mapstructure:"PADDLE_ENVIRONMENT"`
	PaddleAPIKey                   string  `mapstructure:"PADDLE_API_KEY"`
	PaddleWebhookSecret            string  `mapstructure:"PADDLE_WEBHOOK_SECRET"`
	PaddleClientToken              string  `mapstructure:"PADDLE_CLIENT_TOKEN"`
	PaddlePriceSeatMonthly         string  `mapstructure:"PADDLE_PRICE_SEAT_MONTHLY"`
	PaddlePriceSeatYearly          string  `mapstructure:"PADDLE_PRICE_SEAT_YEARLY"`
	PaddlePriceStorageMonthly      string  `mapstructure:"PADDLE_PRICE_STORAGE_MONTHLY"`
	PaddlePriceStorageYearly       string  `mapstructure:"PADDLE_PRICE_STORAGE_YEARLY"`
	OAuthBaseURL                   string  `mapstructure:"OAUTH_BASE_URL"`
	OAuthGoogleClientID            string  `mapstructure:"OAUTH_GOOGLE_CLIENT_ID"`
	OAuthGoogleClientSecret        string  `mapstructure:"OAUTH_GOOGLE_CLIENT_SECRET"`
	OAuthGitHubClientID            string  `mapstructure:"OAUTH_GITHUB_CLIENT_ID"`
	OAuthGitHubClientSecret        string  `mapstructure:"OAUTH_GITHUB_CLIENT_SECRET"`
	WebhookEgressAllowlist         string  `mapstructure:"WEBHOOK_EGRESS_ALLOWLIST"`
	WebhookTimeoutSeconds          int     `mapstructure:"WEBHOOK_TIMEOUT_SECONDS"`
	EmailSender                    string  `mapstructure:"EMAIL_SENDER"`
	EmailFrom                      string  `mapstructure:"EMAIL_FROM"`
	EmailReplyTo                   string  `mapstructure:"EMAIL_REPLY_TO"`
	EmailBaseURL                   string  `mapstructure:"EMAIL_BASE_URL"`
	PublicBaseURL                  string  `mapstructure:"PUBLIC_BASE_URL"`
	EmailTimeoutSeconds            int     `mapstructure:"EMAIL_TIMEOUT_SECONDS"`
	EmailMaxAttempts               int     `mapstructure:"EMAIL_MAX_ATTEMPTS"`
	EmailRetryBackoffSecs          int     `mapstructure:"EMAIL_RETRY_BACKOFF_SECONDS"`
	EmailPollSeconds               int     `mapstructure:"EMAIL_POLL_SECONDS"`
	EmailSMTPHost                  string  `mapstructure:"EMAIL_SMTP_HOST"`
	EmailSMTPPort                  int     `mapstructure:"EMAIL_SMTP_PORT"`
	EmailSMTPUsername              string  `mapstructure:"EMAIL_SMTP_USERNAME"`
	EmailSMTPPassword              string  `mapstructure:"EMAIL_SMTP_PASSWORD"`
	EmailSMTPTLS                   string  `mapstructure:"EMAIL_SMTP_TLS"`
	EmailSMTPInsecureTLS           bool    `mapstructure:"EMAIL_SMTP_INSECURE_SKIP_VERIFY"`
	EmailSendGridAPIKey            string  `mapstructure:"EMAIL_SENDGRID_API_KEY"`
	EmailSendGridEndpoint          string  `mapstructure:"EMAIL_SENDGRID_ENDPOINT"`
	EmailScalewayAPIKey            string  `mapstructure:"EMAIL_SCALEWAY_API_KEY"`
	EmailScalewayProjectID         string  `mapstructure:"EMAIL_SCALEWAY_PROJECT_ID"`
	EmailScalewayRegion            string  `mapstructure:"EMAIL_SCALEWAY_REGION"`
	EmailScalewayEndpoint          string  `mapstructure:"EMAIL_SCALEWAY_ENDPOINT"`
	EmailHTTPEndpoint              string  `mapstructure:"EMAIL_HTTP_ENDPOINT"`
	EmailHTTPMethod                string  `mapstructure:"EMAIL_HTTP_METHOD"`
	EmailHTTPHeaders               string  `mapstructure:"EMAIL_HTTP_HEADERS"`
	EmailHTTPContentType           string  `mapstructure:"EMAIL_HTTP_CONTENT_TYPE"`
	EmailHTTPBodyTemplate          string  `mapstructure:"EMAIL_HTTP_BODY_TEMPLATE"`
	RateLimitEnabled               bool    `mapstructure:"RATE_LIMIT_ENABLED"`
	RateLimitStore                 string  `mapstructure:"RATE_LIMIT_STORE"`
	RateLimitTrustedProxies        string  `mapstructure:"RATE_LIMIT_TRUSTED_PROXIES"`
	RateLimitRedisAddr             string  `mapstructure:"RATE_LIMIT_REDIS_ADDR"`
	RateLimitRedisPassword         string  `mapstructure:"RATE_LIMIT_REDIS_PASSWORD"`
	RateLimitRedisDB               int     `mapstructure:"RATE_LIMIT_REDIS_DB"`
	RateLimitRedisTLS              bool    `mapstructure:"RATE_LIMIT_REDIS_TLS"`
	RateLimitRedisPrefix           string  `mapstructure:"RATE_LIMIT_REDIS_PREFIX"`
	RateLimitAuthIPPerMinute       float64 `mapstructure:"RATE_LIMIT_AUTH_IP_PER_MINUTE"`
	RateLimitAuthIPBurst           int     `mapstructure:"RATE_LIMIT_AUTH_IP_BURST"`
	RateLimitAuthIDPerMinute       float64 `mapstructure:"RATE_LIMIT_AUTH_ID_PER_MINUTE"`
	RateLimitAuthIDBurst           int     `mapstructure:"RATE_LIMIT_AUTH_ID_BURST"`
	RateLimitAPIUserPerMinute      float64 `mapstructure:"RATE_LIMIT_API_USER_PER_MINUTE"`
	RateLimitAPIUserBurst          int     `mapstructure:"RATE_LIMIT_API_USER_BURST"`
	RateLimitChunkIPPerMinute      float64 `mapstructure:"RATE_LIMIT_CHUNK_IP_PER_MINUTE"`
	RateLimitChunkIPBurst          int     `mapstructure:"RATE_LIMIT_CHUNK_IP_BURST"`
	RateLimitGlobalIPPerMinute     float64 `mapstructure:"RATE_LIMIT_GLOBAL_IP_PER_MINUTE"`
	RateLimitGlobalIPBurst         int     `mapstructure:"RATE_LIMIT_GLOBAL_IP_BURST"`
	RateLimitInviteUserPerMinute   float64 `mapstructure:"RATE_LIMIT_INVITE_USER_PER_MINUTE"`
	RateLimitInviteUserBurst       int     `mapstructure:"RATE_LIMIT_INVITE_USER_BURST"`
	RateLimitInviteEmailPerMinute  float64 `mapstructure:"RATE_LIMIT_INVITE_EMAIL_PER_MINUTE"`
	RateLimitInviteEmailBurst      int     `mapstructure:"RATE_LIMIT_INVITE_EMAIL_BURST"`
	RateLimitInviteTokenPerMinute  float64 `mapstructure:"RATE_LIMIT_INVITE_TOKEN_PER_MINUTE"`
	RateLimitInviteTokenBurst      int     `mapstructure:"RATE_LIMIT_INVITE_TOKEN_BURST"`
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	v.SetDefault("CHUNK_PRESIGN_TTL_SECONDS", 3600)
	v.SetDefault("CHUNK_MAX_PAGE_SIZE", 1000)
	v.SetDefault("SHUTDOWN_TIMEOUT_SECONDS", 15)
	v.SetDefault("SNOWFLAKE_LEASE_TTL_SECONDS", 60)
	v.SetDefault("STORAGE_LEDGER_DEFAULT_QUOTA_BYTES", int64(15*1024*1024*1024))
	v.SetDefault("SAAS_MODE", false)
	v.SetDefault("ALLOW_INSECURE_DEV_SECRETS", false)
	v.SetDefault("SAAS_FREE_QUOTA_BYTES", int64(15*1024*1024*1024))
	v.SetDefault("SAAS_TERMS_VERSION", "")
	v.SetDefault("BILLING_PROVIDER", BillingProviderOff)
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_FORMAT", "console")
	v.SetDefault("PPROF_ENABLED", false)
	v.SetDefault("METRICS_ENABLED", true)
	v.SetDefault("TRACING_ENABLED", false)
	v.SetDefault("TRACING_STDOUT", false)
	v.SetDefault("TRACING_SAMPLE_RATIO", 1.0)
	v.SetDefault("LOGS_OTLP_ENABLED", false)
	v.SetDefault("METRICS_OTLP_ENABLED", false)
	v.SetDefault("METRICS_OTLP_INTERVAL_SECONDS", 30)
	v.SetDefault("RATE_LIMIT_ENABLED", true)
	v.SetDefault("RATE_LIMIT_TRUSTED_PROXIES", "127.0.0.1,::1")
	v.SetDefault("RATE_LIMIT_REDIS_PREFIX", "nipa:rl:")
	v.SetDefault("RATE_LIMIT_AUTH_IP_PER_MINUTE", 10.0)
	v.SetDefault("RATE_LIMIT_AUTH_IP_BURST", 10)
	v.SetDefault("RATE_LIMIT_AUTH_ID_PER_MINUTE", 5.0/15.0)
	v.SetDefault("RATE_LIMIT_AUTH_ID_BURST", 5)
	v.SetDefault("RATE_LIMIT_API_USER_PER_MINUTE", 3000.0)
	v.SetDefault("RATE_LIMIT_API_USER_BURST", 200)
	v.SetDefault("RATE_LIMIT_CHUNK_IP_PER_MINUTE", 60000.0)
	v.SetDefault("RATE_LIMIT_CHUNK_IP_BURST", 5000)
	v.SetDefault("RATE_LIMIT_INVITE_USER_PER_MINUTE", 0.5)
	v.SetDefault("RATE_LIMIT_INVITE_USER_BURST", 20)
	v.SetDefault("RATE_LIMIT_INVITE_EMAIL_PER_MINUTE", 0.1)
	v.SetDefault("RATE_LIMIT_INVITE_EMAIL_BURST", 3)
	v.SetDefault("RATE_LIMIT_INVITE_TOKEN_PER_MINUTE", 20.0)
	v.SetDefault("RATE_LIMIT_INVITE_TOKEN_BURST", 10)
	v.SetDefault("RATE_LIMIT_GLOBAL_IP_PER_MINUTE", 6000.0)
	v.SetDefault("RATE_LIMIT_GLOBAL_IP_BURST", 300)

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	v.SetConfigFile(".env")
	v.SetConfigType("env")
	_ = v.MergeInConfig()

	v.AutomaticEnv()
	// AutomaticEnv alone does not feed Unmarshal for keys missing from the
	// config file, so the per-color systemd env files must bind explicitly.
	for _, key := range []string{"SERVER_ADDRESS", "SERVER_PORT", "SHUTDOWN_TIMEOUT_SECONDS", "SNOWFLAKE_NODE_ID", "PUBLIC_BASE_URL",
		"RATE_LIMIT_STORE", "RATE_LIMIT_REDIS_ADDR", "RATE_LIMIT_REDIS_PASSWORD", "RATE_LIMIT_REDIS_DB", "RATE_LIMIT_REDIS_TLS",
		"BILLING_FREE_SEATS", "BILLING_SEAT_INCLUDED_BYTES", "BILLING_STORAGE_PACK_BYTES", "PADDLE_ENVIRONMENT",
		"PADDLE_API_KEY", "PADDLE_WEBHOOK_SECRET", "PADDLE_CLIENT_TOKEN", "PADDLE_PRICE_SEAT_MONTHLY",
		"PADDLE_PRICE_SEAT_YEARLY", "PADDLE_PRICE_STORAGE_MONTHLY", "PADDLE_PRICE_STORAGE_YEARLY"} {
		if err := v.BindEnv(key); err != nil {
			return nil, fmt.Errorf("bind %s: %w", key, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to parse config: %w", err)
	}
	cfg.SnowflakeNodeIDSet = v.IsSet("SNOWFLAKE_NODE_ID")

	return &cfg, nil
}

// SSHLoginAudience returns the canonical origin SSH login signatures must be
// bound to: PUBLIC_BASE_URL, else the host of EMAIL_BASE_URL. It is empty when
// neither is set (SSH login is then refused).
func (c *Config) SSHLoginAudience() (string, error) {
	for _, raw := range []string{c.PublicBaseURL, c.EmailBaseURL} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		origin, err := sshkey.CanonicalOrigin(raw)
		if err != nil {
			return "", fmt.Errorf("invalid public base URL %q: %w", raw, err)
		}
		return origin, nil
	}
	return "", nil
}

const minSecretLength = 32

var sampleSecrets = map[string]bool{"secret": true, "change-me": true, "changeme": true, "password": true, "jwt-secret": true, "test": true}

func isSampleSecret(v string) bool {
	l := strings.ToLower(v)
	return sampleSecrets[l] || strings.HasPrefix(l, "change-me") || strings.HasPrefix(l, "changeme")
}

// ValidateSecretsOrWarn behaves like ValidateSecrets unless
// ALLOW_INSECURE_DEV_SECRETS is set, in which case the problem is handed to
// warn and startup continues.
func (c *Config) ValidateSecretsOrWarn(warn func(msg string, err error)) error {
	err := c.ValidateSecrets()
	if err == nil || !c.AllowInsecureDevSecrets {
		return err
	}
	warn("ALLOW_INSECURE_DEV_SECRETS is set: running with insecure signing keys, sessions can be forged; never use this in production", err)
	return nil
}

// ValidateSecrets rejects missing, sample or short signing keys so a production
// server never starts with forgeable sessions or chunk URLs.
func (c *Config) ValidateSecrets() error {
	for _, s := range []struct{ name, value string }{
		{"JWT_KEY", c.JWTKey},
		{"CHUNK_URL_SIGNING_KEY", c.ChunkURLSigningKey},
	} {
		switch {
		case s.value == "":
			return fmt.Errorf("%s must be set", s.name)
		case isSampleSecret(s.value):
			return fmt.Errorf("%s still has the sample value; generate one with `openssl rand -base64 48`", s.name)
		case len(s.value) < minSecretLength:
			return fmt.Errorf("%s must be at least %d characters", s.name, minSecretLength)
		}
	}
	if c.JWTKey == c.ChunkURLSigningKey {
		return fmt.Errorf("JWT_KEY and CHUNK_URL_SIGNING_KEY must differ")
	}
	return nil
}
