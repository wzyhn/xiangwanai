package xiangwanapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/gin-gonic/gin"
)

const (
	maxWeChatPaymentNotificationBodyBytes = 64 * 1024
	weChatPaySignatureTypeRSA             = "WECHATPAY2-SHA256-RSA2048"
)

type weChatPaymentNotificationDecoder interface {
	Decode(
		context.Context,
		*http.Request,
	) (payment.VerifiedPaymentNotification, error)
}

type weChatPaymentNotificationApplication interface {
	Process(
		context.Context,
		payment.VerifiedPaymentNotification,
	) (payment.PaymentConvergence, error)
}

type WeChatPaymentNotificationHandler struct {
	decoder weChatPaymentNotificationDecoder
	service weChatPaymentNotificationApplication
}

func NewWeChatPaymentNotificationHandler(
	decoder weChatPaymentNotificationDecoder,
	service weChatPaymentNotificationApplication,
) *WeChatPaymentNotificationHandler {
	return &WeChatPaymentNotificationHandler{decoder: decoder, service: service}
}

func (handler *WeChatPaymentNotificationHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.POST(
		"/integrations/wechat-pay/notifications",
		handler.Receive,
	)
}

// Receive godoc
// @Summary Receive one authenticated WeChat Pay success notification
// @Description PROVIDER-KEY command. The raw bounded body is verified with the configured WeChat Pay public key before AES-256-GCM decryption. Only exact server-owned AppID, merchant, merchant-order, amount, currency, JSAPI, and SUCCESS facts enter the same PostgreSQL transaction as payment and participation convergence. Provider notification ID plus payload digest make replay durable without Redis.
// @Tags xiangwan
// @Accept json
// @Param Wechatpay-Serial header string true "WeChat Pay public-key ID"
// @Param Wechatpay-Signature header string true "WeChat Pay signature"
// @Param Wechatpay-Timestamp header string true "Signature timestamp"
// @Param Wechatpay-Nonce header string true "Signature nonce"
// @Success 204
// @Failure 400 {object} WeChatPaymentNotificationFailure
// @Failure 409 {object} WeChatPaymentNotificationFailure
// @Failure 413 {object} WeChatPaymentNotificationFailure
// @Failure 500 {object} WeChatPaymentNotificationFailure
// @Failure 503 {object} WeChatPaymentNotificationFailure
// @Router /xiangwan/integrations/wechat-pay/notifications [post]
func (handler *WeChatPaymentNotificationHandler) Receive(c *gin.Context) {
	if handler == nil || handler.decoder == nil || handler.service == nil {
		writeWeChatPaymentNotificationFailure(
			c,
			http.StatusServiceUnavailable,
			"temporary failure",
		)
		return
	}
	if len(c.Request.URL.Query()) != 0 ||
		len(c.Request.Header.Values("Content-Type")) != 1 ||
		c.ContentType() != "application/json" ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 ||
		!validWeChatPaymentNotificationHeaders(c.Request.Header) {
		writeWeChatPaymentNotificationFailure(c, http.StatusBadRequest, "invalid notification")
		return
	}

	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxWeChatPaymentNotificationBodyBytes,
	)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeWeChatPaymentNotificationFailure(
				c,
				http.StatusRequestEntityTooLarge,
				"notification is too large",
			)
			return
		}
		writeWeChatPaymentNotificationFailure(c, http.StatusBadRequest, "invalid notification")
		return
	}
	_ = c.Request.Body.Close()
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	notification, err := handler.decoder.Decode(c.Request.Context(), c.Request)
	if err != nil {
		writeWeChatPaymentNotificationFailure(c, http.StatusBadRequest, "invalid notification")
		return
	}
	if _, err := handler.service.Process(c.Request.Context(), notification); err != nil {
		writeWeChatPaymentNotificationProcessingError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type WeChatPaymentNotificationFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func validWeChatPaymentNotificationHeaders(header http.Header) bool {
	required := map[string]int{
		"Wechatpay-Serial":    128,
		"Wechatpay-Signature": 1024,
		"Wechatpay-Timestamp": 20,
		"Wechatpay-Nonce":     128,
	}
	for name, maximum := range required {
		values := header.Values(name)
		if len(values) != 1 || values[0] == "" ||
			values[0] != strings.TrimSpace(values[0]) ||
			len([]rune(values[0])) > maximum ||
			strings.ContainsAny(values[0], "\r\n\x00") {
			return false
		}
	}
	signatureTypes := header.Values("Wechatpay-Signature-Type")
	return len(signatureTypes) == 0 ||
		(len(signatureTypes) == 1 && signatureTypes[0] == weChatPaySignatureTypeRSA)
}

func writeWeChatPaymentNotificationProcessingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidWeChatPaymentNotification),
		errors.Is(err, payment.ErrInvalidPaymentNotification):
		writeWeChatPaymentNotificationFailure(c, http.StatusBadRequest, "invalid notification")
	case errors.Is(err, paymentpostgres.ErrPaymentConfirmationConflict):
		writeWeChatPaymentNotificationFailure(c, http.StatusConflict, "notification conflict")
	case errors.Is(err, ErrWeChatPaymentNotificationDisabled):
		writeWeChatPaymentNotificationFailure(c, http.StatusServiceUnavailable, "temporary failure")
	case errors.Is(err, paymentpostgres.ErrPaymentMerchantConfigUnavailable):
		_ = c.Error(err)
		writeWeChatPaymentNotificationFailure(c, http.StatusServiceUnavailable, "temporary failure")
	default:
		_ = c.Error(err)
		writeWeChatPaymentNotificationFailure(c, http.StatusInternalServerError, "temporary failure")
	}
}

func writeWeChatPaymentNotificationFailure(
	c *gin.Context,
	status int,
	message string,
) {
	c.PureJSON(status, WeChatPaymentNotificationFailure{
		Code:    "FAIL",
		Message: message,
	})
}
