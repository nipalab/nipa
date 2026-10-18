package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseDSN                    string  `mapstructure:"DATABASE_DSN"`
	ServerAddress                  string  `mapstructure:"SERVER_ADDRESS"`
	ServerPort                     int     `mapstructure:"SERVER_PORT"`
	JWTKey                         string  `mapstructure:"JWT_KEY"`
	LogLevel                       string  `mapstructure:"LOG_LEVEL"`
	LogFormat                      string  `mapstructure:"LOG_FORMAT"`
	PprofEnabled                   bool    `mapstructure:"PPROF_ENABLED"`
	MetricsEnabled                 bool    `mapstructure:"METRICS_ENABLED"`
	TracingEnabled                 bool    `mapstructure:"TRACING_ENABLED"`
	TracingOTLPEndpoint            string  `mapstructure:"TRACING_OTLP_ENDPOINT"`
	TracingStdout                  bool    `mapstructure:"TRACING_STDOUT"`
	TracingSampleRatio             float64 `mapstructure:"TRACING_SAMPLE_RATIO"`
	TracingServiceName             string  `mapstructure:"TRACING_SERVICE_NAME"`
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
	ChunkPresignTTLSeconds         int     `mapstructure:"CHUNK_PRESIGN_TTL_SECONDS"`
	ChunkMaxPageSize               int     `mapstructure:"CHUNK_MAX_PAGE_SIZE"`
	StorageLedger                  string  `mapstructure:"STORAGE_LEDGER"`
	StorageLedgerDefaultQuotaBytes int64   `mapstructure:"STORAGE_LEDGER_DEFAULT_QUOTA_BYTES"`
	SaaSMode                       bool    `mapstructure:"SAAS_MODE"`
	SaasFreeQuotaBytes             int64   `mapstructure:"SAAS_FREE_QUOTA_BYTES"`
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
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	v.SetDefault("CHUNK_PRESIGN_TTL_SECONDS", 3600)
	v.SetDefault("CHUNK_MAX_PAGE_SIZE", 1000)
	v.SetDefault("SNOWFLAKE_LEASE_TTL_SECONDS", 60)
	v.SetDefault("STORAGE_LEDGER_DEFAULT_QUOTA_BYTES", int64(15*1024*1024*1024))
	v.SetDefault("SAAS_MODE", false)
	v.SetDefault("SAAS_FREE_QUOTA_BYTES", int64(15*1024*1024*1024))
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_FORMAT", "console")
	v.SetDefault("PPROF_ENABLED", false)
	v.SetDefault("METRICS_ENABLED", true)
	v.SetDefault("TRACING_ENABLED", false)
	v.SetDefault("TRACING_STDOUT", false)
	v.SetDefault("TRACING_SAMPLE_RATIO", 1.0)

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	v.SetConfigFile(".env")
	v.SetConfigType("env")
	_ = v.MergeInConfig()

	v.AutomaticEnv()
	if err := v.BindEnv("SNOWFLAKE_NODE_ID"); err != nil {
		return nil, fmt.Errorf("bind SNOWFLAKE_NODE_ID: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to parse config: %w", err)
	}
	cfg.SnowflakeNodeIDSet = v.IsSet("SNOWFLAKE_NODE_ID")

	return &cfg, nil
}
