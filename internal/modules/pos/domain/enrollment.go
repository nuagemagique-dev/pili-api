package domain

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrEnrollmentCodeInvalid means the code is unknown, expired or already used. The three are
// not told apart, so a caller learns nothing about which codes exist.
var ErrEnrollmentCodeInvalid = errors.New("enrollment code invalid or expired")

// Enrollment is a one-time code an admin creates so a handheld can enroll itself. Only the
// SHA-256 of the code is stored.
type Enrollment struct {
	ID        uuid.UUID  `db:"id"`
	CodeHash  string     `db:"code_hash"`
	Label     string     `db:"label"`
	CreatedBy *uuid.UUID `db:"created_by"`
	CreatedAt time.Time  `db:"created_at"`
	ExpiresAt time.Time  `db:"expires_at"`
	UsedAt    *time.Time `db:"used_at"`
	DeviceID  *uuid.UUID `db:"device_id"`
}

// EnrolledDevice is the device presenting a code: the row to create, or to re-key when a device
// with that serial number already exists. It takes the code's label.
type EnrolledDevice struct {
	SerialNumber     string
	DeviceSecretHash string
}

// EnrollmentRepository stores enrollment codes and redeems them.
type EnrollmentRepository interface {
	Create(ctx context.Context, e *Enrollment) error
	// Redeem atomically consumes the unused, unexpired code with this hash and creates or
	// re-keys the device. It returns ErrEnrollmentCodeInvalid when no such code exists.
	Redeem(ctx context.Context, codeHash string, d EnrolledDevice) (*Device, error)
}
