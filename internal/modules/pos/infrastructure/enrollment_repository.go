package infrastructure

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"tsb-service/internal/modules/pos/domain"
	"tsb-service/pkg/db"
)

type EnrollmentRepository struct {
	pool *db.DBPool
}

func NewEnrollmentRepository(pool *db.DBPool) domain.EnrollmentRepository {
	return &EnrollmentRepository{pool: pool}
}

func (r *EnrollmentRepository) Create(ctx context.Context, e *domain.Enrollment) error {
	const q = `INSERT INTO pos_enrollments (id, code_hash, label, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING created_at`
	return r.pool.ForContext(ctx).GetContext(ctx, &e.CreatedAt, q, e.ID, e.CodeHash, e.Label, e.CreatedBy, e.ExpiresAt)
}

// Redeem locks the code row and names the device after the code's label, so two devices racing on one code cannot both enroll. A device
// that enrolls again (same serial number, e.g. after a reset or a move to another restaurant
// and back) keeps its id: its row is re-keyed, un-revoked and its push address dropped.
func (r *EnrollmentRepository) Redeem(ctx context.Context, codeHash string, d domain.EnrolledDevice) (*domain.Device, error) {
	tx, err := r.pool.ForContext(ctx).BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var enrollment struct {
		ID    uuid.UUID `db:"id"`
		Label string    `db:"label"`
	}
	err = tx.GetContext(ctx, &enrollment, `SELECT id, label FROM pos_enrollments
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now() FOR UPDATE`, codeHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrEnrollmentCodeInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("load enrollment: %w", err)
	}

	var device domain.Device
	q := `INSERT INTO pos_devices (serial_number, device_secret_hash, label)
		VALUES ($1, $2, $3)
		ON CONFLICT (serial_number) DO UPDATE SET
			device_secret_hash = EXCLUDED.device_secret_hash,
			label = EXCLUDED.label,
			registered_at = now(),
			revoked_at = NULL,
			fcm_token = NULL, fcm_token_updated_at = NULL,
			fcm_fid = NULL, fcm_fid_updated_at = NULL
		RETURNING ` + deviceColumns
	if err := tx.GetContext(ctx, &device, q, d.SerialNumber, d.DeviceSecretHash, enrollment.Label); err != nil {
		return nil, fmt.Errorf("upsert device: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE pos_enrollments SET used_at = now(), device_id = $2 WHERE id = $1`,
		enrollment.ID, device.ID); err != nil {
		return nil, fmt.Errorf("mark enrollment used: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &device, nil
}
