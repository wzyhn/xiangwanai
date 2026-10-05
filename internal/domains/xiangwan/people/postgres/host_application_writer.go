package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidHostApplicationCommand = errors.New(
		`invalid xiangwan HostApplication command`,
	)
	ErrHostApplicationForbidden = errors.New(
		`xiangwan HostApplication command forbidden`,
	)
	ErrHostApplicationRulesUnavailable = errors.New(
		`xiangwan host rules are unavailable`,
	)
	ErrHostApplicationApplicantUnavailable = errors.New(
		`xiangwan HostApplication applicant unavailable`,
	)
	ErrHostApplicationAlreadyHost = errors.New(
		`xiangwan principal already has host identity`,
	)
	ErrHostApplicationTransactionConflict = errors.New(
		`xiangwan HostApplication transaction conflict`,
	)
)

type HostRules struct {
	Configured       bool
	ApplicationCycle string
	PolicyVersion    string
	Requirements     string
	Benefits         string
}

type ApplyHostApplicationCommand struct {
	TenantID                     uuid.UUID
	PrincipalID                  uuid.UUID
	PersonalIntroduction         string
	RelevantExperience           string
	Availability                 string
	ContactMethod                string
	ExpectedCycle                string
	ExpectedPrivacyPolicyVersion string
	ExpectedPolicyVersion        string
	Consent                      bool
}

type ReviewHostApplicationCommand struct {
	TenantID        uuid.UUID
	ApplicationID   uuid.UUID
	ReviewerID      uuid.UUID
	ExpectedVersion int64
	Decision        people.HostApplicationStatus
	Comment         string
}

type WithdrawHostApplicationCommand struct {
	TenantID        uuid.UUID
	ApplicationID   uuid.UUID
	PrincipalID     uuid.UUID
	ExpectedVersion int64
}

type HostApplicationResult struct {
	Application people.HostApplication
	Duplicate   bool
}

func (ApplyHostApplicationCommand) String() string {
	return `xiangwan ApplyHostApplicationCommand{sensitive_fields:[REDACTED]}`
}

func (value ApplyHostApplicationCommand) GoString() string {
	return value.String()
}

func (ReviewHostApplicationCommand) String() string {
	return `xiangwan ReviewHostApplicationCommand{sensitive_fields:[REDACTED]}`
}

func (value ReviewHostApplicationCommand) GoString() string {
	return value.String()
}

func (WithdrawHostApplicationCommand) String() string {
	return `xiangwan WithdrawHostApplicationCommand{sensitive_fields:[REDACTED]}`
}

func (value WithdrawHostApplicationCommand) GoString() string {
	return value.String()
}

func (HostApplicationResult) String() string {
	return `xiangwan HostApplicationResult{sensitive_fields:[REDACTED]}`
}

func (value HostApplicationResult) GoString() string {
	return value.String()
}

// HostApplicationAuthorizationQuery lets customer-specific authorization and
// rules adapters read current PostgreSQL facts through the same transaction.
// Implementations must not cache grants or policy versions in Redis.
type HostApplicationAuthorizationQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type HostApplicationApplicantAuthorizer interface {
	AuthorizeHostApplicationApplicant(
		context.Context,
		HostApplicationAuthorizationQuery,
		uuid.UUID,
		uuid.UUID,
	) error
}

type HostApplicationReviewerAuthorizer interface {
	AuthorizeHostApplicationReview(
		context.Context,
		HostApplicationAuthorizationQuery,
		uuid.UUID,
		uuid.UUID,
	) error
}

type HostRulesProvider interface {
	CurrentHostRules(
		context.Context,
		HostApplicationAuthorizationQuery,
		uuid.UUID,
	) (HostRules, error)
}

// HostApplicationWriter owns self-service submission, withdrawal, and
// authorized review. Every command uses a serializable PostgreSQL transaction;
// the Principal row serializes submissions for one user without a Redis lock.
type HostApplicationWriter struct {
	transactions         hostApplicationTransactionStarter
	applicants           HostApplicationApplicantAuthorizer
	reviewers            HostApplicationReviewerAuthorizer
	rules                HostRulesProvider
	now                  func() time.Time
	generationID         uuid.UUID
	privacyPolicyVersion string
}

