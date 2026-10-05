package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestHostApplicationRepositoryPersistsScopedHistory(t *testing.T) {
	t.Parallel()

	current := knownHostApplication(t)
	reviewed, _, err := people.ReviewHostApplication(
		current,
		people.ReviewHostApplicationCommand{
			Decision: people.HostApplicationStatusApproved,
			ActorID:  uuid.New(),
			Comment:  `approved`,
			At:       current.UpdatedAt.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`ReviewHostApplication() error = %v`, err)
	}

	var queries []string
	var arguments [][]any
	call := 0
	rows := newFakeRows(
		hostApplicationScanValues(reviewed),
		hostApplicationScanValues(current),
	)
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			call++
			switch call {
			case 1, 2, 3:
				return &fakeRow{values: hostApplicationScanValues(current)}
			default:
				return &fakeRow{values: hostApplicationScanValues(reviewed)}
			}
		},
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			return rows, nil
		},
	}}

	created, err := repository.CreateHostApplication(
		context.Background(),
		current,
	)
	if err != nil {
		t.Fatalf(`CreateHostApplication() error = %v`, err)
	}
	active, err := repository.GetActiveHostApplicationByCycleForUpdate(
		context.Background(),
		current.TenantID,
		current.PrincipalID,
		current.ApplicationCycle,
	)
	if err != nil {
		t.Fatalf(`GetActiveHostApplicationByCycleForUpdate() error = %v`, err)
	}
	owned, err := repository.GetHostApplicationForPrincipalForUpdate(
		context.Background(),
		current.TenantID,
		current.ID,
		current.PrincipalID,
	)
	if err != nil {
		t.Fatalf(`GetHostApplicationForPrincipalForUpdate() error = %v`, err)
	}
	updated, err := repository.UpdateHostApplication(
		context.Background(),
		reviewed,
		current.Version,
	)
	if err != nil {
		t.Fatalf(`UpdateHostApplication() error = %v`, err)
	}
	history, err := repository.ListHostApplicationsByPrincipal(
		context.Background(),
		current.TenantID,
		current.PrincipalID,
	)
	if err != nil {
		t.Fatalf(`ListHostApplicationsByPrincipal() error = %v`, err)
	}
	if !reflect.DeepEqual(created, current) ||
		!reflect.DeepEqual(active, current) ||
		!reflect.DeepEqual(owned, current) ||
		!reflect.DeepEqual(updated, reviewed) ||
		!reflect.DeepEqual(
			history,
			[]people.HostApplication{reviewed, current},
		) ||
		!rows.closed {
		t.Fatalf(
			`HostApplications = %+v %+v %+v %+v %+v`,
			created,
			active,
			owned,
			updated,
			history,
		)
	}
	if len(queries) != 5 ||
		!strings.Contains(queries[0], `INSERT INTO xiangwan_host_applications`) ||
		!strings.Contains(queries[1], `application_cycle = $3`) ||
		!strings.Contains(queries[1], `FOR UPDATE`) ||
		!strings.Contains(queries[2], `principal_id = $3`) ||
		!strings.Contains(queries[3], `version = version + 1`) ||
		!strings.Contains(queries[4], `ORDER BY submitted_at DESC, id DESC`) ||
		len(arguments[0]) != 19 ||
		arguments[3][9] != current.Version {
		t.Fatalf(`HostApplication queries/args = %#v %#v`, queries, arguments)
	}
}

func TestHostApplicationRepositoryMapsMissingAndVersionConflict(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeRow{err: sql.ErrNoRows}
		},
	}}
	if _, err := repository.GetHostApplication(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrHostApplicationNotFound) {
		t.Fatalf(`GetHostApplication() error = %v`, err)
	}
	current := knownHostApplication(t)
	reviewed, _, err := people.ReviewHostApplication(
		current,
		people.ReviewHostApplicationCommand{
			Decision: people.HostApplicationStatusRejected,
			ActorID:  uuid.New(),
			Comment:  `not yet`,
			At:       current.UpdatedAt.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`ReviewHostApplication() error = %v`, err)
	}
	if _, err := repository.UpdateHostApplication(
		context.Background(),
		reviewed,
		current.Version,
	); !errors.Is(err, ErrHostApplicationVersionConflict) {
		t.Fatalf(`UpdateHostApplication() error = %v`, err)
	}
	if _, err := repository.CreateHostApplication(
		context.Background(),
		reviewed,
	); !errors.Is(err, people.ErrInvalidHostApplication) {
		t.Fatalf(`CreateHostApplication(terminal) error = %v`, err)
	}
}

