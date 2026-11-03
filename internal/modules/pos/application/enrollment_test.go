package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"tsb-service/internal/modules/pos/domain"
)

// fakeEnrollments keeps codes in memory and creates devices in the shared fakeDevices, so a
// redeemed code can be followed by a real DeviceLogin.
type fakeEnrollments struct {
	devices *fakeDevices
	codes   map[string]*domain.Enrollment // by code hash
	serials map[string]uuid.UUID
}

func (f *fakeEnrollments) Create(_ context.Context, e *domain.Enrollment) error {
	cp := *e
	f.codes[e.CodeHash] = &cp
	return nil
}

func (f *fakeEnrollments) Redeem(_ context.Context, codeHash string, d domain.EnrolledDevice) (*domain.Device, error) {
	e, ok := f.codes[codeHash]
	if !ok || e.UsedAt != nil || !e.ExpiresAt.After(time.Now()) {
		return nil, domain.ErrEnrollmentCodeInvalid
	}
	id, known := f.serials[d.SerialNumber]
	if !known {
		id = uuid.New()
		f.serials[d.SerialNumber] = id
	}
	dev := &domain.Device{ID: id, SerialNumber: d.SerialNumber, DeviceSecretHash: d.DeviceSecretHash, Label: e.Label}
	f.devices.devices[id] = dev
	now := time.Now()
	e.UsedAt, e.DeviceID = &now, &id
	return dev, nil
}

type fakeAdmin struct{ devices *fakeDevices }

