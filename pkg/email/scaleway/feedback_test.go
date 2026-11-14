package scaleway

import (
	"encoding/json"
	"net/http"
	"testing"

	"tsb-service/pkg/brand"
	"tsb-service/pkg/email/smtptest"

	"github.com/stretchr/testify/require"
)

// useRestaurantEmail sets RESTAURANT_EMAIL for this test and reloads the brand config, which is
// reloaded again from the restored environment when the test ends.
func useRestaurantEmail(t *testing.T, email string) {
	t.Helper()
	t.Cleanup(func() { brand.Load() })
	t.Setenv("RESTAURANT_EMAIL", email)
	brand.Load()
}

func TestFeedbackRecipient(t *testing.T) {
	t.Run("the explicit override wins", func(t *testing.T) {
		useRestaurantEmail(t, "resto@tsb.test")
		t.Setenv("FEEDBACK_RECIPIENT_EMAIL", " owner@tsb.test ")
		require.Equal(t, "owner@tsb.test", FeedbackRecipient())
	})

	t.Run("without an override the restaurant's own address is used", func(t *testing.T) {
		useRestaurantEmail(t, "resto@ygf.test")
		t.Setenv("FEEDBACK_RECIPIENT_EMAIL", "")
		require.Equal(t, "resto@ygf.test", FeedbackRecipient())
	})

	t.Run("a blank restaurant address leaves no recipient", func(t *testing.T) {
		useRestaurantEmail(t, "   ")
		t.Setenv("FEEDBACK_RECIPIENT_EMAIL", "")
		require.Empty(t, FeedbackRecipient())
	})
}

func TestFeedbackEmailGoesToTheRestaurantByDefault(t *testing.T) {
	srv := smtptest.Start(t)
	useSMTP(t, srv)
	useRestaurantEmail(t, "resto@ygf.test")
	t.Setenv("FEEDBACK_RECIPIENT_EMAIL", "")

	require.NoError(t, SendFeedbackEmail("Lin", "lin@example.com", "takeaway", "compliment", "Great broth, thanks!", "fr"))
	require.Equal(t, []string{"resto@ygf.test"}, srv.Messages()[0].To)
}

func TestFeedbackEmailWithoutRecipientSendsNothing(t *testing.T) {
	srv := smtptest.Start(t)
	useSMTP(t, srv)
	useRestaurantEmail(t, " ")
	t.Setenv("FEEDBACK_RECIPIENT_EMAIL", "")

	err := SendFeedbackEmail("Lin", "lin@example.com", "takeaway", "compliment", "Great broth, thanks!", "fr")
	require.ErrorIs(t, err, ErrNoFeedbackRecipient)
	require.Empty(t, srv.Messages())
}

func TestFeedbackEmailViaTEM(t *testing.T) {
	t.Setenv("FEEDBACK_RECIPIENT_EMAIL", "owner@tsb.test")
	headersOf := func(t *testing.T, body []byte) map[string]string {
		t.Helper()
		var req struct {
			AdditionalHeaders []struct{ Key, Value string } `json:"additional_headers"`
		}
		require.NoError(t, json.Unmarshal(body, &req))
		out := map[string]string{}
		for _, h := range req.AdditionalHeaders {
			out[h.Key] = h.Value
		}
		return out
	}

	t.Run("carries the customer's address as Reply-To", func(t *testing.T) {
		f := startFakeTEM(t, temOK)
		require.NoError(t, SendFeedbackEmail("Lin", "lin@example.com", "takeaway", "compliment", "Great broth, thanks!", "fr"))
		require.Len(t, f.requests(), 1)
		require.Equal(t, "lin@example.com", headersOf(t, f.requests()[0].Body)["Reply-To"])
	})

	t.Run("a refused header is dropped and the feedback still goes out", func(t *testing.T) {
		f := startFakeTEM(t, func(w http.ResponseWriter, r *http.Request, n int) {
			if n == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"type":"invalid_arguments","message":"invalid argument(s)","details":[{"argument_name":"additional_headers","reason":"constraint","help_message":"header not allowed"}]}`))
				return
			}
			temOK(w, r, n)
		})
		require.NoError(t, SendFeedbackEmail("Lin", "lin@example.com", "takeaway", "compliment", "Great broth, thanks!", "fr"))
		reqs := f.requests()
		require.Len(t, reqs, 2)
		require.NotContains(t, headersOf(t, reqs[1].Body), "Reply-To")
	})

	t.Run("a refused recipient is not retried", func(t *testing.T) {
		f := startFakeTEM(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"invalid_arguments","message":"invalid argument(s)","details":[{"argument_name":"rcpt","reason":"format","help_message":"bad"}]}`))
		})
		err := SendFeedbackEmail("Lin", "lin@example.com", "takeaway", "compliment", "Great broth, thanks!", "fr")
		require.ErrorIs(t, err, ErrInvalidRecipient)
		require.Len(t, f.requests(), 1)
	})
}
