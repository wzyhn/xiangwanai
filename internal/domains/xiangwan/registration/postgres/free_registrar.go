package registrationpostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidFreeRegistrationCommand    = errors.New("invalid xiangwan free Registration command")
	ErrRegistrationUnavailable           = errors.New("xiangwan Session is unavailable for Registration")
	ErrRegistrationPaymentRequired       = errors.New("xiangwan Session requires payment")
	ErrRegistrationAlreadyOpen           = errors.New("xiangwan principal already has an open Registration")
	ErrRegistrationIdempotencyConflict   = errors.New("xiangwan Registration idempotency conflict")
	ErrRegistrationCapacityConflict      = errors.New("xiangwan Session capacity changed")
	ErrRegistrationTransactionConflict   = errors.New("xiangwan Registration transaction conflict")
	ErrRegistrationQuestionnaireConflict = errors.New(
		"xiangwan Registration questionnaire changed",
	)
	ErrRegistrationContactPolicyConflict = errors.New(
		"xiangwan Registration contact policy changed",
	)
	ErrRegistrationPrivacyPolicyConflict = errors.New(
		"xiangwan Registration privacy policy changed",
	)
	ErrRegistrationGenerationInactive = errors.New(
		"xiangwan Registration generation is inactive",
	)
	ErrRegistrationAnswersInvalid = errors.New(
		"xiangwan Registration questionnaire answers are invalid",
	)
)

type ConfirmFreeRegistrationCommand struct {
	TenantID             uuid.UUID
	SeriesID             uuid.UUID
	InstanceID           uuid.UUID
	SessionID            uuid.UUID
	PrincipalID          uuid.UUID
	IdempotencyKey       string
	Submission           *registration.RegistrationSubmission
	PrivacyPolicyVersion string
	ManualContactEnabled bool
	ContactPolicyVersion string
}

// ReplayFreeRegistrationCommand contains only immutable client intent. It is
// deliberately independent of the current Series, Instance, Session, and
// policy state so a committed response can still be recovered after those
// mutable facts change.
type ReplayFreeRegistrationCommand struct {
	TenantID       uuid.UUID
	SessionID      uuid.UUID
	PrincipalID    uuid.UUID
	IdempotencyKey string
	Submission     registration.RegistrationSubmission
}

// FreeRegistrar confirms one zero-price Registration and updates both capacity
// and historical participation projections in a serializable PostgreSQL
// transaction. The Session row is the concurrency lock; no Redis lock is used.
type FreeRegistrar struct {
	transactions registrationTransactionStarter
	now          func() time.Time
	generationID uuid.UUID
}

func NewFreeRegistrar(db *sql.DB, generationID uuid.UUID) *FreeRegistrar {
	return &FreeRegistrar{
		transactions: sqlRegistrationTransactionStarter{db: db},
		now:          time.Now,
		generationID: generationID,
	}
}

