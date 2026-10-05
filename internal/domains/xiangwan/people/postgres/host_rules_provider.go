package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
)

// PostgreSQLHostRulesProvider projects the latest retained approval. Disabled
// versions and missing customer rules keep the application entrance closed.
type PostgreSQLHostRulesProvider struct{}

func (*PostgreSQLHostRulesProvider) CurrentHostRules(ctx context.Context, q HostApplicationAuthorizationQuery, tenant uuid.UUID) (HostRules, error) {
	var r HostRules
	err := q.QueryRowContext(ctx, `SELECT enabled, application_cycle, policy_version, requirements, benefits FROM xiangwan_host_rule_versions WHERE tenant_id=$1 ORDER BY version DESC LIMIT 1`, tenant).Scan(&r.Configured, &r.ApplicationCycle, &r.PolicyVersion, &r.Requirements, &r.Benefits)
	if !r.Configured && err == nil {
		return HostRules{}, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return HostRules{}, nil
	}
	return r, err
}
