package infrastructure

import (
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tsb-service/internal/api/graphql/testhelpers"
	"tsb-service/internal/modules/pos/domain"
	"tsb-service/pkg/db"
)

func TestEnrollmentRepository(t *testing.T) {
	tdb := testhelpers.SetupTestDatabase(t)
	pool := &db.DBPool{Customer: tdb.DB, Admin: tdb.DB}
	enrollments := NewEnrollmentRepository(pool)
	devices := NewDeviceRepository(pool)
	ctx := t.Context()

	create := func(hash, label string, ttl time.Duration) {
		t.Helper()
		e := &domain.Enrollment{ID: uuid.New(), CodeHash: hash, Label: label, ExpiresAt: time.Now().Add(ttl)}
		require.NoError(t, enrollments.Create(ctx, e))
		assert.False(t, e.CreatedAt.IsZero())
	}

	t.Run("Redeem creates the device under the code's label and uses the code up", func(t *testing.T) {
		create("hash-1", "Comptoir", time.Minute)
		d, err := enrollments.Redeem(ctx, "hash-1", domain.EnrolledDevice{SerialNumber: "android-1", DeviceSecretHash: "aa"})
		require.NoError(t, err)
		assert.Equal(t, "Comptoir", d.Label)
		assert.Equal(t, "aa", d.DeviceSecretHash)
		assert.Nil(t, d.RevokedAt)

		var usedBy uuid.UUID
		require.NoError(t, tdb.DB.GetContext(ctx, &usedBy, `SELECT device_id FROM pos_enrollments WHERE code_hash = 'hash-1' AND used_at IS NOT NULL`))
		assert.Equal(t, d.ID, usedBy)

		_, err = enrollments.Redeem(ctx, "hash-1", domain.EnrolledDevice{SerialNumber: "android-2", DeviceSecretHash: "bb"})
		assert.ErrorIs(t, err, domain.ErrEnrollmentCodeInvalid)
	})

	t.Run("expired and unknown codes are refused", func(t *testing.T) {
		create("hash-expired", "Old", -time.Second)
		_, err := enrollments.Redeem(ctx, "hash-expired", domain.EnrolledDevice{SerialNumber: "x", DeviceSecretHash: "cc"})
		assert.ErrorIs(t, err, domain.ErrEnrollmentCodeInvalid)
		_, err = enrollments.Redeem(ctx, "nope", domain.EnrolledDevice{SerialNumber: "x", DeviceSecretHash: "cc"})
		assert.ErrorIs(t, err, domain.ErrEnrollmentCodeInvalid)
	})

	t.Run("re-enrolling a device keeps its id, re-keys it and clears revocation and push", func(t *testing.T) {
		create("hash-a", "First", time.Minute)
		first, err := enrollments.Redeem(ctx, "hash-a", domain.EnrolledDevice{SerialNumber: "android-re", DeviceSecretHash: "11"})
		require.NoError(t, err)
		require.NoError(t, devices.UpdateFCMInstallationID(ctx, first.ID, "fid-1"))
		_, err = devices.Revoke(ctx, first.ID)
		require.NoError(t, err)

		create("hash-b", "Second", time.Minute)
		again, err := enrollments.Redeem(ctx, "hash-b", domain.EnrolledDevice{SerialNumber: "android-re", DeviceSecretHash: "22"})
		require.NoError(t, err)
		assert.Equal(t, first.ID, again.ID)
		assert.Equal(t, "22", again.DeviceSecretHash)
		assert.Equal(t, "Second", again.Label)
		assert.Nil(t, again.RevokedAt)
		assert.Nil(t, again.FCMFid)
	})

	t.Run("two devices racing on one code: exactly one wins", func(t *testing.T) {
		create("hash-race", "Race", time.Minute)
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Go(func() {
				_, errs[i] = enrollments.Redeem(ctx, "hash-race", domain.EnrolledDevice{
					SerialNumber: "race-" + uuid.NewString(), DeviceSecretHash: "dd",
				})
			})
		}
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else {
				assert.ErrorIs(t, err, domain.ErrEnrollmentCodeInvalid)
			}
		}
		assert.Equal(t, 1, ok)
	})
}

func TestDeviceAdmin(t *testing.T) {
	tdb := testhelpers.SetupTestDatabase(t)
	repo := NewDeviceRepository(&db.DBPool{Customer: tdb.DB, Admin: tdb.DB})
	ctx := t.Context()

	insert := func(serial string, registered time.Time, revoked bool) uuid.UUID {
		id := uuid.New()
		_, err := tdb.DB.ExecContext(ctx, `
			INSERT INTO pos_devices (id, serial_number, device_secret_hash, label, registered_at, revoked_at)
			VALUES ($1, $2, 'h', $2, $3, CASE WHEN $4 THEN now() END)`, id, serial, registered, revoked)
		require.NoError(t, err)
		return id
	}
	now := time.Now()
	old := insert("old", now.Add(-48*time.Hour), false)
	newer := insert("newer", now.Add(-time.Hour), false)
	gone := insert("gone", now, true)

	t.Run("ListAll puts active devices first, newest first", func(t *testing.T) {
		list, err := repo.ListAll(ctx)
		require.NoError(t, err)
		ids := make([]uuid.UUID, len(list))
		for i, d := range list {
			ids[i] = d.ID
		}
		assert.Equal(t, []uuid.UUID{newer, old, gone}, ids)
	})

	t.Run("Revoke keeps the first stamp; unknown ids are sql.ErrNoRows", func(t *testing.T) {
		d, err := repo.Revoke(ctx, old)
		require.NoError(t, err)
		require.NotNil(t, d.RevokedAt)
		first := *d.RevokedAt

		again, err := repo.Revoke(ctx, old)
		require.NoError(t, err)
		assert.True(t, first.Equal(*again.RevokedAt))

		_, err = repo.Revoke(ctx, uuid.New())
		assert.ErrorIs(t, err, sql.ErrNoRows)
	})
}
