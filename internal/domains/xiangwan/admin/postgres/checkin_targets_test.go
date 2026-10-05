package postgres

import (
	"strings"
	"testing"
)

func TestCheckinTargetAuthorizationLockCoversEveryQualifyingGrant(t *testing.T) {
	t.Parallel()

	for _, fragment := range []string{
		"AS MATERIALIZED",
		"admin_grant.capability IN ('super_admin', 'activity_operator')",
		"admin_grant.capability = 'onsite_checkin'",
		"admin_grant.scope_type = 'session'",
		"FOR SHARE OF principal, identity_link, admin_grant",
	} {
		if !strings.Contains(checkinTargetAuthorizationLockSQL, fragment) {
			t.Fatalf("authorization lock query is missing %q", fragment)
		}
	}
	if strings.Contains(checkinTargetAuthorizationLockSQL, "LIMIT 1") {
		t.Fatal("authorization lock query must lock every qualifying Grant")
	}
}
