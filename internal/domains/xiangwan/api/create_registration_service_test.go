package xiangwanapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

func TestCreateRegistrationServiceBuildsOwnerBoundFreeCommand(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	idempotencyKey := uuid.New()
	questionnaireID := uuid.New()
	fieldID := uuid.New()
	confirmedAt := time.Now().UTC()
	sessionReader := &fakeCreateRegistrationSessionReader{
		page: submittableRegistrationPage(seriesID, instanceID, sessionID),
	}
	registrar := &fakeFreeRegistrationConfirmer{result: registration.Registration{
		ID:                  uuid.New(),
		TenantID:            tenantID,
		SeriesID:            seriesID,
		InstanceID:          instanceID,
		SessionID:           sessionID,
		PrincipalID:         principalID,
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      idempotencyKey.String(),
		ConfirmedAt:         &confirmedAt,
		Version:             1,
	}}
	service, err := NewCreateRegistrationService(
		tenantID,
		sessionReader,
		registrar,
		&fakePaidRegistrationStarter{},
		CreateRegistrationConfig{
			PrivacyPolicyVersion: "privacy-v1",
			ManualContactEnabled: true,
			ContactPolicyVersion: "contact-v1",
		},
	)
	if err != nil {
		t.Fatalf("NewCreateRegistrationService() error = %v", err)
	}
	request := CreateRegistrationRequest{
		IdempotencyKey:             idempotencyKey,
		InstancePublicationVersion: 1,
		PriceCents:                 0,
		PrivacyPolicyVersion:       "privacy-v1",
		ContactName:                "Wang Wei",
		ContactPhoneE164:           "+8613812345678",
		ContactPolicyVersion:       "contact-v1",
		QuestionnaireVersionID:     &questionnaireID,
		Answers: []activity.QuestionnaireAnswer{{
			FieldID: fieldID, Values: []string{"answer"},
		}},
	}

	got, err := service.Create(
		context.Background(),
		principalID,
		sessionID,
		request,
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.Registration.ID != registrar.result.ID || got.Payment != nil ||
		sessionReader.calls != 1 ||
		registrar.replayCalls != 1 || registrar.calls != 1 {
		t.Fatalf("Create() result=%+v session=%+v registrar=%+v", got, sessionReader, registrar)
	}
	command := registrar.command
	if command.TenantID != tenantID || command.SeriesID != seriesID ||
		command.InstanceID != instanceID || command.SessionID != sessionID ||
		command.PrincipalID != principalID ||
		command.IdempotencyKey != idempotencyKey.String() ||
		command.Submission == nil ||
		command.Submission.InstancePublicationVersion != 1 ||
		command.Submission.PriceCents != 0 ||
		command.PrivacyPolicyVersion != "privacy-v1" ||
		command.Submission.PrivacyPolicyVersion != "privacy-v1" ||
		command.Submission.Contact.Source != registration.ContactSourceManual ||
		command.Submission.Contact.PolicyVersion != "contact-v1" ||
		command.Submission.QuestionnaireVersionID == nil ||
		*command.Submission.QuestionnaireVersionID != questionnaireID ||
		len(command.Submission.Answers) != 1 ||
		command.Submission.Answers[0].FieldID != fieldID {
		t.Fatalf("Registration command = %+v", command)
	}
	request.Answers[0].Values[0] = "changed"
	if command.Submission.Answers[0].Values[0] == "changed" {
		t.Fatal("Registration command shares request answer state")
	}
}

func TestCreateRegistrationServicePreservesLegacyPrivacyOmission(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	idempotencyKey := uuid.New()
	confirmedAt := time.Now().UTC()
	registrar := &fakeFreeRegistrationConfirmer{result: registration.Registration{
		ID:                  uuid.New(),
		TenantID:            tenantID,
		SeriesID:            seriesID,
		InstanceID:          instanceID,
		SessionID:           sessionID,
		PrincipalID:         principalID,
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      idempotencyKey.String(),
		ConfirmedAt:         &confirmedAt,
		Version:             1,
	}}
	service, err := NewCreateRegistrationService(
		tenantID,
		&fakeCreateRegistrationSessionReader{
			page: submittableRegistrationPage(seriesID, instanceID, sessionID),
		},
		registrar,
		&fakePaidRegistrationStarter{},
		CreateRegistrationConfig{
			PrivacyPolicyVersion: "privacy-v1",
			ManualContactEnabled: true,
			ContactPolicyVersion: "contact-v1",
		},
	)
	if err != nil {
		t.Fatalf("NewCreateRegistrationService() error = %v", err)
	}

	_, err = service.Create(
		context.Background(),
		principalID,
		sessionID,
		CreateRegistrationRequest{
			IdempotencyKey:             idempotencyKey,
			InstancePublicationVersion: 1,
			ContactName:                "Wang Wei",
			ContactPhoneE164:           "+8613812345678",
			ContactPolicyVersion:       "contact-v1",
		},
	)
	if err != nil {
		t.Fatalf("Create(legacy privacy omission) error = %v", err)
	}
	if registrar.command.Submission == nil ||
		registrar.command.Submission.PrivacyPolicyVersion != "" ||
		registrar.command.PrivacyPolicyVersion != "privacy-v1" {
		t.Fatalf("legacy Registration command = %+v", registrar.command)
	}
}

func TestCreateRegistrationServiceLetsRegistrarReplayBeforeCurrentPrivacyCheck(
	t *testing.T,
) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	idempotencyKey := uuid.New()
	confirmedAt := time.Now().UTC()
	registrar := &fakeFreeRegistrationConfirmer{result: registration.Registration{
		ID:                  uuid.New(),
		TenantID:            tenantID,
		SeriesID:            seriesID,
		InstanceID:          instanceID,
		SessionID:           sessionID,
		PrincipalID:         principalID,
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      idempotencyKey.String(),
		ConfirmedAt:         &confirmedAt,
		Version:             1,
	}}
	service, err := NewCreateRegistrationService(
		tenantID,
		&fakeCreateRegistrationSessionReader{
			page: submittableRegistrationPage(seriesID, instanceID, sessionID),
		},
		registrar,
		&fakePaidRegistrationStarter{},
		CreateRegistrationConfig{
			PrivacyPolicyVersion: "privacy-v2",
			ManualContactEnabled: true,
			ContactPolicyVersion: "contact-v1",
		},
	)
	if err != nil {
		t.Fatalf("NewCreateRegistrationService() error = %v", err)
	}

	created, err := service.Create(
		context.Background(),
		principalID,
		sessionID,
		CreateRegistrationRequest{
			IdempotencyKey:             idempotencyKey,
			InstancePublicationVersion: 1,
			PriceCents:                 0,
			PrivacyPolicyVersion:       "privacy-v1",
			ContactName:                "Wang Wei",
			ContactPhoneE164:           "+8613812345678",
			ContactPolicyVersion:       "contact-v1",
		},
	)
	if err != nil || created.Registration.ID != registrar.result.ID {
		t.Fatalf("Create(replay after policy change) = %+v, %v", created, err)
	}
	if registrar.calls != 1 || registrar.command.Submission == nil ||
		registrar.command.Submission.PrivacyPolicyVersion != "privacy-v1" ||
		registrar.command.PrivacyPolicyVersion != "privacy-v2" {
		t.Fatalf("registrar command = %+v", registrar.command)
	}
}

