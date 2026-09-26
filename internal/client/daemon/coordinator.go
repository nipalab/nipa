package daemon

import (
	"context"
	"sync"
)

// coordinator serializes working-copy operations per root. Status, stage and
// diff run under shared admission; the long mutations (update, push, switch,
// merge, revert) take the exclusive slot. Waiters are granted in FIFO order,
// and a queued exclusive blocks later shared acquisitions (writer preference)
// so a stream of status polls cannot starve a push.
type coordinator struct {
	mu        sync.Mutex
	exclusive bool
	shared    int
	waiters   []*ticket
}

type ticket struct {
	ctx       context.Context
	exclusive bool
	ready     chan error
}

func newCoordinator() *coordinator {
	return &coordinator{}
}

// Acquire admits the caller and returns a release that must be called exactly
// once. ahead reports how many exclusive operations were already active or
// queued when this caller arrived.
func (c *coordinator) Acquire(ctx context.Context, exclusive bool) (release func(), ahead int, err error) {
	t := &ticket{ctx: ctx, exclusive: exclusive, ready: make(chan error, 1)}

	c.mu.Lock()
	ahead = c.exclusiveAheadLocked()
	c.waiters = append(c.waiters, t)
	c.dispatchLocked()
	c.mu.Unlock()

	stop := context.AfterFunc(ctx, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, w := range c.waiters {
			if w != t {
				continue
			}
			c.waiters = append(c.waiters[:i], c.waiters[i+1:]...)
			t.ready <- ctx.Err()
			break
		}
		c.dispatchLocked()
	})

	waitErr := <-t.ready
	stop()
	if waitErr != nil {
		return nil, ahead, waitErr
	}
	if err := ctx.Err(); err != nil {
		c.mu.Lock()
		c.releaseLocked(exclusive)
		c.mu.Unlock()
		return nil, ahead, err
	}
	return func() {
		c.mu.Lock()
		c.releaseLocked(exclusive)
		c.mu.Unlock()
	}, ahead, nil
}

func (c *coordinator) releaseLocked(exclusive bool) {
	if exclusive {
		c.exclusive = false
	} else {
		c.shared--
	}
	c.dispatchLocked()
}

func (c *coordinator) queuedLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

func (c *coordinator) exclusiveAheadLocked() int {
	ahead := 0
	if c.exclusive {
		ahead++
	}
	for _, t := range c.waiters {
		if t.exclusive {
			ahead++
		}
	}
	return ahead
}

// dispatchLocked grants waiters from the front of the queue. The head is the
// only ticket that can block: shared waiters behind it are held back on
// purpose, which is what keeps exclusive operations from starving.
func (c *coordinator) dispatchLocked() {
	for len(c.waiters) > 0 {
		t := c.waiters[0]
		if err := t.ctx.Err(); err != nil {
			c.waiters = c.waiters[1:]
			t.ready <- err
			continue
		}
		if t.exclusive {
			if c.exclusive || c.shared > 0 {
				return
			}
			c.waiters = c.waiters[1:]
			c.exclusive = true
			t.ready <- nil
			return
		}
		if c.exclusive {
			return
		}
		c.waiters = c.waiters[1:]
		c.shared++
		t.ready <- nil
	}
}
