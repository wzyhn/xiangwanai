package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestReadPublicHomeProfileUsesSelectedTenantPublication(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	publishedAt := time.Date(2026, time.September, 12, 5, 0, 0, 0, time.UTC)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: []any{
				activity.BrandLifecycleActive,
				int64(4),
				"Xiangwan Tianjin",
				"Meet people through thoughtful activities.",
				activity.HomeHeroModeText,
				"TIANJIN AI COMMUNITY",
				"Meet offline",
				"",
				"",
				[]byte(`[{"code":"ai","label":"AI"},{"code":"local","label":"Local"}]`),
				publishedAt,
			}}
		},
	}}

	got, err := repository.ReadPublicHomeProfile(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ReadPublicHomeProfile() error = %v", err)
	}
	wantTags := []activity.HomeQuickTag{
		{Code: "ai", Label: "AI"},
		{Code: "local", Label: "Local"},
	}
	if got.LifecycleStatus != activity.BrandLifecycleActive ||
		got.PublicationVersion != 4 || got.CommunityName != "Xiangwan Tianjin" ||
		!reflect.DeepEqual(got.AvailableQuickTags, wantTags) ||
		!got.PublishedAt.Equal(publishedAt) {
		t.Fatalf("ReadPublicHomeProfile() = %+v", got)
	}
	for _, fragment := range []string{
		"publication.tenant_id = brand_profile.tenant_id",
		"publication.publication_version = brand_profile.current_publication_version",
		"brand_profile.tenant_id = $1",
		"brand_profile.lifecycle_status IN ('active', 'suspended')",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("BrandProfile query missing %q: %s", fragment, capturedQuery)
		}
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID}) {
		t.Fatalf("BrandProfile query args = %#v", capturedArgs)
	}
}

func TestReadPublicHomeProfileFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		row     *fakeRow
		wantErr error
	}{
		{
			name:    "not active",
			row:     &fakeRow{err: sql.ErrNoRows},
			wantErr: activity.ErrPublicHomeProfileUnavailable,
		},
		{
			name: "malformed snapshot",
			row: &fakeRow{values: []any{
				activity.BrandLifecycleActive,
				int64(1),
				"Xiangwan",
				"Introduction",
				activity.HomeHeroModeText,
				"TIANJIN AI COMMUNITY",
				"",
				"",
				"",
				[]byte(`[{"code":"Not Valid","label":"AI"}]`),
				time.Now().UTC(),
			}},
			wantErr: activity.ErrInvalidPublicHomeProfile,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(string, ...any) rowScanner { return test.row },
			}}
			_, err := repository.ReadPublicHomeProfile(context.Background(), uuid.New())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadPublicHomeProfile() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
