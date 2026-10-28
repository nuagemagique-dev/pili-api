package resolver

import (
	"regexp"
	"strings"

	"tsb-service/internal/api/graphql/apperr"
)

// idempotencyKeyPattern is what a checkout key may hold: a UUID, or any opaque token of the same
// alphabet, so it is safe to log and to index.
var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// parseIdempotencyKey returns the trimmed checkout key, nil when the client sent none (or only
// blanks), or an IDEMPOTENCY_KEY_INVALID error.
func parseIdempotencyKey(in *string) (*string, error) {
	if in == nil {
		return nil, nil
	}
	key := strings.TrimSpace(*in)
	if key == "" {
		return nil, nil
	}
	if !idempotencyKeyPattern.MatchString(key) {
		return nil, apperr.New(apperr.CodeIdempotencyKeyInvalid, "idempotencyKey must be 8 to 64 letters, digits, '-' or '_'")
	}
	return &key, nil
}