func TestCreateRegistrationServiceReplaysBeforeMutableSessionLookup(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	idempotencyKey := uuid.New()
	confirmedAt := time.Now().UTC()
	replayed := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            tenantID,
		SeriesID:            seriesID,
		InstanceID:          instanceID,
		SessionID:           sessionID,
		PrincipalID:         principalID,
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      idempotencyKey.String(),
		ConfirmedAt:         &confirmedAt,
		Version:             1,
	}
	sessionReader := &fakeCreateRegistrationSessionReader{
		err: errors.New("Session was unpublished after commit"),
	}
	registrar := &fakeFreeRegistrationConfirmer{
		replayResult: replayed,
		replayFound:  true,
	}
	service, err := NewCreateRegistrationService(
		tenantID,
		sessionReader,
		registrar,
		&fakePaidRegistrationStarter{},
		CreateRegistrationConfig{
			PrivacyPolicyVersion: "privacy-v2",
			ManualContactEnabled: true,
			ContactPolicyVersion: "contact-v2",
		},
	)
	if err != nil {
		t.Fatalf("NewCreateRegistrationService() error = %v", err)
	}

	got, err := service.Create(
		context.Background(),
		principalID,
		sessionID,
		CreateRegistrationRequest{
			IdempotencyKey:             idempotencyKey,
			InstancePublicationVersion: 1,
			PriceCents:                 0,
			PrivacyPolicyVersion:       "privacy-v1",
			ContactName:                "Wang Wei",
			ContactPhoneE164:           "+8613812345678",
			ContactPolicyVersion:       "contact-v1",
		},
	)
	if err != nil || got.Registration.ID != replayed.ID || got.Payment != nil {
		t.Fatalf("Create(committed replay) = %+v, %v", got, err)
	}
	if sessionReader.calls != 0 || registrar.replayCalls != 1 || registrar.calls != 0 {
		t.Fatalf(
			"mutable lookup calls=%d replay=%d confirm=%d",
			sessionReader.calls,
			registrar.replayCalls,
			registrar.calls,
		)
	}
	if registrar.replayCommand.Submission.PrivacyPolicyVersion != "privacy-v1" ||
		registrar.replayCommand.Submission.Contact.PolicyVersion != "contact-v1" {
		t.Fatalf("replay command = %+v", registrar.replayCommand)
	}
}

