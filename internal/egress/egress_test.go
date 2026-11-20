package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func staticResolver(ips ...string) func(context.Context, string) ([]net.IPAddr, error) {
	return func(context.Context, string) ([]net.IPAddr, error) {
		addrs := make([]net.IPAddr, 0, len(ips))
		for _, ip := range ips {
			addrs = append(addrs, net.IPAddr{IP: net.ParseIP(ip)})
		}
		return addrs, nil
	}
}

func httpClient(guard *Guard) *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: guard.Transport(), CheckRedirect: NoRedirects}
}

func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32, string) {
	t.Helper()
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	return server, &hits, port
}

func get(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func TestGuard_LoopbackNeedsAllowlist(t *testing.T) {
	server, hits, _ := countingServer(t)

	err := get(httpClient(New(Config{})), server.URL)
	require.True(t, IsBlocked(err), "an empty allowlist must not reach loopback")
	require.ErrorContains(t, err, "destination host")
	require.Zero(t, hits.Load())

	for _, entry := range []string{"127.0.0.1", "127.0.0.0/8"} {
		require.NoError(t, get(httpClient(New(Config{Allowlist: []string{entry}})), server.URL), entry)
	}
	require.Equal(t, int32(2), hits.Load())
}

func TestGuard_ResolvesBeforeDial(t *testing.T) {
	_, hits, port := countingServer(t)
	url := "http://internal.example:" + port + "/hook"
	lookup := staticResolver("127.0.0.1")

	err := get(httpClient(New(Config{Resolver: lookup, Label: "webhook"})), url)
	require.True(t, IsBlocked(err), "a hostname must not hide a loopback destination")
	require.ErrorContains(t, err, `webhook host "internal.example" resolves to 127.0.0.1`)
	require.Zero(t, hits.Load())

	require.NoError(t, get(httpClient(New(Config{Resolver: lookup, Allowlist: []string{"internal.example"}})), url))
	require.NoError(t, get(httpClient(New(Config{Resolver: lookup, Allowlist: []string{"127.0.0.0/8"}})), url),
		"the resolved address may fall inside a listed range")
	require.Equal(t, int32(2), hits.Load())
}

func TestGuard_DialEdges(t *testing.T) {
	ctx := context.Background()

	t.Run("lookup failure is not a block", func(t *testing.T) {
		guard := New(Config{Resolver: func(context.Context, string) ([]net.IPAddr, error) {
			return nil, errors.New("dns down")
		}})
		_, err := guard.DialContext(ctx, "tcp", "internal.example:80")
		require.ErrorContains(t, err, "dns down")
		require.False(t, IsBlocked(err))
	})

	t.Run("no resolved addresses is blocked", func(t *testing.T) {
		guard := New(Config{Allowlist: []string{"internal.example"}, Resolver: staticResolver()})
		_, err := guard.DialContext(ctx, "tcp", "internal.example:80")
		require.True(t, IsBlocked(err))
		require.ErrorContains(t, err, "no addresses")
	})

	t.Run("bad address", func(t *testing.T) {
		_, err := New(Config{}).DialContext(ctx, "tcp", "no-port")
		require.Error(t, err)
		require.False(t, IsBlocked(err))
	})

	t.Run("a denied address falls through to an allowed one", func(t *testing.T) {
		_, hits, port := countingServer(t)
		guard := New(Config{Allowlist: []string{"127.0.0.0/8"}, Resolver: staticResolver("10.0.0.1", "127.0.0.1")})
		require.NoError(t, get(httpClient(guard), "http://multi.example:"+port+"/hook"))
		require.Equal(t, int32(1), hits.Load())
	})

	t.Run("dial failure is not a block", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := listener.Addr().(*net.TCPAddr).Port
		require.NoError(t, listener.Close())

		guard := New(Config{DialTimeout: time.Second, Allowlist: []string{"127.0.0.0/8"}, Resolver: staticResolver("127.0.0.1")})
		_, err = guard.DialContext(ctx, "tcp", "refused.example:"+strconv.Itoa(port))
		require.Error(t, err)
		require.False(t, IsBlocked(err))
	})

	t.Run("dial failure wins over a later blocked address", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := listener.Addr().(*net.TCPAddr).Port
		require.NoError(t, listener.Close())

		guard := New(Config{DialTimeout: time.Second, Allowlist: []string{"127.0.0.0/8"}, Resolver: staticResolver("127.0.0.1", "10.0.0.1")})
		_, err = guard.DialContext(ctx, "tcp", "refused.example:"+strconv.Itoa(port))
		require.Error(t, err)
		require.False(t, IsBlocked(err))
	})

	t.Run("only blocked addresses is blocked", func(t *testing.T) {
		guard := New(Config{Resolver: staticResolver("10.0.0.1")})
		_, err := guard.DialContext(ctx, "tcp", "internal.example:80")
		require.True(t, IsBlocked(err))
		require.ErrorContains(t, err, "resolves to 10.0.0.1")
	})
}