func (f *fakeAdmin) ListAll(context.Context) ([]*domain.Device, error) {
	out := make([]*domain.Device, 0, len(f.devices.devices))
	for _, d := range f.devices.devices {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeAdmin) Revoke(_ context.Context, id uuid.UUID) (*domain.Device, error) {
	d, ok := f.devices.devices[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	if d.RevokedAt == nil {
		now := time.Now()
		d.RevokedAt = &now
	}
	return d, nil
}

func newEnrollmentPOS(t *testing.T) (*Service, *fakeEnrollments) {
	t.Helper()
	devices := &fakeDevices{devices: map[uuid.UUID]*domain.Device{}}
	enr := &fakeEnrollments{devices: devices, codes: map[string]*domain.Enrollment{}, serials: map[string]uuid.UUID{}}
	s := NewService(DefaultConfig(testSecret), devices).WithEnrollment(enr, &fakeAdmin{devices: devices})
	return s, enr
}

// deviceSecret is what the handheld generates: 32 random bytes, sent as base64.
func deviceSecret(fill byte) (string, []byte) {
	raw := make([]byte, deviceSecretBytes)
	for i := range raw {
		raw[i] = fill
	}
	key := sha256.Sum256(raw)
	return base64.StdEncoding.EncodeToString(raw), key[:]
}

func TestCreateEnrollment_FormatsAOneTimeCode(t *testing.T) {
	s, enr := newEnrollmentPOS(t)
	e, err := s.CreateEnrollment(t.Context(), "  Comptoir  ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[` + enrollmentAlphabet + `]{4}-[` + enrollmentAlphabet + `]{4}$`).MatchString(e.Code) {
		t.Fatalf("code %q is not ABCD-EFGH from the unambiguous alphabet", e.Code)
	}
	if d := time.Until(e.ExpiresAt); d < EnrollmentTTL-time.Minute || d > EnrollmentTTL {
		t.Fatalf("expires in %v, want about %v", d, EnrollmentTTL)
	}
	stored := enr.codes[hashEnrollmentCode(normalizeEnrollmentCode(e.Code))]
	if stored == nil || stored.Label != "Comptoir" {
		t.Fatalf("stored enrollment %+v: want the hashed code with the trimmed label", stored)
	}
	for hash := range enr.codes {
		if strings.Contains(hash, strings.ReplaceAll(e.Code, "-", "")) {
			t.Fatal("the plaintext code must not be stored")
		}
	}
}

func TestCreateEnrollment_RejectsABadLabel(t *testing.T) {
	s, _ := newEnrollmentPOS(t)
	for _, label := range []string{"", "   ", strings.Repeat("x", maxLabelLen+1)} {
		if _, err := s.CreateEnrollment(t.Context(), label, nil); !errors.Is(err, ErrInvalidEnrollmentInput) {
			t.Fatalf("label %q: err %v, want ErrInvalidEnrollmentInput", label, err)
		}
	}
}

func TestEnroll_TheDeviceThenSignsInWithItsOwnSecret(t *testing.T) {
	s, _ := newEnrollmentPOS(t)
	e, err := s.CreateEnrollment(t.Context(), "YGF comptoir", nil)
	if err != nil {
		t.Fatal(err)
	}
	secret, key := deviceSecret(7)
	// Typed by hand: lower case, no dash.
	typed := strings.ToLower(strings.ReplaceAll(e.Code, "-", ""))
	dev, err := s.Enroll(t.Context(), EnrollInput{Code: typed, Secret: secret, SerialNumber: "android-abc"})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if dev.Label != "YGF comptoir" {
		t.Fatalf("label %q, want the code's label", dev.Label)
	}

	tok := login(t, s, dev.DeviceID, key)
	if got, err := s.VerifyAccessToken(t.Context(), tok); err != nil || got != dev.DeviceID {
		t.Fatalf("VerifyAccessToken: %v %v", got, err)
	}
}

func TestEnroll_ACodeWorksOnce(t *testing.T) {
	s, _ := newEnrollmentPOS(t)
	e, _ := s.CreateEnrollment(t.Context(), "Comptoir", nil)
	secret, _ := deviceSecret(1)
	if _, err := s.Enroll(t.Context(), EnrollInput{Code: e.Code, Secret: secret, SerialNumber: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Enroll(t.Context(), EnrollInput{Code: e.Code, Secret: secret, SerialNumber: "b"}); !errors.Is(err, domain.ErrEnrollmentCodeInvalid) {
		t.Fatalf("second use: %v, want ErrEnrollmentCodeInvalid", err)
	}
}

func TestEnroll_RejectsBadInput(t *testing.T) {
	s, _ := newEnrollmentPOS(t)
	e, _ := s.CreateEnrollment(t.Context(), "Comptoir", nil)
	good, _ := deviceSecret(2)
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	cases := []struct {
		name string
		in   EnrollInput
		want error
	}{
		{"short secret", EnrollInput{Code: e.Code, Secret: short, SerialNumber: "s"}, ErrInvalidEnrollmentInput},
		{"secret not base64", EnrollInput{Code: e.Code, Secret: "%%%", SerialNumber: "s"}, ErrInvalidEnrollmentInput},
		{"blank serial", EnrollInput{Code: e.Code, Secret: good, SerialNumber: "  "}, ErrInvalidEnrollmentInput},
		{"control char in serial", EnrollInput{Code: e.Code, Secret: good, SerialNumber: "a\nb"}, ErrInvalidEnrollmentInput},
		{"short code", EnrollInput{Code: "ABC", Secret: good, SerialNumber: "s"}, domain.ErrEnrollmentCodeInvalid},
		{"unknown code", EnrollInput{Code: "ZZZZ-ZZZZ", Secret: good, SerialNumber: "s"}, domain.ErrEnrollmentCodeInvalid},
	}
	for _, c := range cases {
		if _, err := s.Enroll(t.Context(), c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: err %v, want %v", c.name, err, c.want)
		}
	}
	// None of the refused attempts used the code up.
	if _, err := s.Enroll(t.Context(), EnrollInput{Code: e.Code, Secret: good, SerialNumber: "s"}); err != nil {
		t.Fatalf("valid attempt after refusals: %v", err)
	}
}

func TestRevokeDevice_CutsTheTokenAtOnceOnThisReplica(t *testing.T) {
	s, _ := newEnrollmentPOS(t)
	e, _ := s.CreateEnrollment(t.Context(), "Comptoir", nil)
	secret, key := deviceSecret(3)
	dev, err := s.Enroll(t.Context(), EnrollInput{Code: e.Code, Secret: secret, SerialNumber: "s"})
	if err != nil {
		t.Fatal(err)
	}
	tok := login(t, s, dev.DeviceID, key)
	if _, err := s.VerifyAccessToken(t.Context(), tok); err != nil {
		t.Fatal(err)
	}

	revoked, err := s.RevokeDevice(t.Context(), dev.DeviceID)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("RevokeDevice: %+v %v", revoked, err)
	}
	if _, err := s.VerifyAccessToken(t.Context(), tok); !errors.Is(err, ErrDeviceRevoked) {
		t.Fatalf("token after revoke: %v, want ErrDeviceRevoked", err)
	}

	// Re-enrolling the same device with a new code brings it back right away.
	e2, _ := s.CreateEnrollment(t.Context(), "Comptoir", nil)
	secret2, key2 := deviceSecret(4)
	again, err := s.Enroll(t.Context(), EnrollInput{Code: e2.Code, Secret: secret2, SerialNumber: "s"})
	if err != nil || again.DeviceID != dev.DeviceID {
		t.Fatalf("re-enroll: %+v %v, want the same device id", again, err)
	}
	if _, err := s.VerifyAccessToken(t.Context(), login(t, s, dev.DeviceID, key2)); err != nil {
		t.Fatalf("token after re-enroll: %v", err)
	}
}

func TestRevokeDevice_UnknownID(t *testing.T) {
	s, _ := newEnrollmentPOS(t)
	if _, err := s.RevokeDevice(t.Context(), uuid.New()); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("err %v, want ErrDeviceNotFound", err)
	}
}

func TestEnrollmentCodes_AreRandom(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		c, err := newEnrollmentCode()
		if err != nil {
			t.Fatal(err)
		}
		if seen[c] {
			t.Fatalf("duplicate code %s in 200 draws", c)
		}
		seen[c] = true
	}
}
