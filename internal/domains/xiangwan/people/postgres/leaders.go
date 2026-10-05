package peoplepostgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

var ErrInvalidLeaderQuery = errors.New(
	"invalid xiangwan Instance leader query",
)

// ListInstanceSessionLeaders batch-reads the public leader rows of one
// Instance in a single query: an active Instance role binding joined through
// the active trusted People binding to its published, approved PeopleProfile,
// with the Principal avatar attached when one exists ("" otherwise). The
// product cap is people.MaxSessionLeaders (8); the pre-pass fetches one extra
// row (LIMIT 9) so an invalid or duplicate binding cannot starve the
// projection, and the projection itself truncates the remainder. The
// pre-pass orders by the same role-priority key as people.ProjectSessionLeaders
// (host, invited guest, course instructor, event speaker), then grant time,
// then binding id: with grant-time ordering alone the LIMIT 9 cut could drop
// a host the projection would have displayed first.
func (repository *Repository) ListInstanceSessionLeaders(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) ([]people.SessionLeaderFacts, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || instanceID == uuid.Nil {
		return nil, ErrInvalidLeaderQuery
	}
	rows, err := repository.db.queryContext(ctx, `
SELECT
    role_binding.id,
    role_binding.role_code,
    profile.display_name,
    COALESCE(profile.headline, ''),
    COALESCE(principal.avatar_url, ''),
    role_binding.granted_at
FROM xiangwan_instance_role_bindings AS role_binding
JOIN xiangwan_people_bindings AS people_binding
  ON people_binding.tenant_id = role_binding.tenant_id
 AND people_binding.principal_id = role_binding.principal_id
 AND people_binding.binding_status = 'active'
JOIN xiangwan_people_profiles AS profile
  ON profile.tenant_id = people_binding.tenant_id
 AND profile.id = people_binding.people_profile_id
 AND profile.profile_status = 'published'
 AND profile.moderation_status = 'approved'
LEFT JOIN principals AS principal
  ON principal.id = role_binding.principal_id
WHERE role_binding.tenant_id = $1
  AND role_binding.instance_id = $2
  AND role_binding.role_status = 'active'
ORDER BY
    CASE role_binding.role_code
        WHEN 'host' THEN 0
        WHEN 'invited_guest' THEN 1
        WHEN 'course_instructor' THEN 2
        WHEN 'event_speaker' THEN 3
    END ASC,
    role_binding.granted_at ASC,
    role_binding.id ASC
LIMIT 9
`, tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan Instance Session leaders: %w", err)
	}
	defer rows.Close()
	leaders := make([]people.SessionLeaderFacts, 0)
	for rows.Next() {
		var leader people.SessionLeaderFacts
		if scanErr := rows.Scan(
			&leader.BindingID,
			&leader.RoleCode,
			&leader.DisplayName,
			&leader.Headline,
			&leader.AvatarURL,
			&leader.GrantedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan xiangwan Instance Session leader: %w", scanErr)
		}
		leaders = append(leaders, leader)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan Instance Session leaders: %w", err)
	}
	return leaders, nil
}
