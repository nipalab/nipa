package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

// clearConfigEnv removes environment variables that AutomaticEnv would
// otherwise pick up and override file-based values.
func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DATABASE_DSN", "SERVER_ADDRESS", "SERVER_PORT", "JWT_KEY", "LOG_LEVEL", "LOG_FORMAT", "PPROF_ENABLED", "METRICS_ENABLED", "HASHER_WORKERS", "SNOWFLAKE_NODE_ID", "CHUNK_STORAGE", "CHUNK_STORAGE_DIR", "CHUNK_S3_ENDPOINT", "CHUNK_S3_REGION", "CHUNK_S3_BUCKET", "CHUNK_S3_PREFIX", "CHUNK_S3_ACCESS_KEY_ID", "CHUNK_S3_SECRET_ACCESS_KEY", "CHUNK_URL_SIGNING_KEY", "CHUNK_PRESIGN_TTL_SECONDS", "CHUNK_MAX_PAGE_SIZE", "EMAIL_SENDER", "EMAIL_FROM", "EMAIL_REPLY_TO", "EMAIL_BASE_URL", "EMAIL_TIMEOUT_SECONDS", "EMAIL_MAX_ATTEMPTS", "EMAIL_RETRY_BACKOFF_SECONDS", "EMAIL_POLL_SECONDS", "EMAIL_SMTP_HOST", "EMAIL_SMTP_PORT", "EMAIL_SMTP_USERNAME", "EMAIL_SMTP_PASSWORD", "EMAIL_SMTP_TLS", "EMAIL_SMTP_INSECURE_SKIP_VERIFY", "EMAIL_SENDGRID_API_KEY", "EMAIL_SENDGRID_ENDPOINT", "EMAIL_HTTP_ENDPOINT", "EMAIL_HTTP_METHOD", "EMAIL_HTTP_HEADERS", "EMAIL_HTTP_CONTENT_TYPE", "EMAIL_HTTP_BODY_TEMPLATE"} {
		if old, ok := os.LookupEnv(k); ok {
			_ = os.Unsetenv(k)
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
	}
}

const testYAML = `SERVER_ADDRESS: 127.0.0.1
SERVER_PORT: 6745
DATABASE_DSN: sqlite://nipa.db
JWT_KEY: yaml-secret
LOG_LEVEL: debug
LOG_FORMAT: json
PPROF_ENABLED: true
METRICS_ENABLED: false
CHUNK_STORAGE: s3
CHUNK_STORAGE_DIR: ./chunks
CHUNK_S3_ENDPOINT: http://minio:9000
CHUNK_S3_REGION: eu-central-1
CHUNK_S3_BUCKET: nipa-chunks
CHUNK_S3_PREFIX: tenant-a
CHUNK_S3_ACCESS_KEY_ID: minio-access
CHUNK_S3_SECRET_ACCESS_KEY: minio-secret
CHUNK_URL_SIGNING_KEY: yaml-chunk-secret
CHUNK_PRESIGN_TTL_SECONDS: 120
CHUNK_MAX_PAGE_SIZE: 50
EMAIL_SENDER: smtp
EMAIL_FROM: Nipa <noreply@example.com>
EMAIL_REPLY_TO: support@example.com
EMAIL_BASE_URL: https://nipa.example.com
EMAIL_TIMEOUT_SECONDS: 15
EMAIL_MAX_ATTEMPTS: 4
EMAIL_RETRY_BACKOFF_SECONDS: 3
EMAIL_POLL_SECONDS: 2
EMAIL_SMTP_HOST: smtp.example.com
EMAIL_SMTP_PORT: 2525
EMAIL_SMTP_USERNAME: mailer
EMAIL_SMTP_PASSWORD: mailer-secret
EMAIL_SMTP_TLS: starttls
EMAIL_SMTP_INSECURE_SKIP_VERIFY: true
EMAIL_HTTP_ENDPOINT: https://mail.example.com/send
EMAIL_HTTP_METHOD: PUT
EMAIL_HTTP_HEADERS: '{"X-Api-Key":"secret"}'
EMAIL_HTTP_CONTENT_TYPE: application/json
EMAIL_HTTP_BODY_TEMPLATE: '{"to":{{json .To}}}'
`

