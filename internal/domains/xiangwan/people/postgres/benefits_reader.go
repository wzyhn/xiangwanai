package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	contributionpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

// BenefitsReader returns one coherent PostgreSQL snapshot for the consumer's
// benefits and identity pages. It deliberately has no PeopleProfile search
// input, so an unbound public profile cannot be inferred as the current user.
type BenefitsReader struct {
	transactions myBenefitsTransactionStarter
	authorizer   HostApplicationApplicantAuthorizer
	rules        HostRulesProvider
}

func NewBenefitsReader(
	db *sql.DB,
	authorizer HostApplicationApplicantAuthorizer,
	rules HostRulesProvider,
) *BenefitsReader {
	return &BenefitsReader{
		transactions: sqlMyBenefitsTransactionStarter{db: db},
		authorizer:   authorizer,
		rules:        rules,
	}
}

func (reader *BenefitsReader) Read(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (people.MyBenefits, error) {
	if reader == nil ||
		reader.transactions == nil ||
		reader.authorizer == nil ||
		reader.rules == nil ||
		tenantID == uuid.Nil ||
		principalID == uuid.Nil {
		return people.MyBenefits{}, ErrInvalidHostApplicationCommand
	}
	tx, err := reader.transactions.beginMyBenefitsTx(
		ctx,
		&sql.TxOptions{
			Isolation: sql.LevelRepeatableRead,
			ReadOnly:  true,
		},
	)
	if err != nil {
		return people.MyBenefits{}, fmt.Errorf(
			`begin xiangwan MyBenefits transaction: %w`,
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := reader.authorizer.AuthorizeHostApplicationApplicant(
		ctx,
		tx.authorizationQuery(),
		tenantID,
		principalID,
	); err != nil {
		return people.MyBenefits{}, err
	}
	var trustedBinding *people.Binding
	binding, err := tx.getActiveBindingByPrincipal(
		ctx,
		tenantID,
		principalID,
	)
	switch {
	case err == nil:
		trustedBinding = &binding
	case errors.Is(err, ErrBindingNotFound):
	default:
		return people.MyBenefits{}, err
	}
	roles, err := tx.listIdentityRoles(ctx, tenantID, principalID)
	if err != nil {
		return people.MyBenefits{}, err
	}
	applications, err := tx.listHostApplications(
		ctx,
		tenantID,
		principalID,
	)
	if err != nil {
		return people.MyBenefits{}, err
	}
	contributionEntries, err := tx.listContributionEntries(
		ctx,
		tenantID,
		principalID,
	)
	if err != nil {
		return people.MyBenefits{}, err
	}
	rules, err := reader.rules.CurrentHostRules(
		ctx,
		tx.authorizationQuery(),
		tenantID,
	)
	if err != nil {
		return people.MyBenefits{}, fmt.Errorf(
			`load xiangwan host rules presentation: %w`,
			err,
		)
	}
	result, err := people.BuildMyBenefits(people.MyBenefitsFacts{
		TenantID:            tenantID,
		PrincipalID:         principalID,
		TrustedBinding:      trustedBinding,
		Roles:               roles,
		HostApplications:    applications,
		ContributionEntries: contributionEntries,
		HostRules: people.HostRulesPresentation{
			Configured:       rules.Configured,
			ApplicationCycle: rules.ApplicationCycle,
			PolicyVersion:    rules.PolicyVersion,
			Requirements:     rules.Requirements,
			Benefits:         rules.Benefits,
		},
	})
	if err != nil {
		return people.MyBenefits{}, err
	}
	if err := tx.Commit(); err != nil {
		return people.MyBenefits{},
			classifyHostApplicationTransactionError(err)
	}
	committed = true
	return result, nil
}

type myBenefitsTransactionStarter interface {
	beginMyBenefitsTx(
		context.Context,
		*sql.TxOptions,
	) (myBenefitsTransaction, error)
}

type myBenefitsTransaction interface {
	authorizationQuery() HostApplicationAuthorizationQuery
	getActiveBindingByPrincipal(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (people.Binding, error)
	listIdentityRoles(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]people.IdentityRole, error)
	listHostApplications(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]people.HostApplication, error)
	listContributionEntries(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]contribution.Entry, error)
	Commit() error
	Rollback() error
}

type sqlMyBenefitsTransactionStarter struct {
	db *sql.DB
}

func (starter sqlMyBenefitsTransactionStarter) beginMyBenefitsTx(
	ctx context.Context,
	options *sql.TxOptions,
) (myBenefitsTransaction, error) {
	if starter.db == nil {
		return nil, ErrInvalidHostApplicationCommand
	}
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlMyBenefitsTransaction{
		tx:                     tx,
		repository:             NewRepository(tx),
		contributionRepository: contributionpostgres.NewRepository(tx),
	}, nil
}

type sqlMyBenefitsTransaction struct {
	tx                     *sql.Tx
	repository             *Repository
	contributionRepository *contributionpostgres.Repository
}

func (tx *sqlMyBenefitsTransaction) authorizationQuery() HostApplicationAuthorizationQuery {
	return tx.tx
}

func (tx *sqlMyBenefitsTransaction) getActiveBindingByPrincipal(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (people.Binding, error) {
	return tx.repository.GetActiveBindingByPrincipal(
		ctx,
		tenantID,
		principalID,
	)
}

func (tx *sqlMyBenefitsTransaction) listIdentityRoles(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]people.IdentityRole, error) {
	return tx.repository.ListInstanceRoleIdentityHistory(
		ctx,
		tenantID,
		principalID,
	)
}

func (tx *sqlMyBenefitsTransaction) listHostApplications(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]people.HostApplication, error) {
	return tx.repository.ListHostApplicationsByPrincipal(
		ctx,
		tenantID,
		principalID,
	)
}

func (tx *sqlMyBenefitsTransaction) listContributionEntries(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]contribution.Entry, error) {
	return tx.contributionRepository.ListByPrincipal(
		ctx,
		tenantID,
		principalID,
	)
}

func (tx *sqlMyBenefitsTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlMyBenefitsTransaction) Rollback() error {
	return tx.tx.Rollback()
}
