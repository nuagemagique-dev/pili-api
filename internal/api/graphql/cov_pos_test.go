package graphql_test

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tsb-service/internal/api/graphql/apperr"
	posApplication "tsb-service/internal/modules/pos/application"
)

// The handheld administration: an admin creates a one-time code, the device redeems it, the admin
// sees it in the list and revokes it.
func TestPosDeviceAdministration(t *testing.T) {
	env := setupCovEnv(t, covOptions{})
	r := env.Resolver
	admin := env.ctxFor(env.Fixtures.AdminUser.ID.String(), true, "fr")

	t.Run("createPosEnrollment refuses a blank label", func(t *testing.T) {
		_, err := r.Mutation().CreatePosEnrollment(admin, "   ")
		requireCode(t, err, apperr.CodeUserError)
	})

	enrollment, err := r.Mutation().CreatePosEnrollment(admin, "YGF comptoir")
	require.NoError(t, err)
	assert.Regexp(t, `^[A-Z2-9]{4}-[A-Z2-9]{4}$`, enrollment.Code)
	assert.WithinDuration(t, time.Now().Add(posApplication.EnrollmentTTL), enrollment.ExpiresAt, time.Minute)

	var createdBy uuid.UUID
	require.NoError(t, env.DB.DB.Get(&createdBy, `SELECT created_by FROM pos_enrollments`))
	assert.Equal(t, env.Fixtures.AdminUser.ID, createdBy)

	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	device, err := r.PosService.Enroll(t.Context(), posApplication.EnrollInput{Code: enrollment.Code, Secret: secret, SerialNumber: "android-ygf"})
	require.NoError(t, err)

	t.Run("posDevices lists the enrolled device", func(t *testing.T) {
		list, err := r.Query().PosDevices(admin)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, device.DeviceID, list[0].ID)
		assert.Equal(t, "YGF comptoir", list[0].Label)
		assert.Equal(t, "android-ygf", list[0].SerialNumber)
		assert.Nil(t, list[0].RevokedAt)
	})

	t.Run("revokePosDevice stamps it; an unknown id is NOT_FOUND", func(t *testing.T) {
		revoked, err := r.Mutation().RevokePosDevice(admin, device.DeviceID)
		require.NoError(t, err)
		assert.NotNil(t, revoked.RevokedAt)

		_, err = r.Mutation().RevokePosDevice(admin, uuid.New())
		requireCode(t, err, apperr.CodeNotFound)
	})
}