func TestHostApplicationWriterAppliesAndReplaysCurrentCycle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	command := validApplyHostApplicationCommand()
	applicantAuthorizer := &fakeHostApplicantAuthorizer{}
	rules := &fakeHostRulesProvider{rules: HostRules{
		Configured:       true,
		ApplicationCycle: `2026-q4`,
		PolicyVersion:    `host-rules-v3`,
	}}
	tx := &fakeHostApplicationTransaction{
		activeErr: ErrHostApplicationNotFound,
	}
	writer := testHostApplicationWriter(
		&fakeHostApplicationTransactionStarter{tx: tx},
		applicantAuthorizer,
		&fakeHostReviewerAuthorizer{},
		rules,
		now,
	)

	result, err := writer.Apply(context.Background(), command)
	if err != nil {
		t.Fatalf(`Apply() error = %v`, err)
	}
	if result.Duplicate ||
		result.Application.ApplicationStatus !=
			people.HostApplicationStatusPending ||
		result.Application.ApplicationCycle != `2026-q4` ||
		result.Application.PolicyVersion != `host-rules-v3` ||
		result.Application.PrincipalID != command.PrincipalID ||
		!tx.lockedPrincipal ||
		tx.hasHostCalls != 1 ||
		tx.createCalls != 1 ||
		!tx.committed ||
		tx.rolledBack ||
		applicantAuthorizer.calls != 1 ||
		rules.calls != 1 {
		t.Fatalf(`Apply() = %+v tx=%+v`, result, tx)
	}

	replayTx := &fakeHostApplicationTransaction{
		active: result.Application,
	}
	replayWriter := testHostApplicationWriter(
		&fakeHostApplicationTransactionStarter{tx: replayTx},
		&fakeHostApplicantAuthorizer{},
		&fakeHostReviewerAuthorizer{},
		rules,
		now.Add(time.Minute),
	)
	replayed, err := replayWriter.Apply(context.Background(), command)
	if err != nil || !replayed.Duplicate ||
		replayed.Application.ID != result.Application.ID ||
		replayTx.hasHostCalls != 0 ||
		replayTx.createCalls != 0 ||
		!replayTx.committed {
		t.Fatalf(`Apply(replay) = %+v, %v tx=%+v`, replayed, err, replayTx)
	}
}

func TestHostApplicationWriterFailsClosedForRulesHostAndAuthorization(
	t *testing.T,
) {
	t.Parallel()

	command := validApplyHostApplicationCommand()
	now := time.Now().UTC()
	tests := []struct {
		name       string
		authorizer *fakeHostApplicantAuthorizer
		rules      HostRules
		tx         *fakeHostApplicationTransaction
		want       error
	}{
		{
			name: `authorization`,
			authorizer: &fakeHostApplicantAuthorizer{
				err: ErrHostApplicationForbidden,
			},
			rules: HostRules{
				Configured:       true,
				ApplicationCycle: `2026-q4`,
				PolicyVersion:    `host-rules-v3`,
			},
			tx:   &fakeHostApplicationTransaction{},
			want: ErrHostApplicationForbidden,
		},
		{
			name:       `rules unavailable`,
			authorizer: &fakeHostApplicantAuthorizer{},
			rules:      HostRules{},
			tx:         &fakeHostApplicationTransaction{},
			want:       ErrHostApplicationRulesUnavailable,
		},
		{
			name:       `already host`,
			authorizer: &fakeHostApplicantAuthorizer{},
			rules: HostRules{
				Configured:       true,
				ApplicationCycle: `2026-q4`,
				PolicyVersion:    `host-rules-v3`,
			},
			tx: &fakeHostApplicationTransaction{
				activeErr: ErrHostApplicationNotFound,
				hasHost:   true,
			},
			want: ErrHostApplicationAlreadyHost,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writer := testHostApplicationWriter(
				&fakeHostApplicationTransactionStarter{tx: test.tx},
				test.authorizer,
				&fakeHostReviewerAuthorizer{},
				&fakeHostRulesProvider{rules: test.rules},
				now,
			)
			if _, err := writer.Apply(
				context.Background(),
				command,
			); !errors.Is(err, test.want) {
				t.Fatalf(`Apply() error = %v, want %v`, err, test.want)
			}
			if test.tx.committed || !test.tx.rolledBack ||
				test.tx.createCalls != 0 {
				t.Fatalf(`Apply() tx = %+v`, test.tx)
			}
		})
	}
}