func TestLoadConfig_FromYAML(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", testYAML)
	t.Chdir(dir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", cfg.ServerAddress)
	require.Equal(t, 6745, cfg.ServerPort)
	require.Equal(t, "sqlite://nipa.db", cfg.DatabaseDSN)
	require.Equal(t, "yaml-secret", cfg.JWTKey)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, "json", cfg.LogFormat)
	require.True(t, cfg.PprofEnabled)
	require.False(t, cfg.MetricsEnabled)
	require.Equal(t, "s3", cfg.ChunkStorage)
	require.Equal(t, "./chunks", cfg.ChunkStorageDir)
	require.Equal(t, "http://minio:9000", cfg.ChunkS3Endpoint)
	require.Equal(t, "eu-central-1", cfg.ChunkS3Region)
	require.Equal(t, "nipa-chunks", cfg.ChunkS3Bucket)
	require.Equal(t, "tenant-a", cfg.ChunkS3Prefix)
	require.Equal(t, "minio-access", cfg.ChunkS3AccessKeyID)
	require.Equal(t, "minio-secret", cfg.ChunkS3SecretAccessKey)
	require.Equal(t, "yaml-chunk-secret", cfg.ChunkURLSigningKey)
	require.Equal(t, 120, cfg.ChunkPresignTTLSeconds)
	require.Equal(t, 50, cfg.ChunkMaxPageSize)
	require.Equal(t, "smtp", cfg.EmailSender)
	require.Equal(t, "Nipa <noreply@example.com>", cfg.EmailFrom)
	require.Equal(t, "support@example.com", cfg.EmailReplyTo)
	require.Equal(t, "https://nipa.example.com", cfg.EmailBaseURL)
	require.Equal(t, 15, cfg.EmailTimeoutSeconds)
	require.Equal(t, 4, cfg.EmailMaxAttempts)
	require.Equal(t, 3, cfg.EmailRetryBackoffSecs)
	require.Equal(t, 2, cfg.EmailPollSeconds)
	require.Equal(t, "smtp.example.com", cfg.EmailSMTPHost)
	require.Equal(t, 2525, cfg.EmailSMTPPort)
	require.Equal(t, "mailer", cfg.EmailSMTPUsername)
	require.Equal(t, "mailer-secret", cfg.EmailSMTPPassword)
	require.Equal(t, "starttls", cfg.EmailSMTPTLS)
	require.True(t, cfg.EmailSMTPInsecureTLS)
	require.Equal(t, "https://mail.example.com/send", cfg.EmailHTTPEndpoint)
	require.Equal(t, "PUT", cfg.EmailHTTPMethod)
	require.Equal(t, `{"X-Api-Key":"secret"}`, cfg.EmailHTTPHeaders)
	require.Equal(t, "application/json", cfg.EmailHTTPContentType)
	require.Equal(t, `{"to":{{json .To}}}`, cfg.EmailHTTPBodyTemplate)
}

func TestLoadConfig_ChunkURLDefaults(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	t.Chdir(dir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Empty(t, cfg.ChunkStorage)
	require.Equal(t, 3600, cfg.ChunkPresignTTLSeconds)
	require.Equal(t, 1000, cfg.ChunkMaxPageSize)
	require.Empty(t, cfg.ChunkURLSigningKey)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "console", cfg.LogFormat)
	require.False(t, cfg.PprofEnabled)
	require.True(t, cfg.MetricsEnabled)
}

func TestLoadConfig_EnvFileOverridesYAML(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", testYAML)
	writeFile(t, dir, ".env", "SERVER_ADDRESS=0.0.0.0\nJWT_KEY=dotenv-secret\n")
	t.Chdir(dir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "0.0.0.0", cfg.ServerAddress)
	require.Equal(t, "dotenv-secret", cfg.JWTKey)
	require.Equal(t, 6745, cfg.ServerPort)
}

func TestLoadConfig_ProcessEnvOverridesFiles(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", testYAML)
	t.Chdir(dir)

	t.Setenv("SERVER_PORT", "8080")
	t.Setenv("JWT_KEY", "env-secret")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, 8080, cfg.ServerPort)
	require.Equal(t, "env-secret", cfg.JWTKey)
}

func TestLoadConfig_MissingConfigFile(t *testing.T) {
	clearConfigEnv(t)

	t.Chdir(t.TempDir())

	cfg, err := LoadConfig()
	require.Error(t, err)
	require.Nil(t, cfg)
}
