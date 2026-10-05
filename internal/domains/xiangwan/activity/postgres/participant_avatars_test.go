package activitypostgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestListPublicParticipantAvatarsBatchesConfirmedPublicParticipants(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	firstSession := uuid.New()
	secondSession := uuid.New()
	sessionIDs := []uuid.UUID{firstSession, secondSession}

	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRows{rows: [][]any{
				{firstSession, "/avatars/one.png"},
				{firstSession, "/avatars/two.png"},
				{secondSession, "/avatars/three.png"},
			}}, nil
		},
	}}

	got, err := repository.ListPublicParticipantAvatars(
		context.Background(),
		tenantID,
		sessionIDs,
	)
	if err != nil {
		t.Fatalf("ListPublicParticipantAvatars() error = %v", err)
	}
	want := map[uuid.UUID][]string{
		firstSession:  {"/avatars/one.png", "/avatars/two.png"},
		secondSession: {"/avatars/three.png"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListPublicParticipantAvatars() = %+v, want %+v", got, want)
	}
	for _, fragment := range []string{
		"registration.tenant_id = $1",
		"registration.session_id = ANY($2::UUID[])",
		"registration.participation_status = 'confirmed'",
		"principal.avatar_url <> ''",
		"principal.avatar_file_id IS NOT NULL",
		"/api/v1/xiangwan/avatars/legacy/",
		"/api/v1/files/",
		"/content?%",
		"principal.status = 'active'",
		"principal.deleted_at IS NULL",
		"consumer_profile.occupation_public",
		"consumer_profile.introduction_public",
		"consumer_profile.tags_public",
		"ROW_NUMBER() OVER (",
		"ORDER BY registration.confirmed_at ASC, registration.id ASC",
		"ranked.avatar_rank <= 3",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("avatar query missing %q: %s", fragment, capturedQuery)
		}
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != tenantID ||
		!reflect.DeepEqual(capturedArgs[1], sessionIDs) {
		t.Fatalf("avatar query args = %#v", capturedArgs)
	}
}

func TestListPublicParticipantAvatarsSkipsEmptySelection(t *testing.T) {
	t.Parallel()

	calls := 0
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			calls++
			return &fakeRows{}, nil
		},
	}}
	got, err := repository.ListPublicParticipantAvatars(
		context.Background(),
		uuid.New(),
		nil,
	)
	if err != nil || len(got) != 0 || calls != 0 {
		t.Fatalf("ListPublicParticipantAvatars(empty) = %+v, %v (calls=%d)", got, err, calls)
	}
}

func TestListPublicParticipantAvatarsPropagatesFailures(t *testing.T) {
	t.Parallel()

	queryFailure := errors.New("query failed")
	iterationFailure := errors.New("iteration failed")
	tests := []struct {
		name       string
		executor   *fakeQueryExecutor
		want       error
		wantPrefix string
	}{
		{
			name: "query",
			executor: &fakeQueryExecutor{queryRows: func(string, ...any) (rowsScanner, error) {
				return nil, queryFailure
			}},
			want:       queryFailure,
			wantPrefix: "list xiangwan public participant avatars",
		},
		{
			name: "iterate",
			executor: &fakeQueryExecutor{queryRows: func(string, ...any) (rowsScanner, error) {
				return &fakeRows{err: iterationFailure}, nil
			}},
			want:       iterationFailure,
			wantPrefix: "iterate xiangwan public participant avatars",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := (&Repository{db: test.executor}).ListPublicParticipantAvatars(
				context.Background(),
				uuid.New(),
				[]uuid.UUID{uuid.New()},
			)
			if !errors.Is(err, test.want) || !strings.Contains(err.Error(), test.wantPrefix) {
				t.Fatalf("ListPublicParticipantAvatars() error = %v", err)
			}
		})
	}
}