func TestHostApplicationWriterReviewsWithdrawsAndReplays(t *testing.T) {
	t.Parallel()

	current := knownHostApplication(t)
	now := current.UpdatedAt.Add(time.Minute)
	reviewerID := uuid.New()
	reviewer := &fakeHostReviewerAuthorizer{}
	reviewTx := &fakeHostApplicationTransaction{current: current}
	writer := testHostApplicationWriter(
		&fakeHostApplicationTransactionStarter{tx: reviewTx},
		&fakeHostApplicantAuthorizer{},
		reviewer,
		&fakeHostRulesProvider{},
		now,
	)
	reviewed, err := writer.Review(
		context.Background(),
		ReviewHostApplicationCommand{
			TenantID:        current.TenantID,
			ApplicationID:   current.ID,
			ReviewerID:      reviewerID,
			ExpectedVersion: current.Version,
			Decision:        people.HostApplicationStatusApproved,
			Comment:         `approved after interview`,
		},
	)
	if err != nil || reviewed.Duplicate ||
		reviewed.Application.ApplicationStatus !=
			people.HostApplicationStatusApproved ||
		reviewTx.updateCalls != 1 ||
		!reviewTx.committed ||
		reviewer.calls != 1 {
		t.Fatalf(`Review() = %+v, %v tx=%+v`, reviewed, err, reviewTx)
	}

	replayTx := &fakeHostApplicationTransaction{
		current: reviewed.Application,
	}
	replayWriter := testHostApplicationWriter(
		&fakeHostApplicationTransactionStarter{tx: replayTx},
		&fakeHostApplicantAuthorizer{},
		&fakeHostReviewerAuthorizer{},
		&fakeHostRulesProvider{},
		now.Add(time.Minute),
	)
	replayed, err := replayWriter.Review(
		context.Background(),
		ReviewHostApplicationCommand{
			TenantID:        current.TenantID,
			ApplicationID:   current.ID,
			ReviewerID:      reviewerID,
			ExpectedVersion: current.Version,
			Decision:        people.HostApplicationStatusApproved,
			Comment:         `approved after interview`,
		},
	)
	if err != nil || !replayed.Duplicate ||
		replayTx.updateCalls != 0 ||
		!replayTx.committed {
		t.Fatalf(`Review(replay) = %+v, %v tx=%+v`, replayed, err, replayTx)
	}

	withdrawable := knownHostApplication(t)
	withdrawTx := &fakeHostApplicationTransaction{owned: withdrawable}
	withdrawWriter := testHostApplicationWriter(
		&fakeHostApplicationTransactionStarter{tx: withdrawTx},
		&fakeHostApplicantAuthorizer{},
		&fakeHostReviewerAuthorizer{},
		&fakeHostRulesProvider{},
		now,
	)
	withdrawn, err := withdrawWriter.Withdraw(
		context.Background(),
		WithdrawHostApplicationCommand{
			TenantID:        withdrawable.TenantID,
			ApplicationID:   withdrawable.ID,
			PrincipalID:     withdrawable.PrincipalID,
			ExpectedVersion: withdrawable.Version,
		},
	)
	if err != nil || withdrawn.Duplicate ||
		withdrawn.Application.ApplicationStatus !=
			people.HostApplicationStatusWithdrawn ||
		withdrawTx.updateCalls != 1 ||
		!withdrawTx.committed {
		t.Fatalf(`Withdraw() = %+v, %v tx=%+v`, withdrawn, err, withdrawTx)
	}
}