func TestCreateRegistrationServiceBuildsServerOwnedPaidCommand(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	idempotencyKey := uuid.New()
	createdAt := time.Now().UTC()
	registrationID := uuid.New()
	orderID := uuid.New()
	merchantConfigGenerationID := uuid.New()
	paidRegistrar := &fakePaidRegistrationStarter{
		result: paymentpostgres.PaidRegistrationContext{
			Registration: registration.Registration{
				ID:                  registrationID,
				TenantID:            tenantID,
				SeriesID:            seriesID,
				InstanceID:          instanceID,
				SessionID:           sessionID,
				PrincipalID:         principalID,
				ParticipationStatus: registration.ParticipationStatusPendingPayment,
				IdempotencyKey:      idempotencyKey.String(),
				Version:             1,
				CreatedAt:           createdAt,
				UpdatedAt:           createdAt,
			},
			Payment: payment.PaymentContext{
				Order: payment.Order{
					ID:                         orderID,
					TenantID:                   tenantID,
					RegistrationID:             registrationID,
					SeriesID:                   seriesID,
					InstanceID:                 instanceID,
					SessionID:                  sessionID,
					PrincipalID:                principalID,
					PaymentStatus:              payment.OrderStatusPending,
					IdempotencyKey:             idempotencyKey.String(),
					MerchantOrderNo:            strings.ReplaceAll(idempotencyKey.String(), "-", ""),
					PaymentAppID:               "wx-app-1",
					PaymentMerchantID:          "merchant-1",
					MerchantConfigGenerationID: merchantConfigGenerationID,
					OriginalPriceCents:         9_900,
					PayableCents:               9_900,
					Version:                    1,
					CreatedAt:                  createdAt,
					UpdatedAt:                  createdAt,
				},
				Hold: payment.CapacityHold{
					ID:             uuid.New(),
					TenantID:       tenantID,
					OrderID:        orderID,
					RegistrationID: registrationID,
					SessionID:      sessionID,
					HoldStatus:     payment.CapacityHoldStatusActive,
					ExpiresAt:      createdAt.Add(payment.CapacityHoldDuration),
					Version:        1,
					CreatedAt:      createdAt,
					UpdatedAt:      createdAt,
				},
			},
		},
	}
	page := submittableRegistrationPage(seriesID, instanceID, sessionID)
	page.Detail.PublicationVersion = 4
	page.Detail.PriceCents = 9_900
	service, err := NewCreateRegistrationService(
		tenantID,
		&fakeCreateRegistrationSessionReader{page: page},
		&fakeFreeRegistrationConfirmer{},
		paidRegistrar,
		CreateRegistrationConfig{
			PrivacyPolicyVersion:       "privacy-v1",
			ManualContactEnabled:       true,
			ContactPolicyVersion:       "contact-v1",
			PaidRegistrationEnabled:    true,
			PaymentAppID:               "wx-app-1",
			PaymentMerchantID:          "merchant-1",
			MerchantConfigGenerationID: merchantConfigGenerationID,
		},
	)
	if err != nil {
		t.Fatalf("NewCreateRegistrationService() error = %v", err)
	}
	created, err := service.Create(
		context.Background(),
		principalID,
		sessionID,
		CreateRegistrationRequest{
			IdempotencyKey:             idempotencyKey,
			InstancePublicationVersion: 4,
			PriceCents:                 9_900,
			PrivacyPolicyVersion:       "privacy-v1",
			ContactName:                "Wang Wei",
			ContactPhoneE164:           "+8613812345678",
			ContactPolicyVersion:       "contact-v1",
		},
	)
	if err != nil {
		t.Fatalf("Create(paid) error = %v", err)
	}
	if created.Payment == nil || created.Payment.Order.ID != orderID ||
		paidRegistrar.replayCalls != 1 || paidRegistrar.calls != 1 {
		t.Fatalf("Create(paid) = %+v registrar=%+v", created, paidRegistrar)
	}
	command := paidRegistrar.command
	if command.TenantID != tenantID || command.PrincipalID != principalID ||
		command.SeriesID != seriesID || command.InstanceID != instanceID ||
		command.SessionID != sessionID ||
		command.IdempotencyKey != idempotencyKey.String() ||
		command.MerchantOrderNo != strings.ReplaceAll(idempotencyKey.String(), "-", "") ||
		command.PaymentAppID != "wx-app-1" ||
		command.PaymentMerchantID != "merchant-1" ||
		command.MerchantConfigGenerationID != merchantConfigGenerationID || command.CouponID != nil ||
		command.Submission == nil ||
		command.Submission.InstancePublicationVersion != 4 ||
		command.Submission.PriceCents != 9_900 ||
		command.PrivacyPolicyVersion != "privacy-v1" ||
		command.Submission.PrivacyPolicyVersion != "privacy-v1" {
		t.Fatalf("paid Registration command = %+v", command)
	}
}

