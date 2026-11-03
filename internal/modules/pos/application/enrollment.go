package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"tsb-service/internal/modules/pos/domain"
)

// ErrInvalidEnrollmentInput is a malformed enroll request or label (handler: 400).
var ErrInvalidEnrollmentInput = errors.New("invalid enrollment input")

// ErrDeviceNotFound is an unknown device id in an admin call.
var ErrDeviceNotFound = errors.New("pos device not found")

const (
	// EnrollmentTTL is how long a code can be redeemed after it is created.
	EnrollmentTTL = 15 * time.Minute
	// enrollmentAlphabet leaves out 0/O, 1/I/L so a code read aloud or typed is unambiguous.
	enrollmentAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	enrollmentCodeLen  = 8
	deviceSecretBytes  = 32
	maxLabelLen        = 64
	maxSerialLen       = 128
)

// WithEnrollment enables runtime enrollment and device administration.
func (s *Service) WithEnrollment(e domain.EnrollmentRepository, a domain.DeviceAdminRepository) *Service {
	s.enrollments = e
	s.deviceAdmin = a
	return s
}

// Enrollment is a freshly created code, shown once in the dashboard.
type Enrollment struct {
	Code      string // formatted "ABCD-EFGH"
	ExpiresAt time.Time
}

// CreateEnrollment creates a one-time code that enrolls one handheld under label.
func (s *Service) CreateEnrollment(ctx context.Context, label string, createdBy *uuid.UUID) (*Enrollment, error) {
	label = strings.TrimSpace(label)
	if label == "" || len([]rune(label)) > maxLabelLen {
		return nil, fmt.Errorf("%w: label must be 1 to %d characters", ErrInvalidEnrollmentInput, maxLabelLen)
	}
	raw, err := newEnrollmentCode()
	if err != nil {
		return nil, err
	}
	e := &domain.Enrollment{
		ID:        uuid.New(),
		CodeHash:  hashEnrollmentCode(raw),
		Label:     label,
		CreatedBy: createdBy,
		ExpiresAt: time.Now().Add(EnrollmentTTL).UTC(),
	}
	if err := s.enrollments.Create(ctx, e); err != nil {
		return nil, fmt.Errorf("create enrollment: %w", err)
	}
	return &Enrollment{Code: raw[:4] + "-" + raw[4:], ExpiresAt: e.ExpiresAt}, nil
}

// EnrollInput is what a handheld sends to POST /pos/devices/enroll. The secret is generated on
// the device and never shown; the server keeps its SHA-256 like any other device secret.
type EnrollInput struct {
	Code         string
	Secret       string // base64 of 32 random bytes
	SerialNumber string // stable per device, e.g. derived from ANDROID_ID
}

// EnrolledDevice is returned to the handheld after a successful enrollment.
type EnrolledDevice struct {
	DeviceID uuid.UUID
	Label    string
}

// Enroll redeems a code and creates (or re-keys) the device that presented it.
func (s *Service) Enroll(ctx context.Context, in EnrollInput) (*EnrolledDevice, error) {
	secret, err := base64.StdEncoding.DecodeString(in.Secret)
	if err != nil || len(secret) != deviceSecretBytes {
		return nil, fmt.Errorf("%w: secret must be %d base64 bytes", ErrInvalidEnrollmentInput, deviceSecretBytes)
	}
	serial := strings.TrimSpace(in.SerialNumber)
	if serial == "" || len(serial) > maxSerialLen || strings.IndexFunc(serial, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("%w: serial number", ErrInvalidEnrollmentInput)
	}
	code := normalizeEnrollmentCode(in.Code)
	if len(code) != enrollmentCodeLen {
		return nil, domain.ErrEnrollmentCodeInvalid
	}
	sum := sha256.Sum256(secret)
	device, err := s.enrollments.Redeem(ctx, hashEnrollmentCode(code), domain.EnrolledDevice{
		SerialNumber:     serial,
		DeviceSecretHash: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return nil, err
	}
	// A re-enrolled device may have been revoked a moment ago on this replica.
	s.active.put(device.ID, nil)
	return &EnrolledDevice{DeviceID: device.ID, Label: device.Label}, nil
}

// ListDevices returns every enrolled device for the dashboard.
func (s *Service) ListDevices(ctx context.Context) ([]*domain.Device, error) {
	return s.deviceAdmin.ListAll(ctx)
}

// RevokeDevice cuts a device off: device-login refuses it at once, and its access tokens stop
// working within deviceStatusTTL on the other replicas.
func (s *Service) RevokeDevice(ctx context.Context, id uuid.UUID) (*domain.Device, error) {
	d, err := s.deviceAdmin.Revoke(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}
	if err != nil {
		return nil, err
	}
	s.active.put(id, ErrDeviceRevoked)
	return d, nil
}

func newEnrollmentCode() (string, error) {
	var b strings.Builder
	n := big.NewInt(int64(len(enrollmentAlphabet)))
	for range enrollmentCodeLen {
		i, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", fmt.Errorf("enrollment code: %w", err)
		}
		b.WriteByte(enrollmentAlphabet[i.Int64()])
	}
	return b.String(), nil
}

// normalizeEnrollmentCode accepts the code as typed: any case, with or without separators.
func normalizeEnrollmentCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hashEnrollmentCode(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