func TestHostApplicationWriterRejectsStaleAndInvalidBeforeWriting(t *testing.T) {
	t.Parallel()

	current := knownHostApplication(t)
	tx := &fakeHostApplicationTransaction{current: current}
	starter := &fakeHostApplicationTransactionStarter{tx: tx}
	writer := testHostApplicationWriter(
		starter,
		&fakeHostApplicantAuthorizer{},
		&fakeHostReviewerAuthorizer{},
		&fakeHostRulesProvider{},
		time.Now().UTC(),
	)
	if _, err := writer.Review(
		context.Background(),
		ReviewHostApplicationCommand{
			TenantID:        current.TenantID,
			ApplicationID:   current.ID,
			ReviewerID:      uuid.New(),
			ExpectedVersion: current.Version + 1,
			Decision:        people.HostApplicationStatusApproved,
			Comment:         `approved`,
		},
	); !errors.Is(err, ErrHostApplicationVersionConflict) {
		t.Fatalf(`Review(stale) error = %v`, err)
	}
	if tx.updateCalls != 0 || tx.committed || !tx.rolledBack {
		t.Fatalf(`Review(stale) tx = %+v`, tx)
	}

	invalid := validApplyHostApplicationCommand()
	invalid.ContactMethod = ``
	invalidStarter := &fakeHostApplicationTransactionStarter{
		tx: &fakeHostApplicationTransaction{},
	}
	invalidWriter := testHostApplicationWriter(
		invalidStarter,
		&fakeHostApplicantAuthorizer{},
		&fakeHostReviewerAuthorizer{},
		&fakeHostRulesProvider{},
		time.Now().UTC(),
	)
	if _, err := invalidWriter.Apply(
		context.Background(),
		invalid,
	); !errors.Is(err, ErrInvalidHostApplicationCommand) {
		t.Fatalf(`Apply(invalid) error = %v`, err)
	}
	if invalidStarter.calls != 0 {
		t.Fatalf(`Apply(invalid) began %d transactions`, invalidStarter.calls)
	}
}

func TestClassifyHostApplicationTransactionError(t *testing.T) {
	t.Parallel()

	for _, code := range []string{`23503`, `23505`, `23514`, `40001`, `40P01`} {
		err := classifyHostApplicationTransactionError(
			&pgconn.PgError{Code: code},
		)
		if !errors.Is(err, ErrHostApplicationTransactionConflict) {
			t.Fatalf(`code %s classified as %v`, code, err)
		}
	}
}

func knownHostApplication(t *testing.T) people.HostApplication {
	t.Helper()
	value, err := people.NewHostApplication(
		people.NewHostApplicationCommand{
			TenantID:             uuid.New(),
			PrincipalID:          uuid.New(),
			ApplicationCycle:     `2026-q4`,
			PolicyVersion:        `host-rules-v3`,
			PersonalIntroduction: `Introduction`,
			RelevantExperience:   `Experience`,
			Availability:         `Availability`,
			ContactMethod:        `Contact`,
			SubmittedAt: time.Date(
				2026,
				time.September,
				13,
				12,
				0,
				0,
				0,
				time.UTC,
			),
		},
	)
	if err != nil {
		t.Fatalf(`NewHostApplication() error = %v`, err)
	}
	return value
}

func validApplyHostApplicationCommand() ApplyHostApplicationCommand {
	return ApplyHostApplicationCommand{
		TenantID:             uuid.New(),
		PrincipalID:          uuid.New(),
		PersonalIntroduction: `Introduction`,
		RelevantExperience:   `Experience`,
		Availability:         `Availability`,
		ContactMethod:        `Contact`,
	}
}

func hostApplicationScanValues(value people.HostApplication) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.PrincipalID,
		value.ApplicationCycle,
		value.PolicyVersion,
		value.PersonalIntroduction,
		value.RelevantExperience,
		value.Availability,
		value.ContactMethod,
		value.ApplicationStatus,
		nullUUID(value.ReviewedBy),
		nullTime(value.ReviewedAt),
		nullString(value.ReviewComment),
		nullUUID(value.WithdrawnBy),
		nullTime(value.WithdrawnAt),
		value.Version,
		value.SubmittedAt,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