// Replay returns an exact committed free Registration without consulting any
// mutable publication or capacity fact. A reused operation key with different
// owner, Session, payload, or payment mode fails closed as an idempotency
// conflict.
func (registrar *FreeRegistrar) Replay(
	ctx context.Context,
	command ReplayFreeRegistrationCommand,
) (registration.Registration, bool, error) {
	if registrar == nil || registrar.transactions == nil || ctx == nil ||
		command.TenantID == uuid.Nil || command.SessionID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		!registration.ValidIdempotencyKey(command.IdempotencyKey) {
		return registration.Registration{}, false,
			ErrInvalidFreeRegistrationCommand
	}
	prepared, err := registration.PrepareRegistrationSubmission(
		command.SessionID,
		command.Submission,
	)
	if err != nil {
		return registration.Registration{}, false, fmt.Errorf(
			"%w: %v",
			ErrInvalidFreeRegistrationCommand,
			err,
		)
	}
	tx, err := registrar.transactions.beginTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true},
	)
	if err != nil {
		return registration.Registration{}, false, fmt.Errorf(
			"begin xiangwan free Registration replay transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	existing, err := (&Repository{db: tx}).GetByIdempotencyKey(
		ctx,
		command.TenantID,
		command.IdempotencyKey,
	)
	if errors.Is(err, ErrRegistrationNotFound) {
		if err := tx.Commit(); err != nil {
			return registration.Registration{}, false,
				classifyRegistrationCommitError(err)
		}
		committed = true
		return registration.Registration{}, false, nil
	}
	if err != nil {
		return registration.Registration{}, false, err
	}
	if existing.SessionID != command.SessionID ||
		existing.PrincipalID != command.PrincipalID ||
		existing.IdempotencyKey != command.IdempotencyKey ||
		existing.SeriesID == uuid.Nil || existing.InstanceID == uuid.Nil ||
		(existing.ParticipationStatus != registration.ParticipationStatusConfirmed &&
			existing.ParticipationStatus != registration.ParticipationStatusCancelled) {
		return registration.Registration{}, false,
			ErrRegistrationIdempotencyConflict
	}
	fingerprint, fingerprintErr := getRegistrationSnapshotFingerprint(
		ctx,
		tx,
		command.TenantID,
		existing.ID,
	)
	if fingerprintErr != nil || fingerprint != prepared.RequestFingerprint {
		return registration.Registration{}, false,
			ErrRegistrationIdempotencyConflict
	}
	if err := tx.Commit(); err != nil {
		return registration.Registration{}, false,
			classifyRegistrationCommitError(err)
	}
	committed = true
	return existing, true, nil
}

