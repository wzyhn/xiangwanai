package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

func TestBenefitsReaderReturnsAuthorizedCoherentSnapshot(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	binding := knownBinding(t)
	binding.TenantID = tenantID
	binding.PrincipalID = principalID
	role := people.IdentityRole{
		Binding:        knownRoleBinding(t, people.InstanceRoleHost),
		SeriesTitle:    `AI roundtable`,
		InstanceTitle:  `September`,
		InstanceStatus: activity.InstanceStatusPublished,
	}
	role.Binding.TenantID = tenantID
	role.Binding.PrincipalID = principalID
	application := knownHostApplication(t)
	application.TenantID = tenantID
	application.PrincipalID = principalID
	contributionEntry := knownContributionEntry(t, tenantID, principalID)
	tx := &fakeMyBenefitsTransaction{
		binding:       binding,
		roles:         []people.IdentityRole{role},
		applications:  []people.HostApplication{application},
		contributions: []contribution.Entry{contributionEntry},
	}
	starter := &fakeMyBenefitsTransactionStarter{tx: tx}
	authorizer := &fakeHostApplicantAuthorizer{}
	rules := &fakeHostRulesProvider{rules: HostRules{
		Configured:       true,
		ApplicationCycle: `2026-q4`,
		PolicyVersion:    `host-rules-v3`,
		Requirements:     `Complete an interview.`,
		Benefits:         `Host support and recognition.`,
	}}
	reader := &BenefitsReader{
		transactions: starter,
		authorizer:   authorizer,
		rules:        rules,
	}

	result, err := reader.Read(context.Background(), tenantID, principalID)
	if err != nil {
		t.Fatalf(`Read() error = %v`, err)
	}
	if result.TrustedPeopleProfileID == nil ||
		*result.TrustedPeopleProfileID != binding.PeopleProfileID ||
		!result.HasHostIdentity ||
		result.CanApplyForHost ||
		len(result.CurrentRoles) != 1 ||
		result.CurrentRoles[0].RoleCode != people.InstanceRoleHost ||
		result.CurrentHostApplication == nil ||
		result.CurrentHostApplication.ID != application.ID ||
		result.HostContributionCount != 1 ||
		len(result.HostContributionHistory) != 1 ||
		result.HostRulesState != people.HostRulesStateConfigured ||
		!result.IdentityHistoryAvailable ||
		!tx.committed ||
		tx.rolledBack ||
		authorizer.calls != 1 ||
		rules.calls != 1 ||
		starter.options == nil ||
		starter.options.Isolation != sql.LevelRepeatableRead ||
		!starter.options.ReadOnly {
		t.Fatalf(`Read() = %+v tx=%+v starter=%+v`, result, tx, starter)
	}
}

func TestBenefitsReaderMakesMissingRulesAndBindingExplicit(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	tx := &fakeMyBenefitsTransaction{
		bindingErr: ErrBindingNotFound,
	}
	reader := &BenefitsReader{
		transactions: &fakeMyBenefitsTransactionStarter{tx: tx},
		authorizer:   &fakeHostApplicantAuthorizer{},
		rules:        &fakeHostRulesProvider{},
	}
	result, err := reader.Read(context.Background(), tenantID, principalID)
	if err != nil {
		t.Fatalf(`Read() error = %v`, err)
	}
	if result.TrustedPeopleProfileID != nil ||
		result.HostRulesState != people.HostRulesStatePending ||
		result.CanApplyForHost ||
		result.IdentityHistoryAvailable ||
		!tx.committed {
		t.Fatalf(`Read() = %+v`, result)
	}
}

func TestBenefitsReaderFailsClosedOnAuthorizationOrCrossPrincipalFacts(
	t *testing.T,
) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	forbiddenTx := &fakeMyBenefitsTransaction{}
	forbiddenReader := &BenefitsReader{
		transactions: &fakeMyBenefitsTransactionStarter{tx: forbiddenTx},
		authorizer: &fakeHostApplicantAuthorizer{
			err: ErrHostApplicationForbidden,
		},
		rules: &fakeHostRulesProvider{},
	}
	if _, err := forbiddenReader.Read(
		context.Background(),
		tenantID,
		principalID,
	); !errors.Is(err, ErrHostApplicationForbidden) {
		t.Fatalf(`Read(forbidden) error = %v`, err)
	}
	if forbiddenTx.committed || !forbiddenTx.rolledBack ||
		forbiddenTx.bindingCalls != 0 {
		t.Fatalf(`Read(forbidden) tx = %+v`, forbiddenTx)
	}

	binding := knownBinding(t)
	binding.TenantID = tenantID
	binding.PrincipalID = uuid.New()
	driftTx := &fakeMyBenefitsTransaction{binding: binding}
	driftReader := &BenefitsReader{
		transactions: &fakeMyBenefitsTransactionStarter{tx: driftTx},
		authorizer:   &fakeHostApplicantAuthorizer{},
		rules: &fakeHostRulesProvider{rules: HostRules{
			Configured:       true,
			ApplicationCycle: `2026-q4`,
			PolicyVersion:    `host-rules-v3`,
			Requirements:     `Requirements`,
			Benefits:         `Benefits`,
		}},
	}
	if _, err := driftReader.Read(
		context.Background(),
		tenantID,
		principalID,
	); !errors.Is(err, people.ErrInvalidMyBenefitsFacts) {
		t.Fatalf(`Read(cross-principal) error = %v`, err)
	}
	if driftTx.committed || !driftTx.rolledBack {
		t.Fatalf(`Read(cross-principal) tx = %+v`, driftTx)
	}
}

