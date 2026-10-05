package peoplepostgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ActivePrincipalApplicantAuthorizer rechecks the authenticated consumer
// through the BenefitsReader transaction. It does not cache eligibility.
type ActivePrincipalApplicantAuthorizer struct{}

func NewActivePrincipalApplicantAuthorizer() *ActivePrincipalApplicantAuthorizer {
	return &ActivePrincipalApplicantAuthorizer{}
}

func (*ActivePrincipalApplicantAuthorizer) AuthorizeHostApplicationApplicant(
	ctx context.Context,
	query HostApplicationAuthorizationQuery,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) error {
	if ctx == nil || query == nil || tenantID == uuid.Nil ||
		principalID == uuid.Nil {
		return ErrHostApplicationForbidden
	}
	var active bool
	err := query.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM principals
    WHERE id = $1
      AND primary_tenant_id = $2
      AND status = 'active'
      AND deleted_at IS NULL
)
`, principalID, tenantID).Scan(&active)
	if err != nil {
		return fmt.Errorf("authorize xiangwan host applicant: %w", err)
	}
	if !active {
		return ErrHostApplicationApplicantUnavailable
	}
	return nil
}

// PendingHostRulesProvider is the fail-closed provider used until
// CONFIG-HOST-RULES has an approved versioned PostgreSQL policy source.
type PendingHostRulesProvider struct{}

func (*PendingHostRulesProvider) CurrentHostRules(
	ctx context.Context,
	query HostApplicationAuthorizationQuery,
	tenantID uuid.UUID,
) (HostRules, error) {
	if ctx == nil || query == nil || tenantID == uuid.Nil {
		return HostRules{}, ErrHostApplicationRulesUnavailable
	}
	return HostRules{}, nil
}

var _ HostApplicationApplicantAuthorizer = (*ActivePrincipalApplicantAuthorizer)(nil)
var _ HostRulesProvider = (*PendingHostRulesProvider)(nil)