type fakeHostApplicationTransactionStarter struct {
	tx      hostApplicationTransaction
	err     error
	calls   int
	options *sql.TxOptions
}

func (starter *fakeHostApplicationTransactionStarter) beginHostApplicationTx(
	_ context.Context,
	options *sql.TxOptions,
) (hostApplicationTransaction, error) {
	starter.calls++
	starter.options = options
	return starter.tx, starter.err
}

type fakeHostApplicationTransaction struct {
	active          people.HostApplication
	activeErr       error
	current         people.HostApplication
	currentErr      error
	owned           people.HostApplication
	ownedErr        error
	hasHost         bool
	hasHostErr      error
	createErr       error
	updateErr       error
	commitErr       error
	lockedPrincipal bool
	hasHostCalls    int
	createCalls     int
	updateCalls     int
	committed       bool
	rolledBack      bool
}

func (tx *fakeHostApplicationTransaction) authorizationQuery() HostApplicationAuthorizationQuery {
	return fakeHostAuthorizationQuery{}
}

func (tx *fakeHostApplicationTransaction) lockPrincipal(
	context.Context,
	uuid.UUID,
) error {
	tx.lockedPrincipal = true
	return nil
}

func (tx *fakeHostApplicationTransaction) getActiveByCycleForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	string,
) (people.HostApplication, error) {
	return tx.active, tx.activeErr
}

func (tx *fakeHostApplicationTransaction) hasHostIdentity(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (bool, error) {
	tx.hasHostCalls++
	return tx.hasHost, tx.hasHostErr
}

func (tx *fakeHostApplicationTransaction) create(
	_ context.Context,
	value people.HostApplication,
) (people.HostApplication, error) {
	tx.createCalls++
	return value, tx.createErr
}

func (tx *fakeHostApplicationTransaction) getForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (people.HostApplication, error) {
	return tx.current, tx.currentErr
}

func (tx *fakeHostApplicationTransaction) getForPrincipalForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (people.HostApplication, error) {
	return tx.owned, tx.ownedErr
}

func (tx *fakeHostApplicationTransaction) update(
	_ context.Context,
	value people.HostApplication,
	_ int64,
) (people.HostApplication, error) {
	tx.updateCalls++
	return value, tx.updateErr
}

func (tx *fakeHostApplicationTransaction) Commit() error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeHostApplicationTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

type fakeHostAuthorizationQuery struct{}

func (fakeHostAuthorizationQuery) QueryRowContext(
	context.Context,
	string,
	...any,
) *sql.Row {
	return nil
}

type fakeHostApplicantAuthorizer struct {
	err   error
	calls int
}

func (authorizer *fakeHostApplicantAuthorizer) AuthorizeHostApplicationApplicant(
	context.Context,
	HostApplicationAuthorizationQuery,
	uuid.UUID,
	uuid.UUID,
) error {
	authorizer.calls++
	return authorizer.err
}

type fakeHostReviewerAuthorizer struct {
	err   error
	calls int
}

func (authorizer *fakeHostReviewerAuthorizer) AuthorizeHostApplicationReview(
	context.Context,
	HostApplicationAuthorizationQuery,
	uuid.UUID,
	uuid.UUID,
) error {
	authorizer.calls++
	return authorizer.err
}

type fakeHostRulesProvider struct {
	rules HostRules
	err   error
	calls int
}

func (provider *fakeHostRulesProvider) CurrentHostRules(
	context.Context,
	HostApplicationAuthorizationQuery,
	uuid.UUID,
) (HostRules, error) {
	provider.calls++
	return provider.rules, provider.err
}

func testHostApplicationWriter(
	transactions hostApplicationTransactionStarter,
	applicants HostApplicationApplicantAuthorizer,
	reviewers HostApplicationReviewerAuthorizer,
	rules HostRulesProvider,
	now time.Time,
) *HostApplicationWriter {
	return &HostApplicationWriter{
		transactions: transactions,
		applicants:   applicants,
		reviewers:    reviewers,
		rules:        rules,
		now:          func() time.Time { return now },
	}
}
