package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
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
	for _, k := range envKeys(reflect.TypeOf(Config{})) {
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
CHUNK_VERIFY_CONCURRENCY: 16
PUSH_MAX_FILES: 300
CHUNK_VISIBILITY_CACHE_HASHES: 400
CHUNK_VISIBILITY_TIMEOUT_SECONDS: 45
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
EMAIL_SCALEWAY_API_KEY: scw-secret
EMAIL_SCALEWAY_PROJECT_ID: 11111111-2222-3333-4444-555555555555
EMAIL_SCALEWAY_REGION: nl-ams
EMAIL_SCALEWAY_ENDPOINT: https://api.scaleway.test
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
	require.Equal(t, 16, cfg.ChunkVerifyConcurrency)
	require.Equal(t, 300, cfg.PushMaxFiles)
	require.Equal(t, 400, cfg.ChunkVisibilityCacheHashes)
	require.Equal(t, 45, cfg.ChunkVisibilityTimeoutSeconds)
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
	require.Equal(t, "scw-secret", cfg.EmailScalewayAPIKey)
	require.Equal(t, "11111111-2222-3333-4444-555555555555", cfg.EmailScalewayProjectID)
	require.Equal(t, "nl-ams", cfg.EmailScalewayRegion)
	require.Equal(t, "https://api.scaleway.test", cfg.EmailScalewayEndpoint)
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
	require.Equal(t, 64, cfg.ChunkVerifyConcurrency)
	require.Equal(t, 2_000_000, cfg.PushMaxFiles)
	require.Equal(t, 2_000_000, cfg.ChunkVisibilityCacheHashes)
	require.Equal(t, 30, cfg.ChunkVisibilityTimeoutSeconds)
	require.Empty(t, cfg.ChunkURLSigningKey)
	require.Equal(t, "info", cfg.LogLevel)
	require.Equal(t, "console", cfg.LogFormat)
	require.False(t, cfg.PprofEnabled)
	require.True(t, cfg.MetricsEnabled)
	require.False(t, cfg.TracingEnabled)
	require.Empty(t, cfg.TracingOTLPEndpoint, "the endpoint must stay unset so the standard OTEL_* variables are honored")
	require.False(t, cfg.TracingStdout)
	require.Equal(t, 1.0, cfg.TracingSampleRatio)
	require.Empty(t, cfg.TracingServiceName)
	require.Empty(t, cfg.TracingOTLPHeaders)
	require.Empty(t, cfg.TracingOTLPProtocol)
	require.False(t, cfg.LogsOTLPEnabled)
	require.Empty(t, cfg.LogsOTLPEndpoint)
	require.Empty(t, cfg.LogsOTLPProtocol)
	require.False(t, cfg.MetricsOTLPEnabled)
	require.Empty(t, cfg.MetricsOTLPEndpoint)
	require.Empty(t, cfg.MetricsOTLPProtocol)
	require.Equal(t, 30, cfg.MetricsOTLPIntervalSeconds)
	require.Equal(t, 15, cfg.ShutdownTimeoutSeconds)
	require.Equal(t, 60, cfg.SnowflakeLeaseTTLSecs)
	require.False(t, cfg.SnowflakeNodeIDSet)
	require.Equal(t, int64(15*1024*1024*1024), cfg.StorageLedgerDefaultQuotaBytes)
	require.False(t, cfg.SaaSMode)
	require.Equal(t, int64(15*1024*1024*1024), cfg.SaasFreeQuotaBytes)
	require.Equal(t, BillingProviderOff, cfg.BillingProvider)
	require.Zero(t, cfg.BillingFreeSeats, "no commercial defaults ship with the code")
	require.Zero(t, cfg.BillingSeatIncludedBytes)
	require.Zero(t, cfg.BillingStoragePackBytes)
}

