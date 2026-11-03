package clock

import "time"

// Clock abstracts wall time so domain logic can be tested without sleeping.
type Clock interface {
	Now() time.Time
}

// System is the real wall clock.
type System struct{}

// Now returns the current local time.
func (System) Now() time.Time {
	return time.Now()
}

// Fixed returns a constant time — useful in tests.
type Fixed struct {
	T time.Time
}

// Now returns the fixed instant.
func (f Fixed) Now() time.Time {
	return f.T
}
