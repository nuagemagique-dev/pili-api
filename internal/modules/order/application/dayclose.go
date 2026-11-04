package application

import (
	"context"
	"time"

	"tsb-service/pkg/timezone"
)

// The nightly close runs every day at 23:58, restaurant time (Europe/Brussels),
// well after the kitchen closes and before the day ends, so every order belongs
// to the day it was placed on.
const (
	DayCloseHour   = 23
	DayCloseMinute = 58
)

// NextDayClose returns the first day close strictly after now, in the
// restaurant's timezone. time.Date keeps 23:58 on the wall clock across DST
// changes.
func NextDayClose(now time.Time) time.Time {
	local := now.In(timezone.Location)
	next := time.Date(local.Year(), local.Month(), local.Day(), DayCloseHour, DayCloseMinute, 0, 0, timezone.Location)
	if !next.After(local) {
		next = time.Date(local.Year(), local.Month(), local.Day()+1, DayCloseHour, DayCloseMinute, 0, 0, timezone.Location)
	}
	return next
}

// RunDayClose calls run at every day close until ctx is done. now and after are
// the clock (time.Now and time.After in production). Every replica runs it: the
// close itself only updates orders that are still open, so a second run finds
// nothing left to do.
func RunDayClose(ctx context.Context, now func() time.Time, after func(time.Duration) <-chan time.Time, run func(context.Context)) {
	for {
		t := now()
		select {
		case <-ctx.Done():
			return
		case <-after(NextDayClose(t).Sub(t)):
			run(ctx)
		}
	}
}
