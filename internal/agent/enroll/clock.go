package enroll

import "time"

// Clock is the Agent's source of time: the instant that certificate validity
// is judged against, and the timers the renewal loop sleeps on. Production
// code uses the system clock. A test supplies its own, so that it does not
// depend on how long it really takes or on the host's wall clock stepping.
type Clock interface {
	// Now is the instant that certificate NotBefore and NotAfter are compared with.
	Now() time.Time
	// NewTimer returns a timer that fires once after d.
	NewTimer(d time.Duration) Timer
}

// Timer is the part of time.Timer that the renewal loop uses.
type Timer interface {
	// C receives the time when the timer fires.
	C() <-chan time.Time
	// Stop prevents the timer from firing.
	Stop() bool
}

// SystemClock is the Clock that reads the host's clocks.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) NewTimer(d time.Duration) Timer { return systemTimer{time.NewTimer(d)} }

type systemTimer struct{ t *time.Timer }

func (s systemTimer) C() <-chan time.Time { return s.t.C }

func (s systemTimer) Stop() bool { return s.t.Stop() }

// Option adjusts Renew and RenewLoop.
type Option func(*options)

type options struct{ clock Clock }

// WithClock makes Renew and RenewLoop use clock instead of the system clock.
func WithClock(clock Clock) Option {
	return func(o *options) {
		if clock != nil {
			o.clock = clock
		}
	}
}

func applyOptions(opts []Option) options {
	o := options{clock: SystemClock{}}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}