func TestGuard_NoRedirects(t *testing.T) {
	target, hits, _ := countingServer(t)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	resp, err := httpClient(New(Config{Allowlist: []string{"127.0.0.1"}})).Get(redirector.URL)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Zero(t, hits.Load())
}

func TestGuard_TransportHasNoProxy(t *testing.T) {
	require.Nil(t, New(Config{}).Transport().Proxy)
}

func TestGuard_CheckURL(t *testing.T) {
	require.NoError(t, New(Config{}).CheckURL("http://anything.example"))

	hostOnly := New(Config{Allowlist: []string{"example.com"}, Label: "tracker"})
	require.NoError(t, hostOnly.CheckURL("https://EXAMPLE.com./x"))
	err := hostOnly.CheckURL("http://127.0.0.1:8080")
	require.True(t, IsBlocked(err))
	require.ErrorContains(t, err, `tracker host "127.0.0.1:8080" is not in the egress allowlist`)
	err = hostOnly.CheckURL("http://%zz")
	require.Error(t, err)
	require.False(t, IsBlocked(err))

	ranges := New(Config{Allowlist: []string{"10.0.0.0/8"}})
	require.NoError(t, ranges.CheckURL("http://10.1.2.3"))
	require.NoError(t, ranges.CheckURL("http://jira.internal"), "a hostname may resolve into a listed range")
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip     string
		public bool
	}{
		{"8.8.8.8", true},
		{"2606:4700::1111", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.1", false},
		{"172.16.0.1", false},
		{"192.168.1.1", false},
		{"169.254.169.254", false},
		{"fd00::1", false},
		{"fe80::1", false},
		{"0.0.0.0", false},
		{"224.0.0.1", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.public, IsPublicIP(net.ParseIP(tt.ip)), tt.ip)
	}
}

func TestPolicyAllowsDial(t *testing.T) {
	empty := ParseAllowlist(nil)
	require.True(t, empty.Empty())
	require.True(t, empty.AllowsDial("example.com", net.ParseIP("8.8.8.8")))
	require.False(t, empty.AllowsDial("localhost", net.ParseIP("127.0.0.1")))

	require.True(t, ParseAllowlist([]string{"", "  "}).Empty())

	hostOnly := ParseAllowlist([]string{" Internal.Example. "})
	require.True(t, hostOnly.AllowsDial("internal.example", net.ParseIP("127.0.0.1")))
	require.False(t, hostOnly.AllowsDial("other.example", net.ParseIP("8.8.8.8")))

	listed := ParseAllowlist([]string{"10.0.0.0/8", "192.168.1.5", "fd00::1"})
	require.True(t, listed.AllowsDial("jenkins.internal", net.ParseIP("10.1.2.3")))
	require.True(t, listed.AllowsDial("jenkins.internal", net.ParseIP("192.168.1.5")))
	require.False(t, listed.AllowsDial("jenkins.internal", net.ParseIP("192.168.1.6")))
	require.True(t, listed.AllowsDial("dc.internal", net.ParseIP("fd00::1")))
}
