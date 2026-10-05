package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPaymentCloseJobLeaseIsDueAndOwnerFenced(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 1, 2, 3, 0, time.UTC)
	pending := pendingPaymentCloseJob(now)
	generationID := uuid.New()
	ownerToken := uuid.New()
	acquired, err := AcquirePaymentCloseJob(
		pending,
		generationID,
		ownerToken,
		now,
	)
	if err != nil || acquired.JobStatus != PaymentCloseJobStatusInProgress ||
		acquired.GenerationID == nil || *acquired.GenerationID != generationID ||
		acquired.OwnerToken == nil || *acquired.OwnerToken != ownerToken ||
		acquired.LeaseExpiresAt == nil ||
		!acquired.LeaseExpiresAt.Equal(now.Add(PaymentCloseJobLeaseDuration)) ||
		acquired.AttemptCount != 1 || acquired.Version != pending.Version+1 {
		t.Fatalf("AcquirePaymentCloseJob() = %+v, %v", acquired, err)
	}
	if _, err := RetryPaymentCloseJob(
		acquired,
		uuid.New(),
		PaymentCloseJobCompletion{Failure: context.DeadlineExceeded},
		now.Add(time.Second),
	); !errors.Is(err, ErrPaymentCloseJobLeaseLost) {
		t.Fatalf("RetryPaymentCloseJob(wrong owner) error = %v", err)
	}
	if _, err := AcquirePaymentCloseJob(
		acquired,
		generationID,
		uuid.New(),
		now.Add(PaymentCloseJobLeaseDuration-time.Nanosecond),
	); !errors.Is(err, ErrPaymentCloseJobNotDue) {
		t.Fatalf("AcquirePaymentCloseJob(active lease) error = %v", err)
	}
}

func TestPaymentCloseJobRetryRecordsOnlySafeProviderFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 2, 3, 4, 0, time.UTC)
	ownerToken := uuid.New()
	acquired := acquiredPaymentCloseJob(t, now, ownerToken)
	state := ProviderTradeStateUserPaying
	retried, err := RetryPaymentCloseJob(
		acquired,
		ownerToken,
		PaymentCloseJobCompletion{
			TradeState:        &state,
			ProviderRequestID: "query-request-1",
		},
		now.Add(time.Second),
	)
	if err != nil || retried.JobStatus != PaymentCloseJobStatusRetry ||
		retried.OwnerToken != nil || retried.LeaseExpiresAt != nil ||
		retried.NextAttemptAt == nil ||
		!retried.NextAttemptAt.Equal(now.Add(time.Second+PaymentCloseJobRetryDelay)) ||
		retried.LastTradeState == nil ||
		*retried.LastTradeState != ProviderTradeStateUserPaying ||
		retried.Resolution != PaymentCloseResolutionNone {
		t.Fatalf("RetryPaymentCloseJob() = %+v, %v", retried, err)
	}
	if validatePaymentCloseJob(retried) != nil {
		t.Fatalf("retry did not remain a valid durable job: %+v", retried)
	}
}

func TestPaymentCloseJobTerminalResolutionShapes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name       string
		completion PaymentCloseJobCompletion
	}{
		{
			name: "provider closed",
			completion: PaymentCloseJobCompletion{
				Resolution: PaymentCloseResolutionProviderClosed,
				TradeState: providerTradeStatePointer(ProviderTradeStateNotPay),
			},
		},
		{
			name: "provider terminal",
			completion: PaymentCloseJobCompletion{
				Resolution: PaymentCloseResolutionProviderTerminal,
				TradeState: providerTradeStatePointer(ProviderTradeStateRevoked),
			},
		},
		{
			name: "provider absent",
			completion: PaymentCloseJobCompletion{
				Resolution: PaymentCloseResolutionProviderAbsent,
				Failure: NewProviderFailure(
					ProviderFailureRejected,
					"ORDER_NOT_EXIST",
					context.Canceled,
				),
			},
		},
		{
			name: "payment converged from provider",
			completion: PaymentCloseJobCompletion{
				Resolution: PaymentCloseResolutionPaymentConverged,
				TradeState: providerTradeStatePointer(ProviderTradeStateSuccess),
			},
		},
		{
			name: "payment converged locally",
			completion: PaymentCloseJobCompletion{
				Resolution: PaymentCloseResolutionPaymentConverged,
			},
		},
		{
			name: "manual review",
			completion: PaymentCloseJobCompletion{
				Resolution: PaymentCloseResolutionManualReview,
				TradeState: providerTradeStatePointer(ProviderTradeStateRefund),
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ownerToken := uuid.New()
			acquired := acquiredPaymentCloseJob(t, now, ownerToken)
			completed, err := CompletePaymentCloseJob(
				acquired,
				ownerToken,
				test.completion,
				now.Add(time.Second),
			)
			if err != nil || completed.JobStatus != PaymentCloseJobStatusCompleted ||
				completed.Resolution != test.completion.Resolution ||
				completed.CompletedAt == nil || completed.OwnerToken != nil ||
				completed.LeaseExpiresAt != nil ||
				validatePaymentCloseJob(completed) != nil {
				t.Fatalf("CompletePaymentCloseJob() = %+v, %v", completed, err)
			}
			if _, err := AcquirePaymentCloseJob(
				completed,
				uuid.New(),
				uuid.New(),
				now.Add(2*time.Second),
			); !errors.Is(err, ErrPaymentCloseJobTerminal) {
				t.Fatalf("AcquirePaymentCloseJob(completed) error = %v", err)
			}
		})
	}
}

func TestPaymentCloseJobRejectsContradictoryTerminalFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 4, 5, 6, 0, time.UTC)
	ownerToken := uuid.New()
	acquired := acquiredPaymentCloseJob(t, now, ownerToken)
	_, err := CompletePaymentCloseJob(
		acquired,
		ownerToken,
		PaymentCloseJobCompletion{
			Resolution: PaymentCloseResolutionProviderClosed,
			TradeState: providerTradeStatePointer(ProviderTradeStateSuccess),
		},
		now.Add(time.Second),
	)
	if !errors.Is(err, ErrInvalidPaymentCloseJob) {
		t.Fatalf("CompletePaymentCloseJob(contradiction) error = %v", err)
	}
}

func pendingPaymentCloseJob(now time.Time) PaymentCloseJob {
	createdAt := now.Add(-time.Minute)
	nextAttemptAt := createdAt
	return PaymentCloseJob{
		ID:                uuid.New(),
		TenantID:          uuid.New(),
		OrderID:           uuid.New(),
		PrincipalID:       uuid.New(),
		PaymentAppID:      "wx-xiangwan-app",
		PaymentMerchantID: "1900000109",
		OutTradeNo:        "XW-CLOSE-ORDER-0001",
		AmountCents:       9_900,
		JobStatus:         PaymentCloseJobStatusPending,
		NextAttemptAt:     &nextAttemptAt,
		Version:           1,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
}

func acquiredPaymentCloseJob(
	t *testing.T,
	now time.Time,
	ownerToken uuid.UUID,
) PaymentCloseJob {
	t.Helper()
	acquired, err := AcquirePaymentCloseJob(
		pendingPaymentCloseJob(now),
		uuid.New(),
		ownerToken,
		now,
	)
	if err != nil {
		t.Fatalf("AcquirePaymentCloseJob() error = %v", err)
	}
	return acquired
}

func providerTradeStatePointer(value ProviderTradeState) *ProviderTradeState {
	return &value
}
