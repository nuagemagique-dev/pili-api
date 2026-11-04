package repository

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tsb-service/internal/modules/order/domain"
)

func TestDayCloseQueries(t *testing.T) {
	e := newEnv(t)
	delivery := func(o *domain.Order) { o.OrderType = domain.OrderTypeDelivery }

	preparingPickup := e.save(t, e.newOrder(domain.OrderStatusPreparing, nil))
	preparingDelivery := e.save(t, e.newOrder(domain.OrderStatusPreparing, delivery))
	awaiting := e.save(t, e.newOrder(domain.OrderStatusAwaitingUp, nil))
	onTheRoad := e.save(t, e.newOrder(domain.OrderStatusOutForDelivery, delivery))
	testOrder := e.save(t, e.newOrder(domain.OrderStatusPreparing, func(o *domain.Order) { o.IsTest = true }))
	cancelled := e.save(t, e.newOrder(domain.OrderStatusCanceled, nil))
	cashPending := e.save(t, e.newOrder(domain.OrderStatusPending, nil))
	paidPending := e.save(t, e.newOrder(domain.OrderStatusPending, func(o *domain.Order) { o.IsOnlinePayment = true }))
	e.pay(t, paidPending.ID, "paid")
	unpaidPending := e.save(t, e.newOrder(domain.OrderStatusPending, func(o *domain.Order) { o.IsOnlinePayment = true }))
	e.pay(t, unpaidPending.ID, "open")
	e.save(t, e.newOrder(domain.OrderStatusPending, func(o *domain.Order) { o.IsTest = true })) // never listed

	t.Run("CompleteInProgressOrders completes accepted orders by type", func(t *testing.T) {
		got, err := e.repo.CompleteInProgressOrders(e.ctx)
		require.NoError(t, err)
		byID := map[uuid.UUID]domain.OrderStatus{}
		for _, o := range got {
			byID[o.ID] = o.OrderStatus
		}
		assert.Equal(t, map[uuid.UUID]domain.OrderStatus{
			preparingPickup.ID:   domain.OrderStatusPickedUp,
			preparingDelivery.ID: domain.OrderStatusDelivered,
			awaiting.ID:          domain.OrderStatusPickedUp,
			onTheRoad.ID:         domain.OrderStatusDelivered,
		}, byID)

		for id, want := range map[uuid.UUID]domain.OrderStatus{
			preparingPickup.ID: domain.OrderStatusPickedUp,
			onTheRoad.ID:       domain.OrderStatusDelivered,
			testOrder.ID:       domain.OrderStatusPreparing,
			cancelled.ID:       domain.OrderStatusCanceled,
			cashPending.ID:     domain.OrderStatusPending,
			paidPending.ID:     domain.OrderStatusPending,
		} {
			o, _, err := e.repo.FindByID(e.ctx, id)
			require.NoError(t, err)
			assert.Equal(t, want, o.OrderStatus, id)
		}
	})

	t.Run("a second run finds nothing left", func(t *testing.T) {
		got, err := e.repo.CompleteInProgressOrders(e.ctx)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("FindUnhandledPendingOrderIDs lists cash and paid orders only", func(t *testing.T) {
		ids, err := e.repo.FindUnhandledPendingOrderIDs(e.ctx)
		require.NoError(t, err)
		assert.Equal(t, []uuid.UUID{cashPending.ID, paidPending.ID}, ids)
	})

	t.Run("query failures are returned", func(t *testing.T) {
		repo := closedRepo(t)
		_, err := repo.CompleteInProgressOrders(e.ctx)
		assert.ErrorContains(t, err, "failed to complete in-progress orders")
		_, err = repo.FindUnhandledPendingOrderIDs(e.ctx)
		assert.ErrorContains(t, err, "failed to query unhandled pending orders")
	})
}
