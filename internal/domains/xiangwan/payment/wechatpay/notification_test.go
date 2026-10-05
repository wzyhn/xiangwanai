package wechatpay

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
)

const (
	testNotificationAPIv3Key   = "synthetic-api-v3-key-for-tests!!"
	testNotificationPublicID   = "PUB_KEY_ID_SYNTHETIC_TEST"
	testNotificationAppID      = "wx1234567890abcdef"
	testNotificationMerchant   = "1900000109"
	testNotificationOutTrade   = "XIANGWAN-ORDER-01"
	testNotificationProviderID = "wechat-transaction-01"
)

func TestNotificationDecoderVerifiesDecryptsAndProjectsSuccess(t *testing.T) {
	t.Parallel()

	privateKey := mustNotificationRSAKey(t)
	decoder := mustNotificationDecoder(t, privateKey, testNotificationAPIv3Key)
	now := time.Now().UTC().Truncate(time.Second)
	request, rawBody := signedNotificationRequest(
		t,
		privateKey,
		testNotificationAPIv3Key,
		now,
		payment.PaymentNotificationEventTransactionSuccess,
		testNotificationAppID,
	)
	notification, err := decoder.Decode(context.Background(), request)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	digest := sha256.Sum256(rawBody)
	if notification.NotificationID != "notify-payment-success-01" ||
		notification.EventType != payment.PaymentNotificationEventTransactionSuccess ||
		notification.ResourceType != payment.PaymentNotificationResourceTransaction ||
		notification.OriginalType != payment.PaymentNotificationOriginalTypeTransaction ||
		notification.PayloadDigest != hex.EncodeToString(digest[:]) ||
		notification.Transaction.AppID != testNotificationAppID ||
		notification.Transaction.MerchantID != testNotificationMerchant ||
		notification.Transaction.OutTradeNo != testNotificationOutTrade ||
		notification.Transaction.TransactionID != testNotificationProviderID ||
		notification.Transaction.TradeState != payment.ProviderTradeStateSuccess ||
		notification.Transaction.AmountCents != 9_900 ||
		notification.Transaction.Currency != payment.PaymentQueryCurrency ||
		payment.ValidateVerifiedPaymentNotification(notification) != nil {
		t.Fatalf("decoded notification = %+v", notification)
	}
}

func TestNotificationDecoderRejectsTamperingWrongIdentityAndWrongKey(t *testing.T) {
	t.Parallel()

	privateKey := mustNotificationRSAKey(t)
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name       string
		apiKey     string
		eventType  string
		appID      string
		tamperBody bool
	}{
		{name: "signature tampering", apiKey: testNotificationAPIv3Key, eventType: payment.PaymentNotificationEventTransactionSuccess, appID: testNotificationAppID, tamperBody: true},
		{name: "wrong app identity", apiKey: testNotificationAPIv3Key, eventType: payment.PaymentNotificationEventTransactionSuccess, appID: "wxother1234567890"},
		{name: "wrong event", apiKey: testNotificationAPIv3Key, eventType: "TRANSACTION.CLOSED", appID: testNotificationAppID},
		{name: "wrong decryption key", apiKey: "different-api-v3-key-for-tests!!", eventType: payment.PaymentNotificationEventTransactionSuccess, appID: testNotificationAppID},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			decoder := mustNotificationDecoder(t, privateKey, test.apiKey)
			request, rawBody := signedNotificationRequest(
				t,
				privateKey,
				testNotificationAPIv3Key,
				now,
				test.eventType,
				test.appID,
			)
			if test.tamperBody {
				request.Body = http.NoBody
				request.Body = io.NopCloser(strings.NewReader(string(rawBody) + " "))
			}
			if _, err := decoder.Decode(context.Background(), request); !errors.Is(
				err,
				ErrInvalidNotification,
			) {
				t.Fatalf("Decode(invalid) error = %v", err)
			}
		})
	}
}

func TestNotificationDecoderRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	privateKey := mustNotificationRSAKey(t)
	config := NotificationConfig{
		AppID:                testNotificationAppID,
		MerchantID:           testNotificationMerchant,
		APIv3Key:             "too-short",
		WeChatPayPublicKeyID: testNotificationPublicID,
		WeChatPayPublicKey:   &privateKey.PublicKey,
	}
	if _, err := NewNotificationDecoder(config); !errors.Is(err, ErrInvalidNotificationConfig) {
		t.Fatalf("NewNotificationDecoder(invalid) error = %v", err)
	}
}

func mustNotificationDecoder(
	t *testing.T,
	privateKey *rsa.PrivateKey,
	apiKey string,
) *NotificationDecoder {
	t.Helper()
	decoder, err := NewNotificationDecoder(NotificationConfig{
		AppID:                testNotificationAppID,
		MerchantID:           testNotificationMerchant,
		APIv3Key:             apiKey,
		WeChatPayPublicKeyID: testNotificationPublicID,
		WeChatPayPublicKey:   &privateKey.PublicKey,
	})
	if err != nil {
		t.Fatalf("NewNotificationDecoder() error = %v", err)
	}
	return decoder
}

func mustNotificationRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	return key
}

func signedNotificationRequest(
	t *testing.T,
	privateKey *rsa.PrivateKey,
	apiKey string,
	now time.Time,
	eventType string,
	appID string,
) (*http.Request, []byte) {
	t.Helper()
	successAt := now.Add(-2 * time.Minute)
	plaintext, err := json.Marshal(map[string]any{
		"appid":          appID,
		"mchid":          testNotificationMerchant,
		"out_trade_no":   testNotificationOutTrade,
		"transaction_id": testNotificationProviderID,
		"trade_type":     payment.PaymentQueryTradeType,
		"trade_state":    payment.ProviderTradeStateSuccess,
		"success_time":   successAt.Format(time.RFC3339),
		"amount": map[string]any{
			"total":    int64(9_900),
			"currency": payment.PaymentQueryCurrency,
		},
	})
	if err != nil {
		t.Fatalf("marshal transaction: %v", err)
	}
	ciphertext := encryptNotificationResource(t, apiKey, plaintext)
	body, err := json.Marshal(map[string]any{
		"id":            "notify-payment-success-01",
		"create_time":   now.Add(-time.Minute).Format(time.RFC3339),
		"event_type":    eventType,
		"resource_type": payment.PaymentNotificationResourceTransaction,
		"summary":       "payment succeeded",
		"resource": map[string]any{
			"algorithm":       "AEAD_AES_256_GCM",
			"ciphertext":      ciphertext,
			"associated_data": "transaction",
			"nonce":           "notify-nonce",
			"original_type":   payment.PaymentNotificationOriginalTypeTransaction,
		},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	headerNonce := "header-nonce-01"
	message := fmt.Sprintf("%d\n%s\n%s\n", now.Unix(), headerNonce, body)
	hashed := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("sign notification: %v", err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"https://xiangwan.example.com/api/v1/xiangwan/integrations/wechat-pay/notifications",
		strings.NewReader(string(body)),
	)
	request.Header.Set("Wechatpay-Serial", testNotificationPublicID)
	request.Header.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(signature))
	request.Header.Set("Wechatpay-Timestamp", fmt.Sprintf("%d", now.Unix()))
	request.Header.Set("Wechatpay-Nonce", headerNonce)
	return request, body
}

func encryptNotificationResource(t *testing.T, apiKey string, plaintext []byte) string {
	t.Helper()
	block, err := aes.NewCipher([]byte(apiKey))
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	ciphertext := aead.Seal(
		nil,
		[]byte("notify-nonce"),
		plaintext,
		[]byte("transaction"),
	)
	return base64.StdEncoding.EncodeToString(ciphertext)
}
