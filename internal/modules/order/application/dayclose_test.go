package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"tsb-service/internal/modules/order/domain"
	"tsb-service/pkg/timezone"
)

func brussels(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02 15:04:05", value, timezone.Location)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestNextDayClose(t *testing.T) {
	for _, tc := range []struct{ now, want string }{
		{"2026-11-04 12:00:00", "2026-11-04 23:58:00"},
		{"2026-11-04 23:57:59", "2026-11-04 23:58:00"},
		{"2026-11-04 23:58:00", "2026-11-05 23:58:00"}, // the run that just fired schedules tomorrow
		{"2026-11-04 23:59:30", "2026-11-05 23:58:00"},
		{"2026-11-05 00:00:00", "2026-11-05 23:58:00"},
		{"2026-03-29 01:00:00", "2026-03-29 23:58:00"}, // spring forward day
		{"2026-10-25 01:00:00", "2026-10-25 23:58:00"}, // fall back day
		{"2026-12-31 23:59:00", "2027-01-01 23:58:00"},
	} {
		got := NextDayClose(brussels(t, tc.now))
		if want := brussels(t, tc.want); !got.Equal(want) {
			t.Errorf("NextDayClose(%s) = %s, want %s", tc.now, got.In(timezone.Location), tc.want)
		}
	}

	// The input zone does not matter: 22:58 UTC is 23:58 in Brussels in winter.
	if got := NextDayClose(time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)); !got.Equal(time.Date(2026, 11, 4, 22, 58, 0, 0, time.UTC)) {
		t.Errorf("UTC input: got %s", got.UTC())
	}
}

func TestRunDayClose(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	now := brussels(t, "2026-11-04 23:00:00")
	var waits []time.Duration
	runs := 0
	fire := make(chan time.Time, 1)
	after := func(d time.Duration) <-chan time.Time {
		waits = append(waits, d)
		if len(waits) > 2 {
			cancel()
			return nil
		}
		fire <- now
		return fire
	}
	clock := func() time.Time { return now }
	RunDayClose(ctx, clock, after, func(context.Context) {
		runs++
		now = brussels(t, "2026-11-04 23:58:01")
	})

	if runs != 2 {
		t.Fatalf("runs = %d, want 2", runs)
	}
	if waits[0] != 58*time.Minute {
		t.Errorf("first wait = %s, want 58m", waits[0])
	}
	if want := 24*time.Hour - time.Second; waits[1] != want {
		t.Errorf("second wait = %s, want %s", waits[1], want)
	}
}

func TestCloseDay(t *testing.T) {
	a := &domain.Order{ID: uuid.New(), OrderStatus: domain.OrderStatusDelivered}
	b := &domain.Order{ID: uuid.New(), OrderStatus: domain.OrderStatusPickedUp}
	pending := []uuid.UUID{uuid.New()}

	t.Run("records history for each completed order and lists pending ones", func(t *testing.T) {
		repo := newHistRepo(domain.OrderStatusPending, nil)
		repo.completed = []*domain.Order{a, b}
		repo.unhandledPending = pending
		res, err := NewOrderService(repo, nil).CloseDay(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Completed) != 2 || len(res.UnhandledPending) != 1 || res.UnhandledPending[0] != pending[0] {
			t.Fatalf("res = %+v", res)
		}
		if len(repo.history) != 2 || repo.history[0] != domain.OrderStatusDelivered || repo.history[1] != domain.OrderStatusPickedUp {
			t.Fatalf("history = %v", repo.history)
		}
	})

	t.Run("a failed update is returned", func(t *testing.T) {
		repo := newHistRepo(domain.OrderStatusPending, nil)
		repo.completeErr = errors.New("db down")
		if _, err := NewOrderService(repo, nil).CloseDay(t.Context()); err == nil {
			t.Fatal("want error")
		}
	})

	t.Run("history and pending lookup failures do not undo the close", func(t *testing.T) {
		repo := newHistRepo(domain.OrderStatusPending, nil)
		repo.completed = []*domain.Order{a}
		repo.historyErr = errors.New("history down")
		repo.pendingErr = errors.New("lookup down")
		res, err := NewOrderService(repo, nil).CloseDay(t.Context())
		if err != nil || len(res.Completed) != 1 || res.UnhandledPending != nil {
			t.Fatalf("res=%+v err=%v", res, err)
		}
	})
}