func TestLoadConfig_BillingFromEnv(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	t.Chdir(dir)
	t.Setenv("BILLING_PROVIDER", "paddle")
	t.Setenv("PADDLE_API_KEY", "env-key")
	t.Setenv("PADDLE_PRICE_STORAGE_YEARLY", "pri_storage_y")
	t.Setenv("BILLING_FREE_SEATS", "3")
	t.Setenv("BILLING_STORAGE_PACK_BYTES", "1024")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "paddle", cfg.BillingProvider)
	require.Equal(t, "env-key", cfg.PaddleAPIKey)
	require.Equal(t, "pri_storage_y", cfg.PaddlePriceStorageYearly)
	require.Equal(t, int64(3), cfg.BillingFreeSeats)
	require.Equal(t, int64(1024), cfg.BillingStoragePackBytes)
}

func TestLoadConfig_StorageLedgerDefaultQuotaExplicitZero(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\nSTORAGE_LEDGER_DEFAULT_QUOTA_BYTES: 0\n")
	t.Chdir(dir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Zero(t, cfg.StorageLedgerDefaultQuotaBytes, "0 disables the default quota (unlimited)")
}

func TestLoadConfig_SnowflakeNodeIDFromEnv(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	t.Chdir(dir)

	t.Setenv("SNOWFLAKE_NODE_ID", "3")
	t.Setenv("SNOWFLAKE_LEASE_TTL_SECONDS", "30")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, int64(3), cfg.SnowflakeNodeID)
	require.True(t, cfg.SnowflakeNodeIDSet)
	require.Equal(t, 30, cfg.SnowflakeLeaseTTLSecs)
}

func TestLoadConfig_SnowflakeNodeIDFromYAML(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\nSNOWFLAKE_NODE_ID: 0\n")
	t.Chdir(dir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, int64(0), cfg.SnowflakeNodeID)
	require.True(t, cfg.SnowflakeNodeIDSet)
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

func TestLoadConfig_ServerEnvWithoutConfigKeys(t *testing.T) {
	clearConfigEnv(t)

	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	t.Chdir(dir)

	t.Setenv("SERVER_ADDRESS", "127.0.0.1")
	t.Setenv("SERVER_PORT", "8002")
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "60")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", cfg.ServerAddress)
	require.Equal(t, 8002, cfg.ServerPort, "per-color systemd env files set the port without a config.yaml key")
	require.Equal(t, 60, cfg.ShutdownTimeoutSeconds)
}

func TestLoadConfig_MissingConfigFile(t *testing.T) {
	clearConfigEnv(t)

	t.Chdir(t.TempDir())

	cfg, err := LoadConfig()
	require.Error(t, err)
	require.Nil(t, cfg)
}