func TestCreateRegistrationServiceRejectsIncompleteFeatureConfiguration(
	t *testing.T,
) {
	t.Parallel()

	valid := CreateRegistrationConfig{
		PrivacyPolicyVersion:       "privacy-v1",
		ManualContactEnabled:       true,
		ContactPolicyVersion:       "contact-v1",
		PaidRegistrationEnabled:    true,
		PaymentAppID:               "wx-app-1",
		PaymentMerchantID:          "merchant-1",
		MerchantConfigGenerationID: uuid.New(),
	}
	for _, test := range []struct {
		name          string
		paidRegistrar paidRegistrationStarter
		mutate        func(*CreateRegistrationConfig)
	}{
		{
			name:          "invalid privacy policy version",
			paidRegistrar: &fakePaidRegistrationStarter{},
			mutate: func(config *CreateRegistrationConfig) {
				config.PrivacyPolicyVersion = "privacy/v1"
			},
		},
		{
			name:   "paid registrar missing",
			mutate: func(*CreateRegistrationConfig) {},
		},
		{
			name:          "payment AppID missing",
			paidRegistrar: &fakePaidRegistrationStarter{},
			mutate: func(config *CreateRegistrationConfig) {
				config.PaymentAppID = ""
			},
		},
		{
			name:          "merchant missing",
			paidRegistrar: &fakePaidRegistrationStarter{},
			mutate: func(config *CreateRegistrationConfig) {
				config.PaymentMerchantID = ""
			},
		},
		{
			name:          "disabled payment carries merchant",
			paidRegistrar: &fakePaidRegistrationStarter{},
			mutate: func(config *CreateRegistrationConfig) {
				config.PaidRegistrationEnabled = false
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.mutate(&config)
			_, err := NewCreateRegistrationService(
				uuid.New(),
				&fakeCreateRegistrationSessionReader{},
				&fakeFreeRegistrationConfirmer{},
				test.paidRegistrar,
				config,
			)
			if !errors.Is(err, ErrInvalidCreateRegistrationService) {
				t.Fatalf("NewCreateRegistrationService() error = %v", err)
			}
		})
	}
}

