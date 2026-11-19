package byostorage

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeEndpoint(t *testing.T) {
	ok := map[string]string{
		"https://S3.Example.com":                  "https://s3.example.com",
		"s3.example.com":                          "https://s3.example.com",
		"https://s3.example.com/":                 "https://s3.example.com",
		"https://s3.example.com:8443":             "https://s3.example.com:8443",
		"https://8.8.8.8":                         "https://8.8.8.8",
		"https://[2606:4700:4700::1111]:443":      "https://[2606:4700:4700::1111]:443",
		" https://acct.r2.cloudflarestorage.com ": "https://acct.r2.cloudflarestorage.com",
	}
	for in, want := range ok {
		got, err := NormalizeEndpoint(in, false)
		require.NoError(t, err, in)
		require.Equal(t, want, got)
	}
	refused := []string{
		"",
		"http://s3.example.com",
		"ftp://s3.example.com",
		"https://user:pass@s3.example.com",
		"https://s3.example.com/bucket",
		"https://s3.example.com?x=1",
		"https://s3.example.com#frag",
		"https://127.0.0.1",
		"https://10.0.0.5",
		"https://169.254.169.254",
		"https://[::1]",
		"https://localhost",
		"https://minio",
		"https://metadata.google.internal",
		"https://",
		"https://%zz",
	}
	for _, in := range refused {
		_, err := NormalizeEndpoint(in, false)
		require.Equal(t, 400, errorCode(err), in)
	}

	got, err := NormalizeEndpoint("http://localhost:9000", true)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:9000", got)
	got, err = NormalizeEndpoint("http://127.0.0.1:9000", true)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:9000", got)
}

func TestCheckAddress(t *testing.T) {
	for _, addr := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		require.NoError(t, CheckAddress(netip.MustParseAddr(addr)), addr)
	}
	for _, addr := range []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1",
		"0.0.0.0", "224.0.0.1", "255.255.255.255", "198.18.0.1", "192.0.0.10", "::1", "::",
		"fc00::1", "fe80::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::a00:1", "2001:db8::1",
		"fd00:ec2::254",
	} {
		require.ErrorIs(t, CheckAddress(netip.MustParseAddr(addr)), ErrAddressNotAllowed, addr)
	}
	require.ErrorIs(t, CheckAddress(netip.Addr{}), ErrAddressNotAllowed)
}

func TestDialControl(t *testing.T) {
	strict := dialControl(false)
	require.ErrorIs(t, strict("tcp", "127.0.0.1:80", nil), ErrAddressNotAllowed)
	require.ErrorIs(t, strict("tcp", "[::1]:80", nil), ErrAddressNotAllowed)
	require.NoError(t, strict("tcp", "8.8.8.8:443", nil))
	require.ErrorIs(t, strict("tcp", "not-an-ip:80", nil), ErrAddressNotAllowed)
	require.Error(t, strict("tcp", "missing-port", nil))
	require.NoError(t, dialControl(true)("tcp", "127.0.0.1:80", nil))
}

func TestTransportRefusesLoopbackAtConnectTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := &http.Client{Transport: NewTransport(false)}
	_, err := client.Get(server.URL)
	require.Error(t, err)
	var opErr *net.OpError
	require.True(t, errors.As(err, &opErr))
	require.ErrorIs(t, err, ErrAddressNotAllowed)

	allowed := &http.Client{Transport: NewTransport(true)}
	resp, err := allowed.Get(server.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Nil(t, NewTransport(false).Proxy)
}

func TestBucketAndPrefix(t *testing.T) {
	require.NoError(t, validateBucket("my.assets-1"))
	for _, bad := range []string{"", "ab", "UPPER", "a..b", "-start", "end-", "under_score"} {
		require.Error(t, validateBucket(bad), bad)
	}
	for in, want := range map[string]string{"": "", "/a/b/": "a/b", " nipa ": "nipa", "a.b_c-d/e": "a.b_c-d/e"} {
		got, err := normalizePrefix(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got)
	}
	for _, bad := range []string{"a//b", "a/./b", "../a", "a b", "a?b", string(make([]byte, 201))} {
		_, err := normalizePrefix(bad)
		require.Error(t, err, bad)
	}
}