func TestValidateSecrets(t *testing.T) {
	strong := "0123456789abcdef0123456789abcdef"
	other := "fedcba9876543210fedcba9876543210"
	cases := []struct {
		name, jwt, chunk, want string
	}{
		{"valid", strong, other, ""},
		{"missing jwt", "", other, "JWT_KEY must be set"},
		{"sample jwt", "secret", other, "JWT_KEY still has the sample value"},
		{"short jwt", "short", other, "JWT_KEY must be at least 32"},
		{"missing chunk", strong, "", "CHUNK_URL_SIGNING_KEY must be set"},
		{"sample chunk", strong, "change-me", "CHUNK_URL_SIGNING_KEY still has the sample value"},
		{"short chunk", strong, "short", "CHUNK_URL_SIGNING_KEY must be at least 32"},
		{"identical", strong, strong, "must differ"},
		{"sample prefix", "Change-Me-To-A-Long-Random-Secret-1234", other, "JWT_KEY still has the sample value"},
		{"password", "PASSWORD", other, "JWT_KEY still has the sample value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Config{JWTKey: tc.jwt, ChunkURLSigningKey: tc.chunk}).ValidateSecrets()
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestValidateSecretsOrWarn(t *testing.T) {
	var warned error
	warn := func(_ string, err error) { warned = err }

	weak := &Config{JWTKey: "secret", ChunkURLSigningKey: "change-me"}
	require.Error(t, weak.ValidateSecretsOrWarn(warn))
	require.Nil(t, warned)

	weak.AllowInsecureDevSecrets = true
	require.NoError(t, weak.ValidateSecretsOrWarn(warn))
	require.ErrorContains(t, warned, "JWT_KEY")

	warned = nil
	strong := &Config{JWTKey: "0123456789abcdef0123456789abcdef", ChunkURLSigningKey: "fedcba9876543210fedcba9876543210", AllowInsecureDevSecrets: true}
	require.NoError(t, strong.ValidateSecretsOrWarn(warn))
	require.Nil(t, warned)
}

func TestAllowInsecureDevSecretsDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "SERVER_PORT: 1\n")
	t.Chdir(dir)
	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.False(t, cfg.AllowInsecureDevSecrets)
}

func TestSSHLoginAudience(t *testing.T) {
	cases := []struct {
		name, public, email, want string
		wantErr                   bool
	}{
		{name: "unset"},
		{name: "public base url", public: "https://Nipa.Example.com:443/", want: "nipa.example.com"},
		{name: "email fallback", email: "http://127.0.0.1:8080/app", want: "127.0.0.1:8080"},
		{name: "public wins", public: "https://a.example.com", email: "https://b.example.com", want: "a.example.com"},
		{name: "whitespace ignored", public: "  ", email: "https://b.example.com", want: "b.example.com"},
		{name: "invalid public", public: "ftp://nope", email: "https://b.example.com", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (&Config{PublicBaseURL: tc.public, EmailBaseURL: tc.email}).SSHLoginAudience()
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMFAKey(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 200)
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		key, err := (&Config{MFAEncryptionKey: " " + enc.EncodeToString(raw) + "\n"}).MFAKey()
		require.NoError(t, err)
		require.Equal(t, raw, key)
	}

	key, err := (&Config{}).MFAKey()
	require.NoError(t, err)
	require.Nil(t, key)

	_, err = (&Config{MFAEncryptionKey: base64.StdEncoding.EncodeToString(raw[:31])}).MFAKey()
	require.ErrorContains(t, err, "at least 32 bytes")
	_, err = (&Config{MFAEncryptionKey: "not base64 !!"}).MFAKey()
	require.ErrorContains(t, err, "must be base64")

	strong := "0123456789abcdef0123456789abcdef"
	other := "fedcba9876543210fedcba9876543210"
	require.NoError(t, (&Config{JWTKey: strong, ChunkURLSigningKey: other, MFAEncryptionKey: "short"}).ValidateSecrets(),
		"the MFA key is checked by ValidateMFAKey, only while MFA is on")
}

func TestValidateMFAKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(make([]byte, 32))
	for name, tc := range map[string]struct {
		cfg       Config
		defaultOn bool
		wantErr   bool
	}{
		"free default ignores a bad key":       {cfg: Config{MFAEncryptionKey: "short"}},
		"enterprise default checks the key":    {cfg: Config{MFAEncryptionKey: "short"}, defaultOn: true, wantErr: true},
		"explicitly off ignores a bad key":     {cfg: Config{MFAEncryptionKey: "short", MFAEnabledSet: true}, defaultOn: true},
		"explicitly on checks the key":         {cfg: Config{MFAEncryptionKey: "short", MFAEnabled: true, MFAEnabledSet: true}, wantErr: true},
		"on with a good key":                   {cfg: Config{MFAEncryptionKey: good}, defaultOn: true},
		"on without a key leaves MFA disabled": {cfg: Config{}, defaultOn: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := tc.cfg.ValidateMFAKey(tc.defaultOn)
			if tc.wantErr {
				require.ErrorContains(t, err, "MFA_ENCRYPTION_KEY")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestLoadConfig_MFAEnabled(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	t.Chdir(dir)

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.False(t, cfg.MFAEnabledSet)
	require.False(t, cfg.MFAEnabledOr(false))
	require.True(t, cfg.MFAEnabledOr(true))

	t.Setenv("MFA_ENABLED", "false")
	cfg, err = LoadConfig()
	require.NoError(t, err)
	require.True(t, cfg.MFAEnabledSet)
	require.False(t, cfg.MFAEnabledOr(true))

	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\nMFA_ENABLED: true\n")
	t.Setenv("MFA_ENABLED", "")
	_ = os.Unsetenv("MFA_ENABLED")
	cfg, err = LoadConfig()
	require.NoError(t, err)
	require.True(t, cfg.MFAEnabledOr(false))
}
