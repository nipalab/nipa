package obs

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseHeaders(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{"empty", "", nil},
		{"blank", "   ", nil},
		{"single", "Authorization=Basic dXNlcjpwYXNz", map[string]string{"Authorization": "Basic dXNlcjpwYXNz"}},
		{"multiple", "Authorization=Bearer abc,X-Scope=team", map[string]string{"Authorization": "Bearer abc", "X-Scope": "team"}},
		{"base64 padding survives", "Authorization=Basic dXNlcjpwYXNz==", map[string]string{"Authorization": "Basic dXNlcjpwYXNz=="}},
		{"equals only splits once", "X-Token=a=b", map[string]string{"X-Token": "a=b"}},
		{"spaces trimmed", " A = 1 , B = 2 ", map[string]string{"A": "1", "B": "2"}},
		{"missing separator skipped", "nope", nil},
		{"empty key skipped", "=value", nil},
		{"empty value skipped", "A=", nil},
		{"mixed valid and invalid", "A=1,broken,B=2,", map[string]string{"A": "1", "B": "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ParseHeaders(tt.in))
		})
	}
}

func TestResolveOTLPProtocol(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", otlpProtocolGRPC, false},
		{"grpc", otlpProtocolGRPC, false},
		{"GRPC", otlpProtocolGRPC, false},
		{" http ", otlpProtocolHTTP, false},
		{"http", otlpProtocolHTTP, false},
		{"https", "", true},
		{"udp", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := resolveOTLPProtocol(tt.in)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveOTLPEndpointHTTPDefault(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")

	target := resolveOTLPEndpoint("", defaultOTLPHTTPEndpoint)
	require.Equal(t, defaultOTLPHTTPEndpoint, target.endpoint)
	require.True(t, target.insecure)
	require.Len(t, target.traceHTTPOptions(nil), 2)
	require.Len(t, target.logHTTPOptions(nil), 2)
	require.Len(t, target.metricHTTPOptions(nil), 2)

	target = resolveOTLPEndpoint("https://openobserve.example.com/api/default", defaultOTLPHTTPEndpoint)
	require.False(t, target.insecure, "an explicit URL scheme decides TLS")
	require.Len(t, target.traceHTTPOptions(nil), 1)
	require.Len(t, target.logHTTPOptions(nil), 1)
	require.Len(t, target.metricHTTPOptions(nil), 1)

	headers := ParseHeaders("Authorization=Basic dXNlcjpwYXNz")
	require.Len(t, target.traceHTTPOptions(headers), 2, "headers must be appended to the TLS options")
	require.Len(t, target.logHTTPOptions(headers), 2)
	require.Len(t, target.metricHTTPOptions(headers), 2)
}

func TestSetup_UnknownProtocol(t *testing.T) {
	_, err := SetupTracing(context.Background(), TracingConfig{Enabled: true, Protocol: "udp"})
	require.Error(t, err)

	_, err = SetupLogs(context.Background(), LogsConfig{Enabled: true, Protocol: "udp"})
	require.Error(t, err)
}

func TestHTTPSignalURL(t *testing.T) {
	tests := []struct {
		endpoint string
		signal   string
		want     string
	}{
		{"https://openobserve.example.com/api/default", "logs", "https://openobserve.example.com/api/default/v1/logs"},
		{"https://openobserve.example.com/api/default/", "traces", "https://openobserve.example.com/api/default/v1/traces"},
		{"https://openobserve.example.com/api/default", "metrics", "https://openobserve.example.com/api/default/v1/metrics"},
		{"https://openobserve.example.com", "logs", "https://openobserve.example.com/v1/logs"},
		{"http://127.0.0.1:5080/api/default/v1/logs", "logs", "http://127.0.0.1:5080/api/default/v1/logs"},
		{"https://collector.example.com:4318/v1/traces", "traces", "https://collector.example.com:4318/v1/traces"},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			require.Equal(t, tt.want, httpSignalURL(tt.endpoint, tt.signal))
		})
	}
}
