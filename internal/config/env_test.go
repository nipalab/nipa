package config

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func loadWithMinimalYAML(t *testing.T) *Config {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\n")
	t.Chdir(dir)
	cfg, err := LoadConfig()
	require.NoError(t, err)
	return cfg
}

func TestLoadConfig_EmailSenderFromEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("EMAIL_SENDER", "log")
	t.Setenv("EMAIL_FROM", "Nipa <noreply@example.com>")

	cfg := loadWithMinimalYAML(t)
	require.Equal(t, "log", cfg.EmailSender)
	require.Equal(t, "Nipa <noreply@example.com>", cfg.EmailFrom)
}

func TestLoadConfig_TypedFieldsFromEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("EMAIL_SMTP_PORT", "2525")
	t.Setenv("EMAIL_SMTP_INSECURE_SKIP_VERIFY", "true")
	t.Setenv("RATE_LIMIT_TRUSTED_PROXIES", "10.0.0.1, 10.0.0.2")
	t.Setenv("RATE_LIMIT_ENABLED", "false")
	t.Setenv("TRACING_SAMPLE_RATIO", "0.25")

	cfg := loadWithMinimalYAML(t)
	require.Equal(t, 2525, cfg.EmailSMTPPort)
	require.True(t, cfg.EmailSMTPInsecureTLS)
	require.Equal(t, "10.0.0.1, 10.0.0.2", cfg.RateLimitTrustedProxies)
	prefixes, err := cfg.TrustedProxyPrefixes()
	require.NoError(t, err)
	require.Len(t, prefixes, 2)
	require.False(t, cfg.RateLimitEnabled)
	require.InDelta(t, 0.25, cfg.TracingSampleRatio, 1e-9)
}

func TestLoadConfig_EnvOverridesYAMLForUnboundKey(t *testing.T) {
	clearConfigEnv(t)
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "DATABASE_DSN: nipa.db\nEMAIL_SENDER: smtp\nMAINTENANCE_FILE: /from/yaml\n")
	t.Chdir(dir)
	t.Setenv("EMAIL_SENDER", "log")

	cfg, err := LoadConfig()
	require.NoError(t, err)
	require.Equal(t, "log", cfg.EmailSender)
	require.Equal(t, "/from/yaml", cfg.MaintenanceFile)
}

func TestLoadConfig_EveryFieldFromEnv(t *testing.T) {
	clearConfigEnv(t)
	want := map[string]reflect.Value{}
	typ := reflect.TypeOf(Config{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		key := f.Tag.Get("mapstructure")
		if key == "" || key == "-" {
			continue
		}
		var raw string
		var val reflect.Value
		switch f.Type.Kind() {
		case reflect.String:
			raw = "env-" + key
			val = reflect.ValueOf(raw)
		case reflect.Int, reflect.Int64:
			n := int64(4000 + i)
			raw = strconv.FormatInt(n, 10)
			val = reflect.ValueOf(n).Convert(f.Type)
		case reflect.Bool:
			raw = "true"
			val = reflect.ValueOf(true)
		case reflect.Float64:
			x := 0.5 + float64(i)
			raw = strconv.FormatFloat(x, 'f', -1, 64)
			val = reflect.ValueOf(x)
		default:
			t.Fatalf("field %s has unsupported kind %s", f.Name, f.Type.Kind())
		}
		t.Setenv(key, raw)
		want[f.Name] = val
	}
	require.NotEmpty(t, want)

	cfg := loadWithMinimalYAML(t)
	got := reflect.ValueOf(*cfg)
	for name, val := range want {
		require.Equal(t, val.Interface(), got.FieldByName(name).Interface(), "field %s is not settable from the environment", name)
	}
	require.True(t, cfg.SnowflakeNodeIDSet)
	require.True(t, cfg.MFAEnabledSet)
}

func TestEnvKeys(t *testing.T) {
	keys := envKeys(reflect.TypeOf(struct {
		A string `mapstructure:"A"`
		B string `mapstructure:"-"`
		C string
		D struct {
			E int `mapstructure:"E"`
		} `mapstructure:",squash"`
		F struct {
			G int `mapstructure:"G"`
		} `mapstructure:"F"`
	}{}))
	require.Equal(t, []string{"A", "E", "F.G"}, keys)
}

func TestEnvKeyReplacerMapsNestedKeys(t *testing.T) {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	require.NoError(t, v.BindEnv("F.G"))
	t.Setenv("F_G", "7")
	require.Equal(t, 7, v.GetInt("F.G"))
}

func TestLoadConfig_RegistrationEnabled(t *testing.T) {
	clearConfigEnv(t)
	require.True(t, loadWithMinimalYAML(t).RegistrationEnabled)

	t.Setenv("REGISTRATION_ENABLED", "false")
	require.False(t, loadWithMinimalYAML(t).RegistrationEnabled)
}