func TestCreateRegistrationServiceFailsClosedBeforeRegistrar(t *testing.T) {
	t.Parallel()

	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	validRequest := CreateRegistrationRequest{
		IdempotencyKey:             uuid.New(),
		InstancePublicationVersion: 1,
		PriceCents:                 0,
		PrivacyPolicyVersion:       "privacy-v1",
		ContactName:                "Wang Wei",
		ContactPhoneE164:           "+8613812345678",
		ContactPolicyVersion:       "contact-v1",
	}
	tests := []struct {
		name               string
		manualEnabled      bool
		policy             string
		principalID        uuid.UUID
		request            CreateRegistrationRequest
		page               PublicSessionDetailPage
		registrarErr       error
		wantRegistrarCalls int
		want               error
	}{
		{
			name:               "manual contact disabled",
			manualEnabled:      false,
			principalID:        uuid.New(),
			request:            validRequest,
			page:               submittableRegistrationPage(seriesID, instanceID, sessionID),
			registrarErr:       registrationpostgres.ErrRegistrationContactPolicyConflict,
			wantRegistrarCalls: 1,
			want:               ErrManualRegistrationContactUnavailable,
		},
		{
			name:               "stale contact policy",
			manualEnabled:      true,
			policy:             "contact-v2",
			principalID:        uuid.New(),
			request:            validRequest,
			page:               submittableRegistrationPage(seriesID, instanceID, sessionID),
			registrarErr:       registrationpostgres.ErrRegistrationContactPolicyConflict,
			wantRegistrarCalls: 1,
			want:               ErrManualRegistrationContactUnavailable,
		},
		{
			name:               "stale privacy policy after replay lookup",
			manualEnabled:      true,
			policy:             "contact-v1",
			principalID:        uuid.New(),
			registrarErr:       registrationpostgres.ErrRegistrationPrivacyPolicyConflict,
			wantRegistrarCalls: 1,
			request: func() CreateRegistrationRequest {
				request := validRequest
				request.PrivacyPolicyVersion = "privacy-v2"
				return request
			}(),
			page: submittableRegistrationPage(seriesID, instanceID, sessionID),
			want: ErrRegistrationPrivacyPolicyUnavailable,
		},
		{
			name:          "noncanonical contact policy",
			manualEnabled: true,
			policy:        "contact-v1",
			principalID:   uuid.New(),
			request: func() CreateRegistrationRequest {
				request := validRequest
				request.ContactPolicyVersion = " contact-v1 "
				return request
			}(),
			page: submittableRegistrationPage(seriesID, instanceID, sessionID),
			want: ErrManualRegistrationContactUnavailable,
		},
		{
			name:          "missing principal",
			manualEnabled: true,
			policy:        "contact-v1",
			request:       validRequest,
			page:          submittableRegistrationPage(seriesID, instanceID, sessionID),
			want:          ErrInvalidCreateRegistrationRequest,
		},
		{
			name:          "closed Session",
			manualEnabled: true,
			policy:        "contact-v1",
			principalID:   uuid.New(),
			request:       validRequest,
			page: PublicSessionDetailPage{
				BrandStatus: activity.BrandLifecycleActive,
				Detail: activity.SessionDetail{
					SeriesID: seriesID, InstanceID: instanceID, SessionID: sessionID,
				},
			},
			want: registrationpostgres.ErrRegistrationUnavailable,
		},
		{
			name:          "stale confirmation price",
			manualEnabled: true,
			policy:        "contact-v1",
			principalID:   uuid.New(),
			request: func() CreateRegistrationRequest {
				request := validRequest
				request.PriceCents = 1
				return request
			}(),
			page: submittableRegistrationPage(seriesID, instanceID, sessionID),
			want: registrationpostgres.ErrRegistrationTransactionConflict,
		},
		{
			name:          "paid flow disabled",
			manualEnabled: true,
			policy:        "contact-v1",
			principalID:   uuid.New(),
			request: func() CreateRegistrationRequest {
				request := validRequest
				request.PriceCents = 9_900
				return request
			}(),
			page: func() PublicSessionDetailPage {
				page := submittableRegistrationPage(seriesID, instanceID, sessionID)
				page.Detail.PriceCents = 9_900
				return page
			}(),
			want: ErrPaidRegistrationUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sessionReader := &fakeCreateRegistrationSessionReader{page: test.page}
			registrar := &fakeFreeRegistrationConfirmer{err: test.registrarErr}
			paidRegistrar := &fakePaidRegistrationStarter{}
			service, err := NewCreateRegistrationService(
				uuid.New(),
				sessionReader,
				registrar,
				paidRegistrar,
				CreateRegistrationConfig{
					PrivacyPolicyVersion: "privacy-v1",
					ManualContactEnabled: test.manualEnabled,
					ContactPolicyVersion: test.policy,
				},
			)
			if err != nil {
				t.Fatalf("NewCreateRegistrationService() error = %v", err)
			}
			_, err = service.Create(
				context.Background(),
				test.principalID,
				sessionID,
				test.request,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want %v", err, test.want)
			}
			if registrar.calls != test.wantRegistrarCalls || paidRegistrar.calls != 0 {
				t.Fatalf("registrar calls free=%d paid=%d", registrar.calls, paidRegistrar.calls)
			}
		})
	}
}

