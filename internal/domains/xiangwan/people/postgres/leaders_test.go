package peoplepostgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

// The public Session detail leader stack is capped at
// people.MaxSessionLeaders; the SQL pre-pass must fetch exactly one extra row
// so a single invalid or duplicate binding cannot starve the projection. The
// pre-pass must also order by the domain's role-priority key before grant
// time: truncating at LIMIT 9 in grant order alone could cut a host that the
// projection would have displayed first.
func TestListInstanceSessionLeadersQueryIsBounded(t *testing.T) {
	t.Parallel()

	var capturedQuery string
	granted := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			return newFakeRows([]any{
				uuid.New(),
				people.InstanceRoleHost,
				"主理",
				"",
				"",
				granted,
			}), nil
		},
	}}
	leaders, err := repository.ListInstanceSessionLeaders(
		context.Background(), uuid.New(), uuid.New(),
	)
	if err != nil {
		t.Fatalf("ListInstanceSessionLeaders() error = %v", err)
	}
	if len(leaders) != 1 || leaders[0].DisplayName != "主理" {
		t.Fatalf("ListInstanceSessionLeaders() = %+v", leaders)
	}
	orderAt := strings.LastIndex(capturedQuery, "ORDER BY")
	limitAt := strings.LastIndex(capturedQuery, "LIMIT 9")
	if orderAt < 0 || limitAt < 0 || orderAt > limitAt {
		t.Fatalf("leaders query must ORDER BY before LIMIT 9:\n%s", capturedQuery)
	}
	caseAt := strings.Index(capturedQuery, "CASE role_binding.role_code")
	grantAt := strings.Index(capturedQuery, "role_binding.granted_at ASC")
	if caseAt < 0 || grantAt < 0 || caseAt > grantAt ||
		caseAt < orderAt || grantAt < orderAt {
		t.Fatalf("leaders query must order by role priority, then grant time:\n%s",
			capturedQuery)
	}
	for _, fragment := range []string{
		"WHEN 'host' THEN 0",
		"WHEN 'invited_guest' THEN 1",
		"WHEN 'course_instructor' THEN 2",
		"WHEN 'event_speaker' THEN 3",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("leaders role-priority CASE missing %q", fragment)
		}
	}
}

func TestListInstanceSessionLeadersRejectsInvalidQuery(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			t.Fatal("invalid query reached the database")
			return nil, nil
		},
	}}
	if _, err := repository.ListInstanceSessionLeaders(
		context.Background(), uuid.Nil, uuid.New(),
	); err == nil {
		t.Fatal("ListInstanceSessionLeaders() with nil tenant returned nil error")
	}
}
