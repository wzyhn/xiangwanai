package activitypostgres

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestListMyFavoritesUsesOwnerBoundStableKeyset(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	asOf := time.Date(2026, time.September, 25, 8, 0, 0, 0, time.UTC)
	items := []activity.MyFavoriteItem{
		{
			SeriesID:          uuid.New(),
			Title:             "AI Roundtable",
			SeriesStatus:      activity.SeriesStatusActive,
			FavoriteCount:     9,
			FavoritedAt:       asOf.Add(-time.Minute),
			SessionsAvailable: true,
		},
		{
			SeriesID:      uuid.New(),
			Title:         "Archived Salon",
			SeriesStatus:  activity.SeriesStatusArchived,
			FavoriteCount: 2,
			FavoritedAt:   asOf.Add(-2 * time.Minute),
		},
		{
			SeriesID:      uuid.New(),
			Title:         "Draft Workshop",
			SeriesStatus:  activity.SeriesStatusDraft,
			FavoriteCount: 1,
			FavoritedAt:   asOf.Add(-3 * time.Minute),
		},
	}
	var firstQuery string
	var firstArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			firstQuery = query
			firstArgs = append([]any(nil), args...)
			return &fakeRows{rows: [][]any{
				myFavoriteScanValues(items[0]),
				myFavoriteScanValues(items[1]),
				myFavoriteScanValues(items[2]),
			}}, nil
		},
	}}

	page, err := repository.ListMyFavorites(context.Background(), activity.MyFavoriteFilter{
		TenantID:    tenantID,
		PrincipalID: principalID,
		Limit:       2,
		At:          asOf,
	})
	if err != nil {
		t.Fatalf("ListMyFavorites() error = %v", err)
	}
	if !reflect.DeepEqual(page.Items, items[:2]) || page.AsOf != asOf ||
		page.NextCursor == "" {
		t.Fatalf("ListMyFavorites() = %+v", page)
	}
	for _, fragment := range []string{
		"favorite.tenant_id = $1",
		"favorite.principal_id = $2",
		"favorite.created_at <= $3",
		"status = 'active'",
		"deleted_at IS NULL",
		"ORDER BY favorite.created_at DESC, favorite.series_id DESC",
		"LIMIT $4",
	} {
		if !strings.Contains(firstQuery, fragment) {
			t.Fatalf("My Favorites query missing %q: %s", fragment, firstQuery)
		}
	}
	if strings.Contains(firstQuery, "favorite.id") {
		t.Fatalf("internal Favorite identity entered projection: %s", firstQuery)
	}
	if !reflect.DeepEqual(firstArgs, []any{tenantID, principalID, asOf, 3}) {
		t.Fatalf("first-page args = %#v", firstArgs)
	}
	decodedCursor, err := base64.RawURLEncoding.DecodeString(page.NextCursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	for _, forbidden := range []string{
		tenantID.String(),
		principalID.String(),
		"tenant_id",
		"principal_id",
		"favorite_id",
	} {
		if strings.Contains(string(decodedCursor), forbidden) {
			t.Fatalf("cursor leaked %q: %s", forbidden, decodedCursor)
		}
	}

	var nextQuery string
	var nextArgs []any
	nextRepository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			nextQuery = query
			nextArgs = append([]any(nil), args...)
			return &fakeRows{}, nil
		},
	}}
	nextPage, err := nextRepository.ListMyFavorites(
		context.Background(),
		activity.MyFavoriteFilter{
			TenantID:    tenantID,
			PrincipalID: principalID,
			Cursor:      page.NextCursor,
			Limit:       2,
			At:          asOf.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf("ListMyFavorites(next) error = %v", err)
	}
	if nextPage.AsOf != asOf || nextPage.Items == nil {
		t.Fatalf("ListMyFavorites(next) = %+v", nextPage)
	}
	if !strings.Contains(
		nextQuery,
		"(favorite.created_at, favorite.series_id) < ($4, $5)",
	) || !strings.Contains(nextQuery, "LIMIT $6") {
		t.Fatalf("next-page query = %s", nextQuery)
	}
	if !reflect.DeepEqual(nextArgs, []any{
		tenantID,
		principalID,
		asOf,
		items[1].FavoritedAt,
		items[1].SeriesID,
		3,
	}) {
		t.Fatalf("next-page args = %#v", nextArgs)
	}
}

func TestListMyFavoritesRejectsInvalidStaleAndCrossOwnerCursors(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	asOf := time.Now().UTC().Truncate(time.Second)
	item := activity.MyFavoriteItem{
		SeriesID:          uuid.New(),
		Title:             "AI Salon",
		SeriesStatus:      activity.SeriesStatusActive,
		FavoriteCount:     1,
		FavoritedAt:       asOf.Add(-time.Minute),
		SessionsAvailable: true,
	}
	cursor, err := encodeMyFavoritesCursor(activity.MyFavoriteFilter{
		TenantID:    tenantID,
		PrincipalID: principalID,
		At:          asOf,
	}, item)
	if err != nil {
		t.Fatalf("encodeMyFavoritesCursor() error = %v", err)
	}
	tests := []struct {
		name   string
		filter activity.MyFavoriteFilter
		want   error
	}{
		{
			name: "malformed",
			filter: activity.MyFavoriteFilter{
				TenantID: tenantID, PrincipalID: principalID, Cursor: "not/canonical", At: asOf,
			},
			want: ErrInvalidMyFavoritesCursor,
		},
		{
			name: "other owner",
			filter: activity.MyFavoriteFilter{
				TenantID: tenantID, PrincipalID: uuid.New(), Cursor: cursor, At: asOf,
			},
			want: ErrStaleMyFavoritesCursor,
		},
		{
			name: "expired",
			filter: activity.MyFavoriteFilter{
				TenantID:    tenantID,
				PrincipalID: principalID,
				Cursor:      cursor,
				At:          asOf.Add(activity.MaxMyFavoritesCursorAge + time.Second),
			},
			want: ErrStaleMyFavoritesCursor,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, gotErr := normalizeMyFavoritesFilter(test.filter); !errors.Is(
				gotErr,
				test.want,
			) {
				t.Fatalf("normalizeMyFavoritesFilter() error = %v", gotErr)
			}
		})
	}
}

func TestListMyFavoritesRejectsInvalidProjection(t *testing.T) {
	t.Parallel()

	item := activity.MyFavoriteItem{
		SeriesID:          uuid.New(),
		Title:             "AI Salon",
		SeriesStatus:      activity.SeriesStatusActive,
		FavoriteCount:     1,
		FavoritedAt:       time.Now().UTC(),
		SessionsAvailable: true,
	}
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return &fakeRows{rows: [][]any{
				myFavoriteScanValues(item),
				myFavoriteScanValues(item),
			}}, nil
		},
	}}
	_, err := repository.ListMyFavorites(context.Background(), activity.MyFavoriteFilter{
		TenantID: uuid.New(), PrincipalID: uuid.New(), At: time.Now(),
	})
	if !errors.Is(err, ErrMyFavoriteProjection) {
		t.Fatalf("ListMyFavorites(duplicate) error = %v", err)
	}
}

func myFavoriteScanValues(item activity.MyFavoriteItem) []any {
	return []any{
		item.SeriesID,
		item.Title,
		item.SeriesStatus,
		item.FavoriteCount,
		StringArrayJSON{},
		item.FavoritedAt,
		item.SessionsAvailable,
	}
}