func (registrar *FreeRegistrar) Confirm(
	ctx context.Context,
	command ConfirmFreeRegistrationCommand,
) (registration.Registration, error) {
	if registrar == nil || registrar.transactions == nil || registrar.now == nil ||
		registrar.generationID == uuid.Nil {
		return registration.Registration{}, ErrInvalidFreeRegistrationCommand
	}
	var preparedSubmission *registration.PreparedRegistrationSubmission
	if command.Submission != nil {
		prepared, err := registration.PrepareRegistrationSubmission(
			command.SessionID,
			*command.Submission,
		)
		if err != nil {
			return registration.Registration{}, fmt.Errorf(
				"%w: %v",
				ErrInvalidFreeRegistrationCommand,
				err,
			)
		}
		preparedSubmission = &prepared
	}
	now := registrar.now().UTC()
	candidate, err := registration.NewRegistration(registration.NewRegistrationCommand{
		TenantID:       command.TenantID,
		SeriesID:       command.SeriesID,
		InstanceID:     command.InstanceID,
		SessionID:      command.SessionID,
		PrincipalID:    command.PrincipalID,
		IdempotencyKey: command.IdempotencyKey,
		Now:            now,
	})
	if err != nil {
		return registration.Registration{}, fmt.Errorf("%w: %v", ErrInvalidFreeRegistrationCommand, err)
	}

	tx, err := registrar.transactions.beginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return registration.Registration{}, fmt.Errorf("begin xiangwan free Registration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	repository := &Repository{db: tx}
	existing, err := repository.GetByIdempotencyKey(ctx, command.TenantID, command.IdempotencyKey)
	switch {
	case err == nil:
		if !registrationMatchesCommand(existing, command) {
			return registration.Registration{}, ErrRegistrationIdempotencyConflict
		}
		if preparedSubmission != nil {
			fingerprint, fingerprintErr := getRegistrationSnapshotFingerprint(
				ctx,
				tx,
				command.TenantID,
				existing.ID,
			)
			if fingerprintErr != nil ||
				fingerprint != preparedSubmission.RequestFingerprint {
				return registration.Registration{},
					ErrRegistrationIdempotencyConflict
			}
		}
		if err := tx.Commit(); err != nil {
			return registration.Registration{}, classifyRegistrationCommitError(err)
		}
		committed = true
		return existing, nil
	case !errors.Is(err, ErrRegistrationNotFound):
		return registration.Registration{}, err
	}
	if err := tx.lockActiveGeneration(
		ctx,
		command.TenantID,
		registrar.generationID,
	); err != nil {
		return registration.Registration{}, err
	}
	if preparedSubmission != nil {
		if preparedSubmission.PrivacyPolicyVersion != "" &&
			preparedSubmission.PrivacyPolicyVersion !=
				command.PrivacyPolicyVersion {
			return registration.Registration{}, ErrRegistrationPrivacyPolicyConflict
		}
		if !command.ManualContactEnabled ||
			preparedSubmission.Contact.PolicyVersion != command.ContactPolicyVersion {
			return registration.Registration{}, ErrRegistrationContactPolicyConflict
		}
		if err := requireActiveRegistrationBrand(
			ctx,
			tx,
			command.TenantID,
		); err != nil {
			return registration.Registration{}, err
		}
	}

	series, err := lockRegistrationSeries(ctx, tx, command.TenantID, command.SeriesID)
	if err != nil {
		return registration.Registration{}, err
	}
	if series.status != activity.SeriesStatusActive {
		return registration.Registration{}, ErrRegistrationUnavailable
	}

	instanceStatus, err := lockRegistrationInstance(
		ctx,
		tx,
		command.TenantID,
		command.SeriesID,
		command.InstanceID,
	)
	if err != nil {
		return registration.Registration{}, err
	}
	if instanceStatus != activity.InstanceStatusPublished {
		return registration.Registration{}, ErrRegistrationUnavailable
	}
	instancePublicationVersion := int64(0)
	if preparedSubmission != nil {
		instancePublicationVersion, err =
			getCurrentRegistrationInstancePublicationVersion(
				ctx,
				tx,
				command.TenantID,
				command.SeriesID,
				command.InstanceID,
			)
		if err != nil {
			return registration.Registration{}, err
		}
		if instancePublicationVersion !=
			preparedSubmission.InstancePublicationVersion {
			return registration.Registration{},
				ErrRegistrationTransactionConflict
		}
	}

	session, err := lockRegistrationSession(
		ctx,
		tx,
		command.TenantID,
		command.InstanceID,
		command.SessionID,
	)
	if err != nil {
		return registration.Registration{}, err
	}
	if session.status != activity.SessionStatusPublished {
		return registration.Registration{}, ErrRegistrationUnavailable
	}

	_, err = repository.GetOpenByPrincipalSession(
		ctx,
		command.TenantID,
		command.PrincipalID,
		command.SessionID,
	)
	switch {
	case err == nil:
		return registration.Registration{}, ErrRegistrationAlreadyOpen
	case !errors.Is(err, ErrRegistrationNotFound):
		return registration.Registration{}, err
	}

	if session.priceCents == nil {
		return registration.Registration{}, fmt.Errorf("%w: price is missing", ErrRegistrationUnavailable)
	}
	if preparedSubmission != nil &&
		*session.priceCents != preparedSubmission.PriceCents {
		return registration.Registration{},
			ErrRegistrationTransactionConflict
	}
	display, err := activity.DecideSessionDisplay(activity.SessionDisplayFacts{
		Now:                        now,
		RegistrationStartAt:        session.registrationStartAt,
		RegistrationEndAt:          session.registrationEndAt,
		SessionStartAt:             session.sessionStartAt,
		SessionEndAt:               session.sessionEndAt,
		Capacity:                   session.capacity,
		ConfirmedRegistrationCount: session.confirmedCount,
		ActiveHoldCount:            session.activeHoldCount,
		GroupMinimum:               session.groupMinimum,
		LowStockThreshold:          session.lowStockThreshold,
	})
	if err != nil {
		return registration.Registration{}, fmt.Errorf("%w: %v", ErrRegistrationUnavailable, err)
	}
	if !display.State.RegistrationAllowed() {
		return registration.Registration{}, fmt.Errorf(
			"%w: display state %q",
			ErrRegistrationUnavailable,
			display.State,
		)
	}
	if *session.priceCents != 0 {
		return registration.Registration{}, ErrRegistrationPaymentRequired
	}
	var questionnaire *activity.SessionQuestionnaire
	var normalizedAnswers []activity.QuestionnaireAnswer
	if preparedSubmission != nil {
		questionnaire, normalizedAnswers, err =
			validateCurrentRegistrationQuestionnaire(
				ctx,
				tx,
				command.TenantID,
				command.InstanceID,
				command.SessionID,
				*preparedSubmission,
			)
		if err != nil {
			return registration.Registration{}, err
		}
	}

	created, err := repository.Create(ctx, candidate)
	if err != nil {
		return registration.Registration{}, classifyRegistrationCreateError(err)
	}
	if preparedSubmission != nil {
		if err := createRegistrationSubmissionSnapshot(
			ctx,
			tx,
			created,
			instancePublicationVersion,
			session,
			*preparedSubmission,
			questionnaire,
			normalizedAnswers,
			now,
		); err != nil {
			return registration.Registration{}, err
		}
	}
	if err := incrementConfirmedRegistration(
		ctx,
		tx,
		command.TenantID,
		command.InstanceID,
		command.SessionID,
		session.version,
		now,
	); err != nil {
		return registration.Registration{}, err
	}
	if err := incrementHistoricalRegistration(
		ctx,
		tx,
		command.TenantID,
		command.SeriesID,
		series.version,
		now,
	); err != nil {
		return registration.Registration{}, err
	}

	if err := tx.Commit(); err != nil {
		return registration.Registration{}, classifyRegistrationCommitError(err)
	}
	committed = true
	return created, nil
}

func requireActiveRegistrationBrand(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
) error {
	var status activity.BrandLifecycleStatus
	err := tx.queryRowContext(ctx, `
SELECT lifecycle_status
FROM xiangwan_brand_profiles
WHERE tenant_id = $1
FOR SHARE
`, tenantID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && status != activity.BrandLifecycleActive) {
		return ErrRegistrationUnavailable
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan Registration BrandProfile: %w", err)
	}
	return nil
}

func getCurrentRegistrationInstancePublicationVersion(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (int64, error) {
	var publicationVersion int64
	err := tx.queryRowContext(ctx, `
SELECT activity_instance.publication_version
FROM xiangwan_activity_series AS activity_series
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = activity_series.tenant_id
 AND activity_instance.series_id = activity_series.id
 AND activity_instance.id = activity_series.current_public_instance_id
WHERE activity_series.tenant_id = $1
  AND activity_series.id = $2
  AND activity_instance.id = $3
  AND activity_instance.status = 'published'
`, tenantID, seriesID, instanceID).Scan(&publicationVersion)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && publicationVersion < 1) {
		return 0, ErrRegistrationUnavailable
	}
	if err != nil {
		return 0, fmt.Errorf(
			"read xiangwan Registration Instance publication: %w",
			err,
		)
	}
	return publicationVersion, nil
}

func getRegistrationSnapshotFingerprint(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (string, error) {
	var fingerprint string
	err := tx.queryRowContext(ctx, `
SELECT request_fingerprint
FROM xiangwan_registration_snapshots
WHERE tenant_id = $1 AND registration_id = $2
`, tenantID, registrationID).Scan(&fingerprint)
	if err != nil {
		return "", fmt.Errorf(
			"read xiangwan Registration submission receipt: %w",
			err,
		)
	}
	return fingerprint, nil
}

type registrationQuestionnaireFieldJSON struct {
	FieldID       uuid.UUID                       `json:"field_id"`
	Code          string                          `json:"code"`
	Type          activity.QuestionnaireFieldType `json:"type"`
	Label         string                          `json:"label"`
	HelpText      string                          `json:"help_text"`
	Required      bool                            `json:"required"`
	SortOrder     int                             `json:"sort_order"`
	MinLength     *int                            `json:"min_length"`
	MaxLength     *int                            `json:"max_length"`
	MaxSelections *int                            `json:"max_selections"`
	Options       []activity.QuestionnaireOption  `json:"options"`
}

func loadCurrentRegistrationQuestionnaire(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionQuestionnaire, error) {
	var questionnaire activity.SessionQuestionnaire
	var fieldsJSON []byte
	err := tx.queryRowContext(ctx, `
SELECT
    questionnaire.questionnaire_version_id,
    questionnaire.version,
    questionnaire.privacy_purpose,
    questionnaire.privacy_policy_version,
    questionnaire.published_at,
    JSONB_AGG(
        JSONB_BUILD_OBJECT(
            'field_id', field.field_id,
            'code', field.field_code,
            'type', field.field_type,
            'label', field.label,
            'help_text', field.help_text,
            'required', field.is_required,
            'sort_order', field.sort_order,
            'min_length', field.min_length,
            'max_length', field.max_length,
            'max_selections', field.max_selections,
            'options', field.options
        ) ORDER BY field.sort_order, field.field_id
    )
FROM LATERAL (
    SELECT assignment.questionnaire_version_id
    FROM xiangwan_instance_questionnaires AS assignment
    WHERE assignment.tenant_id = $1
      AND assignment.instance_id = $2
    ORDER BY assignment.assignment_version DESC
    LIMIT 1
) AS current_assignment
JOIN xiangwan_questionnaire_versions AS questionnaire
  ON questionnaire.tenant_id = $1
 AND questionnaire.instance_id = $2
 AND questionnaire.questionnaire_version_id =
     current_assignment.questionnaire_version_id
 AND questionnaire.status = 'published'
JOIN xiangwan_questionnaire_fields AS field
  ON field.tenant_id = questionnaire.tenant_id
 AND field.instance_id = questionnaire.instance_id
 AND field.questionnaire_version_id = questionnaire.questionnaire_version_id
GROUP BY
    questionnaire.questionnaire_version_id,
    questionnaire.version,
    questionnaire.privacy_purpose,
    questionnaire.privacy_policy_version,
    questionnaire.published_at
`, tenantID, instanceID).Scan(
		&questionnaire.QuestionnaireVersionID,
		&questionnaire.Version,
		&questionnaire.PrivacyPurpose,
		&questionnaire.PrivacyPolicyVersion,
		&questionnaire.PublishedAt,
		&fieldsJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.SessionQuestionnaire{},
			activity.ErrQuestionnaireUnavailable
	}
	if err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"read current xiangwan Registration questionnaire: %w",
			err,
		)
	}
	var fields []registrationQuestionnaireFieldJSON
	if err := json.Unmarshal(fieldsJSON, &fields); err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"decode current xiangwan Registration questionnaire: %w",
			err,
		)
	}
	questionnaire.InstanceID = instanceID
	questionnaire.SessionID = sessionID
	questionnaire.Fields = make([]activity.QuestionnaireField, 0, len(fields))
	for _, field := range fields {
		questionnaire.Fields = append(
			questionnaire.Fields,
			activity.QuestionnaireField{
				FieldID:       field.FieldID,
				Code:          field.Code,
				Type:          field.Type,
				Label:         field.Label,
				HelpText:      field.HelpText,
				Required:      field.Required,
				SortOrder:     field.SortOrder,
				MinLength:     field.MinLength,
				MaxLength:     field.MaxLength,
				MaxSelections: field.MaxSelections,
				Options:       field.Options,
			},
		)
	}
	if err := activity.ValidateSessionQuestionnaire(questionnaire); err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"validate current xiangwan Registration questionnaire: %w",
			err,
		)
	}
	return questionnaire, nil
}

