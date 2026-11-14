package feedback

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"go.uber.org/zap"

	"tsb-service/pkg/email/scaleway"
	"tsb-service/pkg/logging"
	"tsb-service/pkg/utils"
)

type FeedbackRequest struct {
	Name           string `json:"name" binding:"required,max=100"`
	Email          string `json:"email" binding:"required,email,max=255"`
	ServiceType    string `json:"serviceType" binding:"required,oneof=takeaway dine-in delivery"`
	FeedbackType   string `json:"feedbackType" binding:"required,oneof=improvement complaint compliment"`
	Message        string `json:"message" binding:"required,min=10,max=2000"`
	Website        string `json:"website"`        // honeypot: should always be empty
	TurnstileToken string `json:"turnstileToken"` // Cloudflare Turnstile token
}

type turnstileResponse struct {
	Success bool `json:"success"`
}

func verifyTurnstile(token, remoteIP string) bool {
	secret := os.Getenv("TURNSTILE_SECRET_KEY")
	if secret == "" {
		// Turnstile not configured: skip verification
		return true
	}

	resp, err := http.PostForm("https://challenges.cloudflare.com/turnstile/v0/siteverify", url.Values{
		"secret":   {secret},
		"response": {token},
		"remoteip": {remoteIP},
	})
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	var result turnstileResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false
	}
	return result.Success
}

// jsonFieldNames maps the request's Go fields to the JSON keys the form sends, so the client can
// mark the right input when the server refuses a value it accepted (an email such as "a@b").
var jsonFieldNames = map[string]string{
	"Name":         "name",
	"Email":        "email",
	"ServiceType":  "serviceType",
	"FeedbackType": "feedbackType",
	"Message":      "message",
}

// invalidFields lists the JSON names of the fields that failed validation, in struct order.
func invalidFields(err error) []string {
	fields := []string{}
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return fields
	}
	for _, fe := range verrs {
		if name, ok := jsonFieldNames[fe.StructField()]; ok && !slices.Contains(fields, name) {
			fields = append(fields, name)
		}
	}
	return fields
}

func HandleFeedback(c *gin.Context) {
	log := logging.FromContext(c.Request.Context())

	// Decode first and validate after trimming, so whitespace-only values fail "required" and
	// "min" instead of slipping through and arriving empty in the owner's inbox.
	var req FeedbackRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_input"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	req.Message = strings.TrimSpace(req.Message)

	if err := binding.Validator.ValidateStruct(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_input", "fields": invalidFields(err)})
		return
	}

	// Honeypot check: if the hidden field is filled, it's a bot.
	// Return 200 to not tip off the bot.
	if req.Website != "" {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}

	// Verify Turnstile token
	if !verifyTurnstile(req.TurnstileToken, c.ClientIP()) {
		log.Warn("turnstile verification failed", zap.String("client_ip", c.ClientIP()))
		c.JSON(http.StatusBadRequest, gin.H{"error": "captcha_failed"})
		return
	}

	// Extract language from context
	lang := utils.GetLang(c.Request.Context())

	if err := scaleway.SendFeedbackEmail(req.Name, req.Email, req.ServiceType, req.FeedbackType, req.Message, lang); err != nil {
		log.Error("failed to send feedback email", zap.Error(err), zap.Bool("recipient_set", scaleway.FeedbackRecipient() != ""))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "email_failed"})
		return
	}

	log.Info("feedback submitted",
		zap.String("service_type", req.ServiceType),
		zap.String("feedback_type", req.FeedbackType),
	)

	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