type fakeCreateRegistrationSessionReader struct {
	page PublicSessionDetailPage
	err  error

	calls     int
	sessionID uuid.UUID
}

func (reader *fakeCreateRegistrationSessionReader) ReadSessionDetail(
	_ context.Context,
	sessionID uuid.UUID,
) (PublicSessionDetailPage, error) {
	reader.calls++
	reader.sessionID = sessionID
	return reader.page, reader.err
}

type fakeFreeRegistrationConfirmer struct {
	result       registration.Registration
	err          error
	replayResult registration.Registration
	replayFound  bool
	replayErr    error

	calls         int
	command       registrationpostgres.ConfirmFreeRegistrationCommand
	replayCalls   int
	replayCommand registrationpostgres.ReplayFreeRegistrationCommand
}

type fakePaidRegistrationStarter struct {
	result       paymentpostgres.PaidRegistrationContext
	err          error
	replayResult paymentpostgres.PaidRegistrationContext
	replayFound  bool
	replayErr    error

	calls         int
	command       paymentpostgres.StartPaidRegistrationCommand
	replayCalls   int
	replayCommand paymentpostgres.ReplayPaidRegistrationCommand
}

func (starter *fakePaidRegistrationStarter) Replay(
	_ context.Context,
	command paymentpostgres.ReplayPaidRegistrationCommand,
) (paymentpostgres.PaidRegistrationContext, bool, error) {
	starter.replayCalls++
	starter.replayCommand = command
	return starter.replayResult, starter.replayFound, starter.replayErr
}

func (starter *fakePaidRegistrationStarter) Start(
	_ context.Context,
	command paymentpostgres.StartPaidRegistrationCommand,
) (paymentpostgres.PaidRegistrationContext, error) {
	starter.calls++
	starter.command = command
	return starter.result, starter.err
}

func (confirmer *fakeFreeRegistrationConfirmer) Confirm(
	_ context.Context,
	command registrationpostgres.ConfirmFreeRegistrationCommand,
) (registration.Registration, error) {
	confirmer.calls++
	confirmer.command = command
	return confirmer.result, confirmer.err
}

func (confirmer *fakeFreeRegistrationConfirmer) Replay(
	_ context.Context,
	command registrationpostgres.ReplayFreeRegistrationCommand,
) (registration.Registration, bool, error) {
	confirmer.replayCalls++
	confirmer.replayCommand = command
	return confirmer.replayResult, confirmer.replayFound, confirmer.replayErr
}

func submittableRegistrationPage(
	seriesID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) PublicSessionDetailPage {
	return PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail: activity.SessionDetail{
			SeriesID: seriesID, InstanceID: instanceID, SessionID: sessionID,
			PublicationVersion: 1,
			PriceCents:         0,
			CTA: activity.SessionDetailCTA{
				Action:  activity.SessionDetailCTAActionStartRegistration,
				Enabled: true,
			},
		},
	}
}
