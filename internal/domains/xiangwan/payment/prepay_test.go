package payment

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPaymentAttemptLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	command := validCreatePrepayAttemptCommand()
	fingerprint, err := PrepayRequestFingerprint(command)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	owner := uuid.New()
	attempt, err := NewPaymentAttempt(NewPaymentAttemptCommand{
		ID:                 uuid.New(),
		TenantID:           command.TenantID,
		OrderID:            command.OrderID,
		PrincipalID:        command.PrincipalID,
		GenerationID:       command.GenerationID,
		IdempotencyKey:     command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		OutTradeNo:         "XW-PREPAY-ORDER-00000001",
		PaymentAppID:       "wxXiangwan123",
		PaymentMerchantID:  "1900000109",
		Description:        "享玩活动报名",
		NotifyURL:          "https://xiangwan.example.com/notify",
		OrderVersion:       command.ExpectedOrderVersion,
		AmountCents:        command.ExpectedPayableCents,
		OwnerToken:         owner,
		Now:                now,
	})
	if err != nil {
		t.Fatalf("new attempt: %v", err)
	}
	if attempt.AttemptStatus != PrepayAttemptStatusInProgress ||
		attempt.LeaseExpiresAt == nil ||
		!attempt.LeaseExpiresAt.Equal(now.Add(PrepayLeaseDuration)) {
		t.Fatalf("unexpected attempt: %+v", attempt)
	}

	providerResult := validProviderPrepayResult()
	completed, err := CompletePaymentAttemptReady(
		attempt,
		owner,
		providerResult,
		now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("complete ready: %v", err)
	}
	parameters, available := completed.PaymentParameters()
	if !available || parameters.Package != providerResult.Parameters.Package ||
		completed.PrepayID == nil || *completed.PrepayID != providerResult.PrepayID {
		t.Fatalf("unexpected ready attempt: %+v", completed)
	}
	if _, err := TakeOverPaymentAttempt(
		completed,
		uuid.New(),
		uuid.New(),
		now.Add(time.Minute),
	); !errors.Is(err, ErrPrepayAttemptTerminal) {
		t.Fatalf("expected terminal attempt, got %v", err)
	}
}

func TestPaymentAttemptTakeoverAndUnknownOutcome(t *testing.T) {
	now := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	attempt, owner := newValidPaymentAttempt(t, now)
	if _, err := TakeOverPaymentAttempt(
		attempt,
		uuid.New(),
		uuid.New(),
		now.Add(PrepayLeaseDuration-time.Nanosecond),
	); !errors.Is(err, ErrPrepayAttemptLeaseActive) {
		t.Fatalf("expected active lease, got %v", err)
	}

	newOwner := uuid.New()
	takenOver, err := TakeOverPaymentAttempt(
		attempt,
		uuid.New(),
		newOwner,
		now.Add(PrepayLeaseDuration),
	)
	if err != nil {
		t.Fatalf("take over: %v", err)
	}
	if takenOver.OwnerToken == nil || *takenOver.OwnerToken != newOwner ||
		takenOver.Version != attempt.Version+1 {
		t.Fatalf("unexpected takeover: %+v", takenOver)
	}
	if _, err := CompletePaymentAttemptUnknown(
		takenOver,
		owner,
		ProviderFailureTimeout,
		"request-1",
		now.Add(PrepayLeaseDuration+time.Second),
	); !errors.Is(err, ErrPrepayAttemptLeaseLost) {
		t.Fatalf("expected lease loss, got %v", err)
	}
	unknown, err := CompletePaymentAttemptUnknown(
		takenOver,
		newOwner,
		ProviderFailureTimeout,
		"request-1",
		now.Add(PrepayLeaseDuration+time.Second),
	)
	if err != nil {
		t.Fatalf("complete unknown: %v", err)
	}
	if unknown.AttemptStatus != PrepayAttemptStatusUnknown ||
		unknown.LastErrorClass == nil ||
		*unknown.LastErrorClass != ProviderFailureTimeout ||
		unknown.OwnerToken != nil || unknown.LeaseExpiresAt != nil {
		t.Fatalf("unexpected unknown attempt: %+v", unknown)
	}
}

func TestPrepayRequestFingerprintBindsConfirmationFacts(t *testing.T) {
	command := validCreatePrepayAttemptCommand()
	first, err := PrepayRequestFingerprint(command)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	second, _ := PrepayRequestFingerprint(command)
	if first != second || len(first) != 64 {
		t.Fatalf("fingerprint is not stable: %q %q", first, second)
	}
	command.ExpectedPayableCents++
	changed, _ := PrepayRequestFingerprint(command)
	if changed == first {
		t.Fatal("amount change must change fingerprint")
	}
}

func TestProviderFailureMetadataFailsClosed(t *testing.T) {
	class, code := ProviderFailureMetadata(errors.New("network"))
	if class != ProviderFailureAmbiguous || code != "" {
		t.Fatalf("unexpected generic metadata: %q %q", class, code)
	}
	class, code = ProviderFailureMetadata(NewProviderFailure(
		ProviderFailureRejected,
		"APPID_MCHID_NOT_MATCH",
		errors.New("provider detail"),
	))
	if class != ProviderFailureRejected || code != "APPID_MCHID_NOT_MATCH" {
		t.Fatalf("unexpected provider metadata: %q %q", class, code)
	}
}

func newValidPaymentAttempt(t *testing.T, now time.Time) (PaymentAttempt, uuid.UUID) {
	t.Helper()
	command := validCreatePrepayAttemptCommand()
	fingerprint, _ := PrepayRequestFingerprint(command)
	owner := uuid.New()
	attempt, err := NewPaymentAttempt(NewPaymentAttemptCommand{
		ID:                 uuid.New(),
		TenantID:           command.TenantID,
		OrderID:            command.OrderID,
		PrincipalID:        command.PrincipalID,
		GenerationID:       command.GenerationID,
		IdempotencyKey:     command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		OutTradeNo:         "XW-PREPAY-ORDER-00000001",
		PaymentAppID:       "wxXiangwan123",
		PaymentMerchantID:  "1900000109",
		Description:        "享玩活动报名",
		NotifyURL:          "https://xiangwan.example.com/notify",
		OrderVersion:       command.ExpectedOrderVersion,
		AmountCents:        command.ExpectedPayableCents,
		OwnerToken:         owner,
		Now:                now,
	})
	if err != nil {
		t.Fatalf("new attempt: %v", err)
	}
	return attempt, owner
}

func validCreatePrepayAttemptCommand() CreatePrepayAttemptCommand {
	return CreatePrepayAttemptCommand{
		TenantID:             uuid.New(),
		GenerationID:         uuid.New(),
		OrderID:              uuid.New(),
		PrincipalID:          uuid.New(),
		IdempotencyKey:       uuid.New(),
		ExpectedOrderVersion: 1,
		ExpectedPayableCents: 19900,
	}
}

func validProviderPrepayResult() ProviderPrepayResult {
	return ProviderPrepayResult{
		PrepayID:          "wx201410272009395522657a690389285100",
		ProviderRequestID: "request-1",
		Parameters: MiniProgramPaymentParameters{
			AppID:     "wxXiangwan123",
			TimeStamp: "1789290000",
			NonceStr:  "nonce-1",
			Package:   "prepay_id=wx201410272009395522657a690389285100",
			SignType:  "RSA",
			PaySign:   "signed-payment-parameters",
		},
	}
}
