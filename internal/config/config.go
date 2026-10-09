package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	DatabaseDSN            string `mapstructure:"DATABASE_DSN"`
	ServerAddress          string `mapstructure:"SERVER_ADDRESS"`
	ServerPort             int    `mapstructure:"SERVER_PORT"`
	JWTKey                 string `mapstructure:"JWT_KEY"`
	LogLevel               string `mapstructure:"LOG_LEVEL"`
	SnowflakeNodeID        int64  `mapstructure:"SNOWFLAKE_NODE_ID"`
	HasherWorkers          int    `mapstructure:"HASHER_WORKERS"`
	ChunkStorage           string `mapstructure:"CHUNK_STORAGE"`
	ChunkStorageDir        string `mapstructure:"CHUNK_STORAGE_DIR"`
	ChunkS3Endpoint        string `mapstructure:"CHUNK_S3_ENDPOINT"`
	ChunkS3Region          string `mapstructure:"CHUNK_S3_REGION"`
	ChunkS3Bucket          string `mapstructure:"CHUNK_S3_BUCKET"`
	ChunkS3Prefix          string `mapstructure:"CHUNK_S3_PREFIX"`
	ChunkS3AccessKeyID     string `mapstructure:"CHUNK_S3_ACCESS_KEY_ID"`
	ChunkS3SecretAccessKey string `mapstructure:"CHUNK_S3_SECRET_ACCESS_KEY"`
	ChunkURLSigningKey     string `mapstructure:"CHUNK_URL_SIGNING_KEY"`
	ChunkPresignTTLSeconds int    `mapstructure:"CHUNK_PRESIGN_TTL_SECONDS"`
	ChunkMaxPageSize       int    `mapstructure:"CHUNK_MAX_PAGE_SIZE"`
	WebhookEgressAllowlist string `mapstructure:"WEBHOOK_EGRESS_ALLOWLIST"`
	WebhookTimeoutSeconds  int    `mapstructure:"WEBHOOK_TIMEOUT_SECONDS"`
	EmailSender            string `mapstructure:"EMAIL_SENDER"`
	EmailFrom              string `mapstructure:"EMAIL_FROM"`
	EmailReplyTo           string `mapstructure:"EMAIL_REPLY_TO"`
	EmailBaseURL           string `mapstructure:"EMAIL_BASE_URL"`
	EmailTimeoutSeconds    int    `mapstructure:"EMAIL_TIMEOUT_SECONDS"`
	EmailSMTPHost          string `mapstructure:"EMAIL_SMTP_HOST"`
	EmailSMTPPort          int    `mapstructure:"EMAIL_SMTP_PORT"`
	EmailSMTPUsername      string `mapstructure:"EMAIL_SMTP_USERNAME"`
	EmailSMTPPassword      string `mapstructure:"EMAIL_SMTP_PASSWORD"`
	EmailSMTPTLS           string `mapstructure:"EMAIL_SMTP_TLS"`
	EmailSMTPInsecureTLS   bool   `mapstructure:"EMAIL_SMTP_INSECURE_SKIP_VERIFY"`
	EmailSendGridAPIKey    string `mapstructure:"EMAIL_SENDGRID_API_KEY"`
	EmailSendGridEndpoint  string `mapstructure:"EMAIL_SENDGRID_ENDPOINT"`
	EmailHTTPEndpoint      string `mapstructure:"EMAIL_HTTP_ENDPOINT"`
	EmailHTTPMethod        string `mapstructure:"EMAIL_HTTP_METHOD"`
	EmailHTTPHeaders       string `mapstructure:"EMAIL_HTTP_HEADERS"`
	EmailHTTPContentType   string `mapstructure:"EMAIL_HTTP_CONTENT_TYPE"`
	EmailHTTPBodyTemplate  string `mapstructure:"EMAIL_HTTP_BODY_TEMPLATE"`
}

func LoadConfig() (*Config, error) {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	v.SetDefault("CHUNK_PRESIGN_TTL_SECONDS", 3600)
	v.SetDefault("CHUNK_MAX_PAGE_SIZE", 1000)

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	v.SetConfigFile(".env")
	v.SetConfigType("env")
	_ = v.MergeInConfig()

	v.AutomaticEnv()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unable to parse config: %w", err)
	}

	return &cfg, nil
}
