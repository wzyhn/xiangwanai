package peoplepostgres

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

func TestPublicProfilesUsesModeratedTenantKeyset(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	first := knownReviewedProfile(t)
	second := knownReviewedProfile(t)
	lookahead := knownReviewedProfile(t)
	second.TenantID = first.TenantID
	lookahead.TenantID = first.TenantID
	first.UpdatedAt = asOf.Add(-time.Minute)
	second.UpdatedAt = asOf.Add(-2 * time.Minute)
	lookahead.UpdatedAt = asOf.Add(-3 * time.Minute)

	call := 0
	var queries []string
	var arguments [][]any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			call++
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			if call == 1 {
				return newFakeRows(
					profileScanValues(first),
					profileScanValues(second),
					profileScanValues(lookahead),
				), nil
			}
			return newFakeRows(), nil
		},
	}}

	page, err := repository.ListPublishedProfiles(
		context.Background(),
		people.PublicProfileFilter{
			TenantID: first.TenantID,
			Limit:    2,
			At:       asOf,
		},
	)
	if err != nil {
		t.Fatalf("ListPublishedProfiles() error = %v", err)
	}
	if !page.AsOf.Equal(asOf) || len(page.Items) != 2 ||
		page.Items[0].ID != first.ID || page.Items[1].ID != second.ID ||
		page.NextCursor == "" {
		t.Fatalf("public PeopleProfiles page = %+v", page)
	}
	if !reflect.DeepEqual(arguments[0], []any{
		first.TenantID,
		asOf,
		3,
	}) {
		t.Fatalf("first query args = %#v", arguments[0])
	}
	for _, fragment := range []string{
		"tenant_id = $1",
		"profile_status = 'published'",
		"moderation_status = 'approved'",
		"updated_at <= $2",
		"ORDER BY updated_at DESC, id DESC",
		"LIMIT $3",
	} {
		if !strings.Contains(queries[0], fragment) {
			t.Fatalf("first query does not contain %q", fragment)
		}
	}

	next, err := repository.ListPublishedProfiles(
		context.Background(),
		people.PublicProfileFilter{
			TenantID: first.TenantID,
			Limit:    2,
			Cursor:   page.NextCursor,
			At:       asOf.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf("ListPublishedProfiles(next) error = %v", err)
	}
	if len(next.Items) != 0 || next.NextCursor != "" ||
		!next.AsOf.Equal(asOf) {
		t.Fatalf("next public PeopleProfiles page = %+v", next)
	}
	if len(arguments[1]) != 5 || arguments[1][0] != first.TenantID ||
		!arguments[1][1].(time.Time).Equal(asOf) ||
		!arguments[1][2].(time.Time).Equal(second.UpdatedAt) ||
		arguments[1][3] != second.ID || arguments[1][4] != 3 {
		t.Fatalf("next query args = %#v", arguments[1])
	}
	if !strings.Contains(queries[1], "(updated_at, id) < ($3, $4)") ||
		!strings.Contains(queries[1], "LIMIT $5") {
		t.Fatalf("next query = %q", queries[1])
	}
}

func TestPublicProfilesRejectsInvalidAndCrossTenantCursors(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	profile := knownReviewedProfile(t)
	profile.UpdatedAt = asOf.Add(-time.Minute)
	validFilter := people.PublicProfileFilter{
		TenantID: profile.TenantID,
		Limit:    1,
		At:       asOf,
	}
	cursor, err := encodePublicProfilesCursor(validFilter, profile)
	if err != nil {
		t.Fatalf("encodePublicProfilesCursor() error = %v", err)
	}
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			t.Fatal("invalid request unexpectedly reached PostgreSQL")
			return nil, nil
		},
	}}
	tests := []struct {
		name    string
		filter  people.PublicProfileFilter
		wantErr error
	}{
		{
			name:    "tenant required",
			filter:  people.PublicProfileFilter{},
			wantErr: ErrInvalidPublicProfilesFilter,
		},
		{
			name: "limit too large",
			filter: people.PublicProfileFilter{
				TenantID: profile.TenantID,
				Limit:    people.MaxPublicProfilesLimit + 1,
			},
			wantErr: ErrInvalidPublicProfilesFilter,
		},
		{
			name: "malformed cursor",
			filter: publicProfilesFilterWithCursor(
				validFilter,
				"not-base64!",
				asOf,
			),
			wantErr: ErrInvalidPublicProfilesCursor,
		},
		{
			name: "unknown cursor field",
			filter: publicProfilesFilterWithCursor(
				validFilter,
				base64.RawURLEncoding.EncodeToString(
					[]byte(`{"v":1,"unknown":true}`),
				),
				asOf,
			),
			wantErr: ErrInvalidPublicProfilesCursor,
		},
		{
			name: "tenant changed",
			filter: func() people.PublicProfileFilter {
				value := publicProfilesFilterWithCursor(validFilter, cursor, asOf)
				value.TenantID = uuid.New()
				return value
			}(),
			wantErr: ErrStalePublicProfilesCursor,
		},
		{
			name: "cursor expired",
			filter: publicProfilesFilterWithCursor(
				validFilter,
				cursor,
				asOf.Add(people.MaxPublicProfilesCursorAge+time.Second),
			),
			wantErr: ErrStalePublicProfilesCursor,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, gotErr := repository.ListPublishedProfiles(
				context.Background(),
				test.filter,
			)
			if !errors.Is(gotErr, test.wantErr) {
				t.Fatalf(
					"ListPublishedProfiles() error = %v, want %v",
					gotErr,
					test.wantErr,
				)
			}
		})
	}
}

func TestPublicProfilesRejectsDatabaseProjectionOutsidePublicSet(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	profile := knownReviewedProfile(t)
	profile.UpdatedAt = asOf.Add(time.Second)
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeRows(profileScanValues(profile)), nil
		},
	}}
	_, err := repository.ListPublishedProfiles(
		context.Background(),
		people.PublicProfileFilter{TenantID: profile.TenantID, At: asOf},
	)
	if !errors.Is(err, ErrPublicProfilesProjection) {
		t.Fatalf("ListPublishedProfiles(projection) error = %v", err)
	}
}

func publicProfilesFilterWithCursor(
	filter people.PublicProfileFilter,
	cursor string,
	at time.Time,
) people.PublicProfileFilter {
	filter.Cursor = cursor
	filter.At = at
	return filter
}