func TestBenefitsReaderRejectsInvalidIdentityBeforeTransaction(t *testing.T) {
	t.Parallel()

	starter := &fakeMyBenefitsTransactionStarter{
		tx: &fakeMyBenefitsTransaction{},
	}
	reader := &BenefitsReader{
		transactions: starter,
		authorizer:   &fakeHostApplicantAuthorizer{},
		rules:        &fakeHostRulesProvider{},
	}
	if _, err := reader.Read(
		context.Background(),
		uuid.Nil,
		uuid.New(),
	); !errors.Is(err, ErrInvalidHostApplicationCommand) {
		t.Fatalf(`Read(invalid) error = %v`, err)
	}
	if starter.calls != 0 {
		t.Fatalf(`Read(invalid) began %d transactions`, starter.calls)
	}
}

func TestIdentityRoleRepositoryUsesPrincipalScopeAndStableHistoryOrder(
	t *testing.T,
) {
	t.Parallel()

	role := people.IdentityRole{
		Binding:        knownRoleBinding(t, people.InstanceRoleInvitedGuest),
		SeriesTitle:    `Series`,
		InstanceTitle:  `Instance`,
		InstanceStatus: activity.InstanceStatusCompleted,
	}
	var query string
	var arguments []any
	rows := newFakeRows(identityRoleScanValues(role))
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(gotQuery string, args ...any) (rowsScanner, error) {
			query = gotQuery
			arguments = append([]any(nil), args...)
			return rows, nil
		},
	}}
	got, err := repository.ListInstanceRoleIdentityHistory(
		context.Background(),
		role.Binding.TenantID,
		role.Binding.PrincipalID,
	)
	if err != nil ||
		len(got) != 1 ||
		got[0].Binding.ID != role.Binding.ID ||
		got[0].State != people.IdentityRoleStateHistorical ||
		!rows.closed {
		t.Fatalf(`ListInstanceRoleIdentityHistory() = %+v, %v`, got, err)
	}
	if !strings.Contains(query, `JOIN xiangwan_activity_instances`) ||
		!strings.Contains(query, `role_binding.principal_id = $2`) ||
		!strings.Contains(
			query,
			`ORDER BY role_binding.granted_at DESC, role_binding.id DESC`,
		) ||
		!reflect.DeepEqual(
			arguments,
			[]any{role.Binding.TenantID, role.Binding.PrincipalID},
		) {
		t.Fatalf(`identity role query/args = %q %#v`, query, arguments)
	}
}

func identityRoleScanValues(value people.IdentityRole) []any {
	values := roleBindingScanValues(value.Binding)
	return append(
		values,
		value.SeriesTitle,
		value.InstanceTitle,
		value.InstanceStatus,
	)
}

type fakeMyBenefitsTransactionStarter struct {
	tx      myBenefitsTransaction
	err     error
	calls   int
	options *sql.TxOptions
}

func (starter *fakeMyBenefitsTransactionStarter) beginMyBenefitsTx(
	_ context.Context,
	options *sql.TxOptions,
) (myBenefitsTransaction, error) {
	starter.calls++
	starter.options = options
	return starter.tx, starter.err
}

type fakeMyBenefitsTransaction struct {
	binding          people.Binding
	bindingErr       error
	roles            []people.IdentityRole
	rolesErr         error
	applications     []people.HostApplication
	applicationsErr  error
	contributions    []contribution.Entry
	contributionsErr error
	commitErr        error
	bindingCalls     int
	committed        bool
	rolledBack       bool
}

func (tx *fakeMyBenefitsTransaction) listContributionEntries(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) ([]contribution.Entry, error) {
	return append(
		[]contribution.Entry(nil),
		tx.contributions...,
	), tx.contributionsErr
}

func (tx *fakeMyBenefitsTransaction) authorizationQuery() HostApplicationAuthorizationQuery {
	return fakeHostAuthorizationQuery{}
}

func (tx *fakeMyBenefitsTransaction) getActiveBindingByPrincipal(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (people.Binding, error) {
	tx.bindingCalls++
	return tx.binding, tx.bindingErr
}

func (tx *fakeMyBenefitsTransaction) listIdentityRoles(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) ([]people.IdentityRole, error) {
	return append([]people.IdentityRole(nil), tx.roles...), tx.rolesErr
}

func (tx *fakeMyBenefitsTransaction) listHostApplications(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) ([]people.HostApplication, error) {
	return append(
		[]people.HostApplication(nil),
		tx.applications...,
	), tx.applicationsErr
}

func (tx *fakeMyBenefitsTransaction) Commit() error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeMyBenefitsTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

func knownContributionEntry(
	t *testing.T,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) contribution.Entry {
	t.Helper()
	at := time.Now().UTC()
	value, err := contribution.Earn(contribution.EarnCommand{
		TenantID:         tenantID,
		PeopleProfileID:  uuid.New(),
		PeopleBindingID:  uuid.New(),
		PrincipalID:      principalID,
		SeriesID:         uuid.New(),
		InstanceID:       uuid.New(),
		RegistrationID:   uuid.New(),
		SessionID:        uuid.New(),
		CheckinID:        uuid.New(),
		CheckinEventID:   uuid.New(),
		RoleBindingID:    uuid.New(),
		ContributionType: contribution.TypeHostCheckin,
		OccurredAt:       at,
		RecordedAt:       at,
	})
	if err != nil {
		t.Fatalf(`contribution.Earn() error = %v`, err)
	}
	return value
}
