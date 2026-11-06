package grpc

import (
	"context"
	"errors"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	serverDomain "github.com/nipalab/nipa/internal/domain"
)

// retryPolicy bounds and paces repeated attempts of one transfer. Transient
// failures and throttling have separate budgets: a rate-limited server asks
// the client to wait, which is expected during a large push or clone.
type retryPolicy struct {
	attempts         int
	backoff          time.Duration
	throttleAttempts int
	maxThrottleWait  time.Duration
}

var defaultRetryPolicy = retryPolicy{
	attempts:         3,
	backoff:          250 * time.Millisecond,
	throttleAttempts: 10,
	maxThrottleWait:  30 * time.Second,
}

var errThrottled = errors.New("throttled by the server")

// delay is the pause after the given failed attempt: the backoff doubled per
// attempt plus up to a quarter of jitter so parallel workers do not retry in step.
func (p retryPolicy) delay(attempt int) time.Duration {
	d := p.backoff << attempt
	return d + rand.N(d/4+1)
}

// throttleDelay is the pause after a throttled attempt: the server's hint when
// it gave one, the doubling backoff otherwise, capped and spread by up to half
// again so a pool of workers does not come back all at once.
func (p retryPolicy) throttleDelay(attempt int, hint time.Duration) time.Duration {
	d := hint
	if d <= 0 {
		d = p.backoff << min(attempt, 16)
	}
	if p.maxThrottleWait > 0 && d > p.maxThrottleWait {
		d = p.maxThrottleWait
	}
	return d + rand.N(d/2+1)
}

// retryHint classifies one failed attempt.
type retryHint struct {
	retryable bool
	throttled bool
	wait      time.Duration
}

func httpRetryHint(res *http.Response) retryHint {
	if res.StatusCode == http.StatusTooManyRequests {
		return retryHint{retryable: true, throttled: true, wait: parseRetryAfter(res.Header.Get("Retry-After"), time.Now())}
	}
	return retryHint{retryable: retryableStatus(res.StatusCode)}
}

// retryTransfer runs try until it succeeds, fails for good or runs out of
// attempts. Every throttled attempt also lowers the transfer concurrency.
func (c *Client) retryTransfer(ctx context.Context, try func() (retryHint, error)) error {
	failures, throttles := 0, 0
	for {
		hint, err := try()
		if err == nil || !hint.retryable {
			return err
		}
		var wait time.Duration
		if hint.throttled {
			if throttles >= c.retry.throttleAttempts {
				return err
			}
			c.recordTransfer(0, errThrottled)
			wait = c.retry.throttleDelay(throttles, hint.wait)
			throttles++
		} else {
			failures++
			if failures >= c.retry.attempts {
				return err
			}
			wait = c.retry.delay(failures - 1)
		}
		if err := sleepContext(ctx, wait); err != nil {
			return err
		}
	}
}

func retryableStatus(status int) bool {
	return status >= http.StatusInternalServerError ||
		status == http.StatusTooManyRequests ||
		status == http.StatusRequestTimeout
}

// parseRetryAfter reads a Retry-After header in seconds or as an HTTP date.
func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

var retryInPattern = regexp.MustCompile(`retry in (\d+)s`)

// rateLimitWait reports whether err is the server's rate limit rejection and
// how long it asked to wait. The rejection happens before the handler runs, so
// the call is always safe to repeat.
func rateLimitWait(err error) (time.Duration, bool) {
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.ResourceExhausted || !strings.HasPrefix(st.Message(), serverDomain.RateLimitMessage) {
		return 0, false
	}
	if m := retryInPattern.FindStringSubmatch(st.Message()); m != nil {
		if secs, err := strconv.Atoi(m[1]); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second, true
		}
	}
	return 0, true
}

// invokeWithThrottleRetry repeats call while the server rejects it for rate
// limiting, waiting as long as it asks to.
func (c *Client) invokeWithThrottleRetry(ctx context.Context, call func() error) error {
	for throttles := 0; ; throttles++ {
		err := call()
		wait, limited := rateLimitWait(err)
		if !limited || throttles >= c.retry.throttleAttempts {
			return err
		}
		if err := sleepContext(ctx, c.retry.throttleDelay(throttles, wait)); err != nil {
			return err
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
