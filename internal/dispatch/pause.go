package dispatch

import "time"

// PauseCheckInterval is how often a paused loop checks whether it may resume.
const PauseCheckInterval = 500 * time.Millisecond

// Pause reports whether background writers must hold off, e.g. while the
// database is frozen for a move. A nil Pause never pauses.
type Pause func() bool

func (p Pause) Active() bool {
	return p != nil && p()
}

// Wait blocks while p is active, checking every interval. It returns false
// when stop closes first.
func (p Pause) Wait(stop <-chan struct{}, interval time.Duration) bool {
	if interval <= 0 {
		interval = PauseCheckInterval
	}
	for p.Active() {
		timer := time.NewTimer(interval)
		select {
		case <-stop:
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
	return true
}