func NewHostApplicationWriter(
	db *sql.DB,
	applicants HostApplicationApplicantAuthorizer,
	reviewers HostApplicationReviewerAuthorizer,
	rules HostRulesProvider,
) *HostApplicationWriter {
	return &HostApplicationWriter{
		transactions: sqlHostApplicationTransactionStarter{db: db},
		applicants:   applicants,
		reviewers:    reviewers,
		rules:        rules,
		now:          time.Now,
	}
}

// NewFencedHostApplicationWriter is the production constructor. Rules and
// explicit privacy consent must match the version presented to the applicant.
func NewFencedHostApplicationWriter(db *sql.DB, applicants HostApplicationApplicantAuthorizer, rules HostRulesProvider, generation uuid.UUID, privacyVersion string) *HostApplicationWriter {
	w := NewHostApplicationWriter(db, applicants, nil, rules)
	w.generationID = generation
	w.privacyPolicyVersion = privacyVersion
	return w
}

func (writer *HostApplicationWriter) Apply(
	ctx context.Context,
	command ApplyHostApplicationCommand,
) (HostApplicationResult, error) {
	if err := validateApplyHostApplicationCommand(writer, command); err != nil {
		return HostApplicationResult{}, err
	}
	tx, err := writer.begin(ctx)
	if err != nil {
		return HostApplicationResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := writer.fence(ctx, tx, command.TenantID); err != nil {
		return HostApplicationResult{}, err
	}
	query := tx.authorizationQuery()
	if err := writer.applicants.AuthorizeHostApplicationApplicant(
		ctx,
		query,
		command.TenantID,
		command.PrincipalID,
	); err != nil {
		return HostApplicationResult{}, err
	}
	if err := tx.lockPrincipal(ctx, command.PrincipalID); err != nil {
		return HostApplicationResult{}, err
	}
	if writer.generationID != uuid.Nil {
		existing, err := tx.getActiveByCycleForUpdate(ctx, command.TenantID, command.PrincipalID, command.ExpectedCycle)
		if err == nil {
			if existing.PolicyVersion != command.ExpectedPolicyVersion {
				return HostApplicationResult{}, ErrHostApplicationVersionConflict
			}
			if err := tx.Commit(); err != nil {
				return HostApplicationResult{}, classifyHostApplicationTransactionError(err)
			}
			committed = true
			return HostApplicationResult{Application: existing, Duplicate: true}, nil
		}
		if !errors.Is(err, ErrHostApplicationNotFound) {
			return HostApplicationResult{}, err
		}
		var retainedID uuid.UUID
		native := tx.(*sqlHostApplicationTransaction)
		err = native.tx.QueryRowContext(ctx, `SELECT id FROM xiangwan_host_applications WHERE tenant_id=$1 AND principal_id=$2 AND application_cycle=$3 AND policy_version=$4 AND personal_introduction=$5 AND relevant_experience=$6 AND availability=$7 AND contact_method=$8 ORDER BY submitted_at DESC,id DESC LIMIT 1`, command.TenantID, command.PrincipalID, command.ExpectedCycle, command.ExpectedPolicyVersion, strings.TrimSpace(command.PersonalIntroduction), strings.TrimSpace(command.RelevantExperience), strings.TrimSpace(command.Availability), strings.TrimSpace(command.ContactMethod)).Scan(&retainedID)
		if err == nil {
			retained, err := native.repository.GetHostApplication(ctx, command.TenantID, retainedID)
			if err != nil {
				return HostApplicationResult{}, err
			}
			if err := tx.Commit(); err != nil {
				return HostApplicationResult{}, classifyHostApplicationTransactionError(err)
			}
			committed = true
			return HostApplicationResult{Application: retained, Duplicate: true}, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return HostApplicationResult{}, err
		}
	}
	rules, err := writer.rules.CurrentHostRules(
		ctx,
		query,
		command.TenantID,
	)
	if err != nil {
		return HostApplicationResult{}, fmt.Errorf(
			`load current xiangwan host rules: %w`,
			err,
		)
	}
	if !rules.Configured {
		return HostApplicationResult{}, ErrHostApplicationRulesUnavailable
	}
	if writer.generationID != uuid.Nil && (rules.ApplicationCycle != command.ExpectedCycle || rules.PolicyVersion != command.ExpectedPolicyVersion) {
		return HostApplicationResult{}, ErrHostApplicationVersionConflict
	}
	now := writer.now().UTC().Truncate(time.Microsecond)
	candidate, err := people.NewHostApplication(
		people.NewHostApplicationCommand{
			TenantID:             command.TenantID,
			PrincipalID:          command.PrincipalID,
			ApplicationCycle:     rules.ApplicationCycle,
			PolicyVersion:        rules.PolicyVersion,
			PersonalIntroduction: command.PersonalIntroduction,
			RelevantExperience:   command.RelevantExperience,
			Availability:         command.Availability,
			ContactMethod:        command.ContactMethod,
			SubmittedAt:          now,
		},
	)
	if err != nil {
		return HostApplicationResult{}, fmt.Errorf(
			`%w: %v`,
			ErrInvalidHostApplicationCommand,
			err,
		)
	}

	existing, err := tx.getActiveByCycleForUpdate(
		ctx,
		command.TenantID,
		command.PrincipalID,
		candidate.ApplicationCycle,
	)
	switch {
	case err == nil:
		if err := tx.Commit(); err != nil {
			return HostApplicationResult{},
				classifyHostApplicationTransactionError(err)
		}
		committed = true
		return HostApplicationResult{
			Application: existing,
			Duplicate:   true,
		}, nil
	case !errors.Is(err, ErrHostApplicationNotFound):
		return HostApplicationResult{}, err
	}

	alreadyHost, err := tx.hasHostIdentity(
		ctx,
		command.TenantID,
		command.PrincipalID,
	)
	if err != nil {
		return HostApplicationResult{}, err
	}
	if alreadyHost {
		return HostApplicationResult{}, ErrHostApplicationAlreadyHost
	}
	created, err := tx.create(ctx, candidate)
	if err != nil {
		return HostApplicationResult{},
			classifyHostApplicationTransactionError(err)
	}
	if writer.generationID != uuid.Nil {
		native := tx.(*sqlHostApplicationTransaction)
		if _, err := native.tx.ExecContext(ctx, `INSERT INTO xiangwan_host_application_consents(application_id,tenant_id,privacy_policy_version,consented_at) VALUES($1,$2,$3,$4)`, created.ID, command.TenantID, writer.privacyPolicyVersion, created.SubmittedAt); err != nil {
			return HostApplicationResult{}, err
		}
		if err := writer.audit(ctx, native, command.TenantID, command.PrincipalID, created.ID, "host_application.apply", created.SubmittedAt); err != nil {
			return HostApplicationResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return HostApplicationResult{},
			classifyHostApplicationTransactionError(err)
	}
	committed = true
	return HostApplicationResult{Application: created}, nil
}

func (writer *HostApplicationWriter) Review(
	ctx context.Context,
	command ReviewHostApplicationCommand,
) (HostApplicationResult, error) {
	if err := validateReviewHostApplicationCommand(writer, command); err != nil {
		return HostApplicationResult{}, err
	}
	tx, err := writer.begin(ctx)
	if err != nil {
		return HostApplicationResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := writer.reviewers.AuthorizeHostApplicationReview(
		ctx,
		tx.authorizationQuery(),
		command.TenantID,
		command.ReviewerID,
	); err != nil {
		return HostApplicationResult{}, err
	}
	current, err := tx.getForUpdate(
		ctx,
		command.TenantID,
		command.ApplicationID,
	)
	if err != nil {
		return HostApplicationResult{}, err
	}
	if current.ApplicationStatus == people.HostApplicationStatusPending &&
		current.Version != command.ExpectedVersion {
		return HostApplicationResult{},
			ErrHostApplicationVersionConflict
	}
	updated, duplicate, err := people.ReviewHostApplication(
		current,
		people.ReviewHostApplicationCommand{
			Decision: command.Decision,
			ActorID:  command.ReviewerID,
			Comment:  command.Comment,
			At: writer.now().UTC().Truncate(
				time.Microsecond,
			),
		},
	)
	if err != nil {
		return HostApplicationResult{}, err
	}
	if !duplicate {
		updated, err = tx.update(
			ctx,
			updated,
			command.ExpectedVersion,
		)
		if err != nil {
			return HostApplicationResult{},
				classifyHostApplicationTransactionError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return HostApplicationResult{},
			classifyHostApplicationTransactionError(err)
	}
	committed = true
	return HostApplicationResult{
		Application: updated,
		Duplicate:   duplicate,
	}, nil
}

func (writer *HostApplicationWriter) Withdraw(
	ctx context.Context,
	command WithdrawHostApplicationCommand,
) (HostApplicationResult, error) {
	if err := validateWithdrawHostApplicationCommand(writer, command); err != nil {
		return HostApplicationResult{}, err
	}
	tx, err := writer.begin(ctx)
	if err != nil {
		return HostApplicationResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := writer.fence(ctx, tx, command.TenantID); err != nil {
		return HostApplicationResult{}, err
	}
	if writer.generationID != uuid.Nil {
		if err := tx.lockPrincipal(ctx, command.PrincipalID); err != nil {
			return HostApplicationResult{}, err
		}
	}
	if err := writer.applicants.AuthorizeHostApplicationApplicant(
		ctx,
		tx.authorizationQuery(),
		command.TenantID,
		command.PrincipalID,
	); err != nil {
		return HostApplicationResult{}, err
	}
	current, err := tx.getForPrincipalForUpdate(
		ctx,
		command.TenantID,
		command.ApplicationID,
		command.PrincipalID,
	)
	if err != nil {
		return HostApplicationResult{}, err
	}
	if current.ApplicationStatus == people.HostApplicationStatusPending &&
		current.Version != command.ExpectedVersion {
		return HostApplicationResult{},
			ErrHostApplicationVersionConflict
	}
	updated, duplicate, err := people.WithdrawHostApplication(
		current,
		command.PrincipalID,
		writer.now().UTC().Truncate(time.Microsecond),
	)
	if err != nil {
		return HostApplicationResult{}, err
	}
	if !duplicate {
		updated, err = tx.update(
			ctx,
			updated,
			command.ExpectedVersion,
		)
		if err != nil {
			return HostApplicationResult{},
				classifyHostApplicationTransactionError(err)
		}
	}
	if writer.generationID != uuid.Nil && !duplicate {
		if err := writer.audit(ctx, tx.(*sqlHostApplicationTransaction), command.TenantID, command.PrincipalID, updated.ID, "host_application.withdraw", updated.UpdatedAt); err != nil {
			return HostApplicationResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return HostApplicationResult{},
			classifyHostApplicationTransactionError(err)
	}
	committed = true
	return HostApplicationResult{
		Application: updated,
		Duplicate:   duplicate,
	}, nil
}

func (writer *HostApplicationWriter) fence(ctx context.Context, tx hostApplicationTransaction, tenant uuid.UUID) error {
	if writer.generationID == uuid.Nil {
		return nil
	}
	native, ok := tx.(*sqlHostApplicationTransaction)
	if !ok {
		return ErrHostApplicationForbidden
	}
	var active bool
	if err := native.tx.QueryRowContext(ctx, `SELECT active_generation_id=$2 AND bootstrap_completed_at IS NOT NULL FROM xiangwan_runtime_generations WHERE tenant_id=$1 FOR SHARE`, tenant, writer.generationID).Scan(&active); err != nil || !active {
		return ErrHostApplicationForbidden
	}
	// Serialize rule publication with submission without retaining a policy cache.
	if _, err := native.tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "xiangwan:host-rules:"+tenant.String()); err != nil {
		return err
	}
	return nil
}
func (writer *HostApplicationWriter) audit(ctx context.Context, tx *sqlHostApplicationTransaction, tenant, actor, target uuid.UUID, action string, at time.Time) error {
	_, err := tx.tx.ExecContext(ctx, `INSERT INTO xiangwan_admin_audit_events(id,tenant_id,actor_id,action_code,target_type,target_id,request_id,details,occurred_at,created_at) VALUES($1,$2,$3,$4,'host_application',$5,$6,'{}',$7,$7)`, uuid.New(), tenant, actor, action, target, uuid.NewString(), at)
	return err
}

func (writer *HostApplicationWriter) begin(
	ctx context.Context,
) (hostApplicationTransaction, error) {
	tx, err := writer.transactions.beginHostApplicationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return nil, fmt.Errorf(
			`begin xiangwan HostApplication transaction: %w`,
			err,
		)
	}
	return tx, nil
}

func validateApplyHostApplicationCommand(
	writer *HostApplicationWriter,
	command ApplyHostApplicationCommand,
) error {
	if writer != nil && writer.generationID != uuid.Nil && (!command.Consent || len(command.ExpectedCycle) > 100 || len(command.ExpectedPolicyVersion) > 100 || command.ExpectedCycle == "" || command.ExpectedPolicyVersion == "" || writer.privacyPolicyVersion == "" || command.ExpectedPrivacyPolicyVersion != writer.privacyPolicyVersion) {
		return ErrInvalidHostApplicationCommand
	}
	if writer == nil ||
		writer.transactions == nil ||
		writer.applicants == nil ||
		writer.rules == nil ||
		writer.now == nil ||
		command.TenantID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		strings.TrimSpace(command.PersonalIntroduction) == `` ||
		len([]rune(strings.TrimSpace(command.PersonalIntroduction))) >
			people.MaxHostApplicationIntroductionRunes ||
		strings.TrimSpace(command.RelevantExperience) == `` ||
		len([]rune(strings.TrimSpace(command.RelevantExperience))) >
			people.MaxHostApplicationExperienceRunes ||
		strings.TrimSpace(command.Availability) == `` ||
		len([]rune(strings.TrimSpace(command.Availability))) >
			people.MaxHostApplicationAvailabilityRunes ||
		strings.TrimSpace(command.ContactMethod) == `` ||
		len([]rune(strings.TrimSpace(command.ContactMethod))) >
			people.MaxHostApplicationContactRunes {
		return ErrInvalidHostApplicationCommand
	}
	return nil
}

func validateReviewHostApplicationCommand(
	writer *HostApplicationWriter,
	command ReviewHostApplicationCommand,
) error {
	if writer == nil ||
		writer.transactions == nil ||
		writer.reviewers == nil ||
		writer.now == nil ||
		command.TenantID == uuid.Nil ||
		command.ApplicationID == uuid.Nil ||
		command.ReviewerID == uuid.Nil ||
		command.ExpectedVersion < 1 ||
		(command.Decision != people.HostApplicationStatusApproved &&
			command.Decision != people.HostApplicationStatusRejected) ||
		strings.TrimSpace(command.Comment) == `` ||
		len([]rune(strings.TrimSpace(command.Comment))) >
			people.MaxHostApplicationReviewRunes {
		return ErrInvalidHostApplicationCommand
	}
	return nil
}

func validateWithdrawHostApplicationCommand(
	writer *HostApplicationWriter,
	command WithdrawHostApplicationCommand,
) error {
	if writer == nil ||
		writer.transactions == nil ||
		writer.applicants == nil ||
		writer.now == nil ||
		command.TenantID == uuid.Nil ||
		command.ApplicationID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		command.ExpectedVersion < 1 {
		return ErrInvalidHostApplicationCommand
	}
	return nil
}

type hostApplicationTransactionStarter interface {
	beginHostApplicationTx(
		context.Context,
		*sql.TxOptions,
	) (hostApplicationTransaction, error)
}

type hostApplicationTransaction interface {
	authorizationQuery() HostApplicationAuthorizationQuery
	lockPrincipal(context.Context, uuid.UUID) error
	getActiveByCycleForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		string,
	) (people.HostApplication, error)
	hasHostIdentity(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	create(
		context.Context,
		people.HostApplication,
	) (people.HostApplication, error)
	getForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (people.HostApplication, error)
	getForPrincipalForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (people.HostApplication, error)
	update(
		context.Context,
		people.HostApplication,
		int64,
	) (people.HostApplication, error)
	Commit() error
	Rollback() error
}

type sqlHostApplicationTransactionStarter struct {
	db *sql.DB
}

func (starter sqlHostApplicationTransactionStarter) beginHostApplicationTx(
	ctx context.Context,
	options *sql.TxOptions,
) (hostApplicationTransaction, error) {
	if starter.db == nil {
		return nil, ErrInvalidHostApplicationCommand
	}
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlHostApplicationTransaction{
		tx:         tx,
		repository: NewRepository(tx),
	}, nil
}

type sqlHostApplicationTransaction struct {
	tx         *sql.Tx
	repository *Repository
}

func (tx *sqlHostApplicationTransaction) authorizationQuery() HostApplicationAuthorizationQuery {
	return tx.tx
}

func (tx *sqlHostApplicationTransaction) lockPrincipal(
	ctx context.Context,
	principalID uuid.UUID,
) error {
	var lockedID uuid.UUID
	err := tx.tx.QueryRowContext(ctx, `
SELECT id
FROM principals
WHERE id = $1
FOR UPDATE
`, principalID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrHostApplicationApplicantUnavailable
	}
	if err != nil {
		return fmt.Errorf(`lock xiangwan host applicant: %w`, err)
	}
	return nil
}

func (tx *sqlHostApplicationTransaction) getActiveByCycleForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	applicationCycle string,
) (people.HostApplication, error) {
	return tx.repository.GetActiveHostApplicationByCycleForUpdate(
		ctx,
		tenantID,
		principalID,
		applicationCycle,
	)
}

func (tx *sqlHostApplicationTransaction) hasHostIdentity(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (bool, error) {
	var result bool
	err := tx.tx.QueryRowContext(ctx, `
SELECT
    EXISTS (
        SELECT 1
        FROM xiangwan_host_applications
        WHERE tenant_id = $1
          AND principal_id = $2
          AND application_status = 'approved'
    )
    OR EXISTS (
        SELECT 1
        FROM xiangwan_instance_role_bindings AS role_binding
        JOIN xiangwan_activity_instances AS activity_instance
          ON activity_instance.tenant_id = role_binding.tenant_id
         AND activity_instance.id = role_binding.instance_id
        WHERE role_binding.tenant_id = $1
          AND role_binding.principal_id = $2
          AND role_binding.role_code = 'host'
          AND role_binding.role_status = 'active'
          AND activity_instance.status IN (
              'draft', 'pending_publish', 'published'
          )
    )
`, tenantID, principalID).Scan(&result)
	if err != nil {
		return false, fmt.Errorf(
			`read xiangwan current host identity: %w`,
			err,
		)
	}
	return result, nil
}

func (tx *sqlHostApplicationTransaction) create(
	ctx context.Context,
	value people.HostApplication,
) (people.HostApplication, error) {
	return tx.repository.CreateHostApplication(ctx, value)
}

func (tx *sqlHostApplicationTransaction) getForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	applicationID uuid.UUID,
) (people.HostApplication, error) {
	return tx.repository.GetHostApplicationForUpdate(
		ctx,
		tenantID,
		applicationID,
	)
}

func (tx *sqlHostApplicationTransaction) getForPrincipalForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	applicationID uuid.UUID,
	principalID uuid.UUID,
) (people.HostApplication, error) {
	return tx.repository.GetHostApplicationForPrincipalForUpdate(
		ctx,
		tenantID,
		applicationID,
		principalID,
	)
}

func (tx *sqlHostApplicationTransaction) update(
	ctx context.Context,
	value people.HostApplication,
	expectedVersion int64,
) (people.HostApplication, error) {
	return tx.repository.UpdateHostApplication(ctx, value, expectedVersion)
}

func (tx *sqlHostApplicationTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlHostApplicationTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func classifyHostApplicationTransactionError(err error) error {
	if errors.Is(err, ErrHostApplicationVersionConflict) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case `23503`, `23505`, `23514`, `40001`, `40P01`:
			return fmt.Errorf(
				`%w: %v`,
				ErrHostApplicationTransactionConflict,
				err,
			)
		}
	}
	return err
}
