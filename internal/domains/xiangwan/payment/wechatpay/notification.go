package wechatpay

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	wechatpaynotify "github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	wechatpayments "github.com/wechatpay-apiv3/wechatpay-go/services/payments"
)

var (
	ErrInvalidNotificationConfig = errors.New("invalid xiangwan WeChat Pay notification config")
	ErrInvalidNotification       = errors.New("invalid xiangwan WeChat Pay notification")
)

type NotificationConfig struct {
	AppID                string
	MerchantID           string
	APIv3Key             string
	WeChatPayPublicKeyID string
	WeChatPayPublicKey   *rsa.PublicKey
}

// NotificationDecoder verifies the raw request signature before the official
// SDK decrypts its AES-256-GCM resource. It returns only a normalized payment
// fact and a digest; raw provider payloads never cross this boundary.
type NotificationDecoder struct {
	appID      string
	merchantID string
	handler    *wechatpaynotify.Handler
}

func NewNotificationDecoder(
	config NotificationConfig,
) (*NotificationDecoder, error) {
	if !validNotificationConfig(config) {
		return nil, ErrInvalidNotificationConfig
	}
	verifier := verifiers.NewSHA256WithRSAPubkeyVerifier(
		config.WeChatPayPublicKeyID,
		*config.WeChatPayPublicKey,
	)
	handler, err := wechatpaynotify.NewRSANotifyHandler(config.APIv3Key, verifier)
	if err != nil {
		return nil, ErrInvalidNotificationConfig
	}
	return &NotificationDecoder{
		appID:      config.AppID,
		merchantID: config.MerchantID,
		handler:    handler,
	}, nil
}

func (decoder *NotificationDecoder) Decode(
	ctx context.Context,
	request *http.Request,
) (payment.VerifiedPaymentNotification, error) {
	if decoder == nil || decoder.handler == nil || ctx == nil ||
		request == nil || request.Body == nil {
		return payment.VerifiedPaymentNotification{}, ErrInvalidNotification
	}
	rawBody, err := io.ReadAll(request.Body)
	if err != nil || len(rawBody) == 0 {
		return payment.VerifiedPaymentNotification{}, ErrInvalidNotification
	}
	_ = request.Body.Close()
	request.Body = io.NopCloser(bytes.NewReader(rawBody))

	// The upstream SDK dereferences resource while parsing. Check only the
	// structural prerequisite here; authenticity is established immediately
	// afterwards over the untouched raw bytes.
	var shape struct {
		Resource *json.RawMessage `json:"resource"`
	}
	if json.Unmarshal(rawBody, &shape) != nil || shape.Resource == nil ||
		len(*shape.Resource) == 0 || bytes.Equal(*shape.Resource, []byte("null")) {
		return payment.VerifiedPaymentNotification{}, ErrInvalidNotification
	}

	var transaction wechatpayments.Transaction
	envelope, err := decoder.handler.ParseNotifyRequest(ctx, request, &transaction)
	if err != nil || envelope == nil || envelope.Resource == nil ||
		envelope.CreateTime == nil {
		return payment.VerifiedPaymentNotification{}, ErrInvalidNotification
	}
	result, err := projectPaymentQueryResult(&transaction, nil)
	if err != nil || result.AppID != decoder.appID ||
		result.MerchantID != decoder.merchantID {
		return payment.VerifiedPaymentNotification{}, ErrInvalidNotification
	}
	digest := sha256.Sum256(rawBody)
	notification := payment.VerifiedPaymentNotification{
		NotificationID: envelope.ID,
		CreatedAt:      envelope.CreateTime.UTC(),
		EventType:      envelope.EventType,
		ResourceType:   envelope.ResourceType,
		OriginalType:   envelope.Resource.OriginalType,
		PayloadDigest:  hex.EncodeToString(digest[:]),
		Transaction:    result,
	}
	if payment.ValidateVerifiedPaymentNotification(notification) != nil {
		return payment.VerifiedPaymentNotification{}, ErrInvalidNotification
	}
	return notification, nil
}

func validNotificationConfig(config NotificationConfig) bool {
	return config.AppID != "" && config.AppID == strings.TrimSpace(config.AppID) &&
		len([]rune(config.AppID)) <= 64 &&
		merchantIDPattern.MatchString(config.MerchantID) &&
		len([]byte(config.APIv3Key)) == 32 &&
		config.APIv3Key == strings.TrimSpace(config.APIv3Key) &&
		!strings.ContainsAny(config.APIv3Key, "\r\n\x00") &&
		publicKeyIDPattern.MatchString(config.WeChatPayPublicKeyID) &&
		config.WeChatPayPublicKey != nil && config.WeChatPayPublicKey.N != nil &&
		config.WeChatPayPublicKey.N.Sign() > 0 && config.WeChatPayPublicKey.E > 0
}
