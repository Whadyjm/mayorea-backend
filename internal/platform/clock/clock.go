// Package clock provides the business "today" (Venezuela time).
package clock

import "time"

type System struct{ loc *time.Location }

// NewSystem uses the given IANA zone; if tzdata is unavailable it falls back
// to the fixed UTC-4 offset Venezuela uses (no DST).
func NewSystem(zone string) System {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.FixedZone("VET", -4*3600)
	}
	return System{loc: loc}
}

// Today returns the current local calendar day as UTC midnight.
func (c System) Today() time.Time {
	y, m, d := time.Now().In(c.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func (c System) Now() time.Time { return time.Now() }

// Fixed is a Clock for tests.
type Fixed struct{ Day time.Time }

func (c Fixed) Today() time.Time { return c.Day }
func (c Fixed) Now() time.Time   { return c.Day }