func validateCurrentRegistrationQuestionnaire(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	submission registration.PreparedRegistrationSubmission,
) (
	*activity.SessionQuestionnaire,
	[]activity.QuestionnaireAnswer,
	error,
) {
	questionnaire, err := loadCurrentRegistrationQuestionnaire(
		ctx,
		tx,
		tenantID,
		instanceID,
		sessionID,
	)
	if errors.Is(err, activity.ErrQuestionnaireUnavailable) {
		if submission.QuestionnaireVersionID != nil || len(submission.Answers) != 0 {
			return nil, nil, ErrRegistrationQuestionnaireConflict
		}
		return nil, []activity.QuestionnaireAnswer{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if submission.QuestionnaireVersionID == nil ||
		*submission.QuestionnaireVersionID !=
			questionnaire.QuestionnaireVersionID {
		return nil, nil, ErrRegistrationQuestionnaireConflict
	}
	answers, err := activity.NormalizeQuestionnaireAnswers(
		questionnaire,
		submission.Answers,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: %v",
			ErrRegistrationAnswersInvalid,
			err,
		)
	}
	return &questionnaire, answers, nil
}

func createRegistrationSubmissionSnapshot(
	ctx context.Context,
	tx registrationTransaction,
	created registration.Registration,
	instancePublicationVersion int64,
	session lockedRegistrationSession,
	submission registration.PreparedRegistrationSubmission,
	questionnaire *activity.SessionQuestionnaire,
	answers []activity.QuestionnaireAnswer,
	now time.Time,
) error {
	var questionnaireVersionID any
	var questionnaireVersion any
	var privacyPurpose any
	var privacyPolicyVersion any
	var acknowledgedPrivacyPolicyVersion any
	if submission.PrivacyPolicyVersion != "" {
		acknowledgedPrivacyPolicyVersion = submission.PrivacyPolicyVersion
	}
	if questionnaire != nil {
		questionnaireVersionID = questionnaire.QuestionnaireVersionID
		questionnaireVersion = questionnaire.Version
		privacyPurpose = questionnaire.PrivacyPurpose
		privacyPolicyVersion = questionnaire.PrivacyPolicyVersion
	}
	var storedRegistrationID uuid.UUID
	err := tx.queryRowContext(ctx, `
INSERT INTO xiangwan_registration_snapshots (
    registration_id,
    tenant_id,
    series_id,
    instance_id,
    session_id,
    principal_id,
    instance_publication_version,
    session_version,
    price_cents,
    privacy_policy_version,
    contact_source,
    contact_name,
    contact_phone_e164,
    contact_policy_version,
    questionnaire_version_id,
    questionnaire_version,
    questionnaire_privacy_purpose,
    questionnaire_privacy_policy_version,
    request_fingerprint,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17, $18, $19, $20
)
RETURNING registration_id
`,
		created.ID,
		created.TenantID,
		created.SeriesID,
		created.InstanceID,
		created.SessionID,
		created.PrincipalID,
		instancePublicationVersion,
		session.version,
		*session.priceCents,
		acknowledgedPrivacyPolicyVersion,
		submission.Contact.Source,
		submission.Contact.Name,
		submission.Contact.PhoneE164,
		submission.Contact.PolicyVersion,
		questionnaireVersionID,
		questionnaireVersion,
		privacyPurpose,
		privacyPolicyVersion,
		submission.RequestFingerprint,
		now,
	).Scan(&storedRegistrationID)
	if err != nil {
		return fmt.Errorf("create xiangwan Registration snapshot: %w", err)
	}
	if storedRegistrationID != created.ID {
		return ErrRegistrationTransactionConflict
	}
	if questionnaire == nil {
		if len(answers) != 0 {
			return ErrRegistrationTransactionConflict
		}
		return nil
	}
	if len(answers) != len(questionnaire.Fields) {
		return ErrRegistrationTransactionConflict
	}
	for index, field := range questionnaire.Fields {
		if answers[index].FieldID != field.FieldID {
			return ErrRegistrationTransactionConflict
		}
		optionsJSON, marshalErr := json.Marshal(field.Options)
		if marshalErr != nil {
			return fmt.Errorf("encode xiangwan Registration options: %w", marshalErr)
		}
		answerJSON, marshalErr := json.Marshal(answers[index].Values)
		if marshalErr != nil {
			return fmt.Errorf("encode xiangwan Registration answer: %w", marshalErr)
		}
		var storedFieldID uuid.UUID
		err := tx.queryRowContext(ctx, `
INSERT INTO xiangwan_registration_answers (
    tenant_id,
    instance_id,
    registration_id,
    questionnaire_version_id,
    field_id,
    field_code,
    field_type,
    field_label,
    field_help_text,
    is_required,
    sort_order,
    min_length,
    max_length,
    max_selections,
    options,
    answer_values,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11, $12, $13, $14, $15, $16, $17
)
RETURNING field_id
`,
			created.TenantID,
			created.InstanceID,
			created.ID,
			questionnaire.QuestionnaireVersionID,
			field.FieldID,
			field.Code,
			field.Type,
			field.Label,
			field.HelpText,
			field.Required,
			field.SortOrder,
			field.MinLength,
			field.MaxLength,
			field.MaxSelections,
			optionsJSON,
			answerJSON,
			now,
		).Scan(&storedFieldID)
		if err != nil {
			return fmt.Errorf("create xiangwan Registration answer: %w", err)
		}
		if storedFieldID != field.FieldID {
			return ErrRegistrationTransactionConflict
		}
	}
	return nil
}

type lockedRegistrationSeries struct {
	status  activity.SeriesStatus
	version int64
}

func lockRegistrationSeries(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (lockedRegistrationSeries, error) {
	var series lockedRegistrationSeries
	err := tx.queryRowContext(ctx, `
SELECT status, version
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, seriesID).Scan(&series.status, &series.version)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedRegistrationSeries{}, ErrRegistrationUnavailable
	}
	if err != nil {
		return lockedRegistrationSeries{}, fmt.Errorf("lock xiangwan Registration Series: %w", err)
	}
	return series, nil
}

func lockRegistrationInstance(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (activity.InstanceStatus, error) {
	var status activity.InstanceStatus
	err := tx.queryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2 AND id = $3
FOR UPDATE
`, tenantID, seriesID, instanceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRegistrationUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("lock xiangwan Registration Instance: %w", err)
	}
	return status, nil
}

type lockedRegistrationSession struct {
	status              activity.SessionStatus
	registrationStartAt time.Time
	registrationEndAt   time.Time
	sessionStartAt      time.Time
	sessionEndAt        time.Time
	capacity            int
	groupMinimum        int
	lowStockThreshold   *int
	priceCents          *int64
	confirmedCount      int
	activeHoldCount     int
	version             int64
}

func lockRegistrationSession(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (lockedRegistrationSession, error) {
	var session lockedRegistrationSession
	var registrationStartAt sql.NullTime
	var registrationEndAt sql.NullTime
	var sessionStartAt sql.NullTime
	var sessionEndAt sql.NullTime
	var capacity sql.NullInt64
	var groupMinimum sql.NullInt64
	var lowStockThreshold sql.NullInt64
	var priceCents sql.NullInt64
	err := tx.queryRowContext(ctx, `
SELECT
    status,
    registration_start_at,
    registration_end_at,
    session_start_at,
    session_end_at,
    capacity,
    group_minimum,
    low_stock_threshold,
    price_cents,
    confirmed_registration_count,
    active_hold_count,
    version
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3
FOR UPDATE
`, tenantID, instanceID, sessionID).Scan(
		&session.status,
		&registrationStartAt,
		&registrationEndAt,
		&sessionStartAt,
		&sessionEndAt,
		&capacity,
		&groupMinimum,
		&lowStockThreshold,
		&priceCents,
		&session.confirmedCount,
		&session.activeHoldCount,
		&session.version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedRegistrationSession{}, ErrRegistrationUnavailable
	}
	if err != nil {
		return lockedRegistrationSession{}, fmt.Errorf("lock xiangwan Registration Session: %w", err)
	}
	session.registrationStartAt = registrationStartAt.Time
	session.registrationEndAt = registrationEndAt.Time
	session.sessionStartAt = sessionStartAt.Time
	session.sessionEndAt = sessionEndAt.Time
	session.capacity = int(capacity.Int64)
	session.groupMinimum = int(groupMinimum.Int64)
	session.lowStockThreshold = nullableIntPointer(lowStockThreshold)
	session.priceCents = nullableInt64Pointer(priceCents)
	return session, nil
}

func incrementConfirmedRegistration(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	expectedVersion int64,
	now time.Time,
) error {
	var newVersion int64
	err := tx.queryRowContext(ctx, `
UPDATE xiangwan_activity_sessions
SET confirmed_registration_count = confirmed_registration_count + 1,
    version = version + 1,
    updated_at = $4
WHERE tenant_id = $1
  AND instance_id = $2
  AND id = $3
  AND status = 'published'
  AND version = $5
  AND confirmed_registration_count + active_hold_count < capacity
RETURNING version
`, tenantID, instanceID, sessionID, now, expectedVersion).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRegistrationCapacityConflict
	}
	if err != nil {
		return fmt.Errorf("increment xiangwan Session confirmed Registration count: %w", err)
	}
	return nil
}

func incrementHistoricalRegistration(
	ctx context.Context,
	tx registrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	expectedVersion int64,
	now time.Time,
) error {
	var newVersion int64
	err := tx.queryRowContext(ctx, `
UPDATE xiangwan_activity_series
SET historical_registration_count = historical_registration_count + 1,
    version = version + 1,
    updated_at = $3
WHERE tenant_id = $1
  AND id = $2
  AND version = $4
RETURNING version
`, tenantID, seriesID, now, expectedVersion).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRegistrationTransactionConflict
	}
	if err != nil {
		return fmt.Errorf("increment xiangwan Series historical Registration count: %w", err)
	}
	return nil
}

func registrationMatchesCommand(
	existing registration.Registration,
	command ConfirmFreeRegistrationCommand,
) bool {
	return existing.TenantID == command.TenantID &&
		existing.SeriesID == command.SeriesID &&
		existing.InstanceID == command.InstanceID &&
		existing.SessionID == command.SessionID &&
		existing.PrincipalID == command.PrincipalID
}

func classifyRegistrationCreateError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" {
		return err
	}
	switch postgresError.ConstraintName {
	case "xiangwan_registrations_tenant_idempotency_key_key":
		return ErrRegistrationIdempotencyConflict
	case "uq_xiangwan_registrations_open_principal_session":
		return ErrRegistrationAlreadyOpen
	default:
		return ErrRegistrationTransactionConflict
	}
}

func classifyRegistrationCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "40001" {
		return fmt.Errorf("%w: %v", ErrRegistrationTransactionConflict, err)
	}
	return fmt.Errorf("commit xiangwan free Registration transaction: %w", err)
}

func nullableIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

func nullableInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

type registrationTransaction interface {
	queryExecutor
	lockActiveGeneration(context.Context, uuid.UUID, uuid.UUID) error
	Commit() error
	Rollback() error
}

type registrationTransactionStarter interface {
	beginTx(context.Context, *sql.TxOptions) (registrationTransaction, error)
}

type sqlRegistrationTransactionStarter struct {
	db *sql.DB
}

func (starter sqlRegistrationTransactionStarter) beginTx(
	ctx context.Context,
	options *sql.TxOptions,
) (registrationTransaction, error) {
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlRegistrationTransaction{tx: tx}, nil
}

type sqlRegistrationTransaction struct {
	tx *sql.Tx
}

func (tx *sqlRegistrationTransaction) lockActiveGeneration(
	ctx context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	var writeEpoch int64
	err := tx.tx.QueryRowContext(ctx, `
SELECT write_epoch
FROM xiangwan_runtime_generations
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
  AND active_generation_id = $2
  AND write_epoch > 0
  AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, tenantID, generationID).Scan(&writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRegistrationGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan Registration generation: %w", err)
	}
	return nil
}

func (tx *sqlRegistrationTransaction) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return tx.tx.QueryRowContext(ctx, query, args...)
}

func (tx *sqlRegistrationTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlRegistrationTransaction) Rollback() error {
	return tx.tx.Rollback()
}
