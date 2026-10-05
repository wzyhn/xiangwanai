package activitypostgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestListPublicFavoriteAvatarsBatchesCurrentPublicRelations(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	firstSeries := uuid.New()
	secondSeries := uuid.New()
	seriesIDs := []uuid.UUID{firstSeries, secondSeries}
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRows{rows: [][]any{
				{firstSeries, "/avatars/new.png"},
				{firstSeries, "/avatars/next.png"},
				{secondSeries, "/avatars/other.png"},
			}}, nil
		},
	}}

	got, err := repository.ListPublicFavoriteAvatars(
		context.Background(), tenantID, seriesIDs,
	)
	if err != nil {
		t.Fatalf("ListPublicFavoriteAvatars() error = %v", err)
	}
	want := map[uuid.UUID][]string{
		firstSeries:  {"/avatars/new.png", "/avatars/next.png"},
		secondSeries: {"/avatars/other.png"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListPublicFavoriteAvatars() = %+v, want %+v", got, want)
	}
	for _, fragment := range []string{
		"favorite.tenant_id = $1",
		"favorite.series_id = ANY($2::UUID[])",
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
		"ORDER BY favorite.created_at DESC, favorite.principal_id DESC",
		"ranked.avatar_rank <= 3",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("favorite avatar query missing %q: %s", fragment, capturedQuery)
		}
	}
	if len(capturedArgs) != 2 || capturedArgs[0] != tenantID ||
		!reflect.DeepEqual(capturedArgs[1], seriesIDs) {
		t.Fatalf("favorite avatar query args = %#v", capturedArgs)
	}
}

func TestListPublicFavoriteAvatarsSkipsEmptySelection(t *testing.T) {
	t.Parallel()
	calls := 0
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			calls++
			return &fakeRows{}, nil
		},
	}}
	got, err := repository.ListPublicFavoriteAvatars(
		context.Background(), uuid.New(), nil,
	)
	if err != nil || len(got) != 0 || calls != 0 {
		t.Fatalf("ListPublicFavoriteAvatars(empty) = %+v, %v (calls=%d)", got, err, calls)
	}
}

func TestListPublicFavoriteAvatarsPropagatesFailures(t *testing.T) {
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
			wantPrefix: "list xiangwan public favorite avatars",
		},
		{
			name: "iterate",
			executor: &fakeQueryExecutor{queryRows: func(string, ...any) (rowsScanner, error) {
				return &fakeRows{err: iterationFailure}, nil
			}},
			want:       iterationFailure,
			wantPrefix: "iterate xiangwan public favorite avatars",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := (&Repository{db: test.executor}).ListPublicFavoriteAvatars(
				context.Background(), uuid.New(), []uuid.UUID{uuid.New()},
			)
			if !errors.Is(err, test.want) || !strings.Contains(err.Error(), test.wantPrefix) {
				t.Fatalf("ListPublicFavoriteAvatars() error = %v", err)
			}
		})
	}
}
