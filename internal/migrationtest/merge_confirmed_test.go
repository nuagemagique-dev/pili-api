package migrationtest_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mergeConfirmedVersion = 20261103140000

// TestMergeConfirmedIntoPreparing runs the CONFIRMED/PREPARING merge on real rows: confirmed
// orders become PREPARING, their history keeps the confirmation moment without showing
// PREPARING twice, and the constraint refuses CONFIRMED afterwards.
func TestMergeConfirmedIntoPreparing(t *testing.T) {
	db, dir, _ := setup(t)
	require.NoError(t, goose.UpTo(db, dir, mergeConfirmedVersion-1))

	user := uuid.New()
	_, err := db.Exec(`INSERT INTO users (id, first_name, last_name, email, zitadel_user_id)
		VALUES ($1, 'Test', 'Merge', $2, $3)`, user, user.String()+"@example.com", user.String())
	require.NoError(t, err)
	order := func(status string, history ...string) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, db.QueryRow(`INSERT INTO orders (user_id, order_type, order_status, total_price, language)
			VALUES ($1, 'PICKUP', $2, 18.00, 'fr') RETURNING id`, user, status).Scan(&id))
		for i, h := range history {
			_, err := db.Exec(`INSERT INTO order_status_history (order_id, status, changed_at)
				VALUES ($1, $2, now() - make_interval(mins => $3))`, id, h, 60-i)
			require.NoError(t, err)
		}
		return id
	}
	confirmed := order("CONFIRMED", "PENDING", "CONFIRMED")
	preparing := order("PREPARING", "PENDING", "CONFIRMED", "PREPARING")
	ready := order("AWAITING_PICK_UP", "PENDING", "CONFIRMED", "PREPARING", "AWAITING_PICK_UP")
	pending := order("PENDING", "PENDING")

	require.NoError(t, goose.UpByOne(db, dir))

	status := func(id uuid.UUID) (s string) {
		require.NoError(t, db.QueryRow(`SELECT order_status FROM orders WHERE id = $1`, id).Scan(&s))
		return s
	}
	history := func(id uuid.UUID) (out []string) {
		rows, err := db.Query(`SELECT status FROM order_status_history WHERE order_id = $1 ORDER BY changed_at, id`, id)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var s string
			require.NoError(t, rows.Scan(&s))
			out = append(out, s)
		}
		return out
	}
	assert.Equal(t, "PREPARING", status(confirmed))
	assert.Equal(t, "PREPARING", status(preparing))
	assert.Equal(t, "AWAITING_PICK_UP", status(ready))
	assert.Equal(t, "PENDING", status(pending))
	assert.Equal(t, []string{"PENDING", "PREPARING"}, history(confirmed))
	assert.Equal(t, []string{"PENDING", "PREPARING"}, history(preparing))
	assert.Equal(t, []string{"PENDING", "PREPARING", "AWAITING_PICK_UP"}, history(ready))
	assert.Equal(t, []string{"PENDING"}, history(pending))

	_, err = db.Exec(`UPDATE orders SET order_status = 'CONFIRMED' WHERE id = $1`, pending)
	require.Error(t, err, "CONFIRMED is no longer a valid order status")
}
