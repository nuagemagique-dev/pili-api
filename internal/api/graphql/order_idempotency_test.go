package graphql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tsb-service/internal/api/graphql/apperr"
	"tsb-service/internal/api/graphql/resolver"
	orderApplication "tsb-service/internal/modules/order/application"
	orderDomain "tsb-service/internal/modules/order/domain"
)

// A checkout retried with the same idempotency key (the reply of the first try was lost on a
// dropped connection) gets the order it already placed, never a second one.

func TestCreateOrderIdempotencyKey(t *testing.T) {
	shop := setupShop(t)
	tc := shop.tc

	t.Run("a cash retry with the same key returns the same order", func(t *testing.T) {
		userID, token := shop.customer(t, "idem-cash")
		input := createOrderInput("PICKUP", lines(shop.salmon, 2), map[string]any{"idempotencyKey": uuid.NewString()})

		first := mustCreateOrder(t, tc, token, "fr", input)
		again := mustCreateOrder(t, tc, token, "fr", input)

		assert.Equal(t, first.ID, again.ID)
		assert.Equal(t, first.TotalPrice, again.TotalPrice)
		assert.Equal(t, 1, shop.orderCount(t, userID), "one order stored")
	})

	t.Run("an online retry returns the same order and its payment, Mollie is called once", func(t *testing.T) {
		userID, token := shop.customer(t, "idem-online")
		input := createOrderInput("PICKUP", lines(shop.salmon, 2), map[string]any{
			"isOnlinePayment": true,
			"idempotencyKey":  uuid.NewString(),
		})

		mollieBefore := len(shop.stub.Requests())
		first := mustCreateOrder(t, tc, token, "fr", input)
		again := mustCreateOrder(t, tc, token, "fr", input)

		assert.Equal(t, first.ID, again.ID)
		require.NotNil(t, again.Payment, "the retry still carries the checkout link")
		assert.Equal(t, first.Payment.MolliePaymentID, again.Payment.MolliePaymentID)
		assert.Contains(t, string(again.Payment.Links), "https://www.mollie.com/checkout/")
		assert.Len(t, shop.stub.Requests(), mollieBefore+1, "one payment created")
		assert.Equal(t, 1, shop.orderCount(t, userID))
	})

	t.Run("a new key places a new order", func(t *testing.T) {
		userID, token := shop.customer(t, "idem-new")
		items := lines(shop.salmon, 1)
		a := mustCreateOrder(t, tc, token, "fr", createOrderInput("PICKUP", items, map[string]any{"idempotencyKey": uuid.NewString()}))
		b := mustCreateOrder(t, tc, token, "fr", createOrderInput("PICKUP", items, map[string]any{"idempotencyKey": uuid.NewString()}))
		assert.NotEqual(t, a.ID, b.ID)
		assert.Equal(t, 2, shop.orderCount(t, userID))
	})

	t.Run("no key or a blank key keeps the old behaviour", func(t *testing.T) {
		userID, token := shop.customer(t, "idem-none")
		items := lines(shop.salmon, 1)
		mustCreateOrder(t, tc, token, "fr", createOrderInput("PICKUP", items, nil))
		mustCreateOrder(t, tc, token, "fr", createOrderInput("PICKUP", items, nil))
		mustCreateOrder(t, tc, token, "fr", createOrderInput("PICKUP", items, map[string]any{"idempotencyKey": "   "}))
		mustCreateOrder(t, tc, token, "fr", createOrderInput("PICKUP", items, map[string]any{"idempotencyKey": "   "}))
		assert.Equal(t, 4, shop.orderCount(t, userID))
	})

	t.Run("the key is per customer", func(t *testing.T) {
		key := uuid.NewString()
		items := lines(shop.salmon, 1)
		_, tokenA := shop.customer(t, "idem-a")
		_, tokenB := shop.customer(t, "idem-b")
		a := mustCreateOrder(t, tc, tokenA, "fr", createOrderInput("PICKUP", items, map[string]any{"idempotencyKey": key}))
		b := mustCreateOrder(t, tc, tokenB, "fr", createOrderInput("PICKUP", items, map[string]any{"idempotencyKey": key}))
		assert.NotEqual(t, a.ID, b.ID, "another customer's key never returns their order")
	})

	t.Run("the key is stored trimmed", func(t *testing.T) {
		key := uuid.NewString()
		_, token := shop.customer(t, "idem-trim")
		order := mustCreateOrder(t, tc, token, "fr", createOrderInput("PICKUP", lines(shop.salmon, 1), map[string]any{"idempotencyKey": "  " + key + " "}))
		var stored string
		require.NoError(t, tc.DB.DB.GetContext(t.Context(), &stored, `SELECT idempotency_key FROM orders WHERE id = $1`, order.ID))
		assert.Equal(t, key, stored)
	})

	t.Run("a malformed key is refused and reported", func(t *testing.T) {
		userID, token := shop.customer(t, "idem-bad")
		for _, bad := range []string{"short", "has spaces inside it", "semi;colon-1234", strings.Repeat("a", 65)} {
			got, orderErr := createOrderAs(t, tc, token, "fr", createOrderInput("PICKUP", lines(shop.salmon, 1), map[string]any{"idempotencyKey": bad}))
			require.Nil(t, got, "key %q", bad)
			require.NotNil(t, orderErr, "key %q", bad)
			assert.Equal(t, string(apperr.CodeIdempotencyKeyInvalid), orderErr.Code())
		}
		assert.False(t, apperr.IsExpected(apperr.CodeIdempotencyKeyInvalid), "a bad key is a client bug: it reaches Sentry")
		assert.Equal(t, 0, shop.orderCount(t, userID))
	})

	t.Run("a concurrent duplicate that slips past the lookup gets the first order back", func(t *testing.T) {
		userID, token := shop.customer(t, "idem-race")
		input := createOrderInput("PICKUP", lines(shop.salmon, 2), map[string]any{"idempotencyKey": uuid.NewString()})
		first := mustCreateOrder(t, tc, token, "fr", input)

		// The first lookup misses as if the other request had not committed yet: the insert then
		// hits the unique index and the resolver reads the winner.
		racing := withResolver(t, tc, func(r *resolver.Resolver) {
			r.OrderService = &lateLookupOrderService{OrderService: r.OrderService}
		})
		again := mustCreateOrder(t, racing, token, "fr", input)

		assert.Equal(t, first.ID, again.ID)
		assert.Equal(t, 1, shop.orderCount(t, userID))
	})

	t.Run("a failing lookup is a server fault, nothing is placed", func(t *testing.T) {
		userID, token := shop.customer(t, "idem-lookup-fail")
		broken := withResolver(t, tc, func(r *resolver.Resolver) {
			r.OrderService = &brokenLookupOrderService{OrderService: r.OrderService}
		})
		got, orderErr := createOrderAs(t, broken, token, "fr", createOrderInput("PICKUP", lines(shop.salmon, 1), map[string]any{"idempotencyKey": uuid.NewString()}))
		require.Nil(t, got)
		require.NotNil(t, orderErr)
		assert.Equal(t, string(apperr.CodeOrderCreateFailed), orderErr.Code())
		assert.Equal(t, 0, shop.orderCount(t, userID))
	})
}

// lateLookupOrderService misses its first idempotency lookup, everything else is real.
type lateLookupOrderService struct {
	orderApplication.OrderService
	calls int
}

func (s *lateLookupOrderService) GetOrderByIdempotencyKey(ctx context.Context, userID uuid.UUID, key string) (*orderDomain.Order, error) {
	s.calls++
	if s.calls == 1 {
		return nil, nil
	}
	return s.OrderService.GetOrderByIdempotencyKey(ctx, userID, key)
}

// brokenLookupOrderService fails its idempotency lookup, everything else is real.
type brokenLookupOrderService struct {
	orderApplication.OrderService
}

func (s *brokenLookupOrderService) GetOrderByIdempotencyKey(context.Context, uuid.UUID, string) (*orderDomain.Order, error) {
	return nil, context.DeadlineExceeded
}
