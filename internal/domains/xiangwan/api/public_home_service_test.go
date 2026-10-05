package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

// 空主题的品牌档案发布后,catalog 侧的 clone 会把空列表折叠成 nil,而
// profile 侧读回的是空非 nil 切片——一致性检查必须按语义比较,否则空主题
// 发布让整个首页 500(2026-09-19 生产事故)。
func TestPublicHomeServiceTreatsNilAndEmptyQuickTagsAsEqual(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	profile.AvailableQuickTags = []activity.HomeQuickTag{}
	catalog := activity.HomeCatalog{
		AvailableQuickTags: nil,
		BusinessTimezone:   activity.BusinessTimezone,
		Cards:              nil,
		AsOf:               now,
	}
	service, err := NewPublicHomeService(
		apiUUID(81),
		&fakePublicHomeProfileReader{profile: profile},
		&fakePublicHomeCatalogReader{catalog: catalog},
		&fakePublicHomeAvatarReader{},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewPublicHomeService() error = %v", err)
	}
	page, err := service.ReadHome(context.Background(), activity.HomeFilter{})
	if err != nil {
		t.Fatalf("ReadHome() error = %v, want nil-vs-empty acceptance", err)
	}
	if len(page.Catalog.AvailableQuickTags) != 0 || len(page.Profile.AvailableQuickTags) != 0 {
		t.Fatalf("quick tags changed during the read: %+v", page)
	}
}

func TestPublicHomeServiceUsesFixedTenantProfileAndClock(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(80)
	now := time.Date(2026, time.September, 14, 4, 30, 0, 0, time.UTC)
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	filter := activity.HomeFilter{
		ActivityType: activity.ActivityTypeAIRoundtable,
		QuickTags:    []string{"ai"},
		Limit:        10,
	}
	wantCatalog := activity.HomeCatalog{
		AvailableQuickTags: profile.AvailableQuickTags,
		BusinessTimezone:   activity.BusinessTimezone,
		Cards:              []activity.HomeCard{},
		AsOf:               now,
	}
	profileReader := &fakePublicHomeProfileReader{profile: profile}
	catalogReader := &fakePublicHomeCatalogReader{catalog: wantCatalog}
	service, err := NewPublicHomeService(
		tenantID,
		profileReader,
		catalogReader,
		&fakePublicHomeAvatarReader{},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewPublicHomeService() error = %v", err)
	}
	page, err := service.ReadHome(context.Background(), filter)
	if err != nil {
		t.Fatalf("ReadHome() error = %v", err)
	}
	if !reflect.DeepEqual(page, PublicHomePage{Profile: profile, Catalog: wantCatalog}) ||
		profileReader.tenantID != tenantID || catalogReader.tenantID != tenantID ||
		!reflect.DeepEqual(catalogReader.filter, filter) ||
		!reflect.DeepEqual(catalogReader.quickTags, profile.AvailableQuickTags) ||
		!catalogReader.now.Equal(now) {
		t.Fatalf("page=%+v profileReader=%+v catalogReader=%+v", page, profileReader, catalogReader)
	}
}

func TestPublicHomeServiceFailsClosed(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	validProfile := validPublicHomeProfile(now.Add(-time.Hour))
	tests := []struct {
		name          string
		profileReader *fakePublicHomeProfileReader
		catalogReader *fakePublicHomeCatalogReader
		wantErr       error
	}{
		{
			name: "profile unavailable",
			profileReader: &fakePublicHomeProfileReader{
				err: activity.ErrPublicHomeProfileUnavailable,
			},
			catalogReader: &fakePublicHomeCatalogReader{},
			wantErr:       activity.ErrPublicHomeProfileUnavailable,
		},
		{
			name: "invalid profile",
			profileReader: &fakePublicHomeProfileReader{
				profile: activity.PublicHomeProfile{},
			},
			catalogReader: &fakePublicHomeCatalogReader{},
			wantErr:       activity.ErrInvalidPublicHomeProfile,
		},
		{
			name:          "catalog error",
			profileReader: &fakePublicHomeProfileReader{profile: validProfile},
			catalogReader: &fakePublicHomeCatalogReader{err: activity.ErrInvalidHomeFilter},
			wantErr:       activity.ErrInvalidHomeFilter,
		},
		{
			name:          "mismatched tags",
			profileReader: &fakePublicHomeProfileReader{profile: validProfile},
			catalogReader: &fakePublicHomeCatalogReader{catalog: activity.HomeCatalog{
				AvailableQuickTags: []activity.HomeQuickTag{},
				BusinessTimezone:   activity.BusinessTimezone,
			}},
			wantErr: ErrPublicHomeResponseConflict,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := NewPublicHomeService(
				apiUUID(81),
				test.profileReader,
				test.catalogReader,
				&fakePublicHomeAvatarReader{},
				func() time.Time { return now },
			)
			if err != nil {
				t.Fatalf("NewPublicHomeService() error = %v", err)
			}
			_, err = service.ReadHome(context.Background(), activity.HomeFilter{})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadHome() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func validPublicHomeProfile(publishedAt time.Time) activity.PublicHomeProfile {
	return activity.PublicHomeProfile{
		LifecycleStatus:    activity.BrandLifecycleActive,
		PublicationVersion: 2,
		CommunityName:      "Xiangwan Tianjin",
		BrandIntro:         "Meet people through thoughtful activities.",
		HeroMode:           activity.HomeHeroModeText,
		HeroEyebrow:        "TIANJIN AI COMMUNITY",
		AvailableQuickTags: []activity.HomeQuickTag{{Code: "ai", Label: "AI"}},
		PublishedAt:        publishedAt,
	}
}

type fakePublicHomeProfileReader struct {
	profile activity.PublicHomeProfile
	err     error

	tenantID uuid.UUID
}

func (fake *fakePublicHomeProfileReader) ReadPublicHomeProfile(
	_ context.Context,
	tenantID uuid.UUID,
) (activity.PublicHomeProfile, error) {
	fake.tenantID = tenantID
	return fake.profile, fake.err
}

func TestPublicHomeServiceBucketsParticipantAvatarsPerSession(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	firstSession := apiUUID(91)
	secondSession := apiUUID(92)
	catalog := activity.HomeCatalog{
		AvailableQuickTags: profile.AvailableQuickTags,
		BusinessTimezone:   activity.BusinessTimezone,
		AsOf:               now,
		Cards: []activity.HomeCard{
			{SessionID: firstSession},
			{SessionID: secondSession},
		},
	}
	avatarReader := &fakePublicHomeAvatarReader{avatars: map[uuid.UUID][]string{
		// Four stored rows must clamp to the public cap of three.
		firstSession: {"/avatars/a.png", "/avatars/b.png", "/avatars/c.png", "/avatars/d.png"},
	}, favorites: map[uuid.UUID][]string{
		apiUUID(93): {"/avatars/favorite-a.png", "/avatars/favorite-b.png", "/avatars/favorite-c.png", "/avatars/favorite-d.png"},
	}}
	catalog.Cards[0].SeriesID = apiUUID(93)
	catalog.Cards[1].SeriesID = apiUUID(94)
	service, err := NewPublicHomeService(
		apiUUID(81),
		&fakePublicHomeProfileReader{profile: profile},
		&fakePublicHomeCatalogReader{catalog: catalog},
		avatarReader,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewPublicHomeService() error = %v", err)
	}
	page, err := service.ReadHome(context.Background(), activity.HomeFilter{})
	if err != nil {
		t.Fatalf("ReadHome() error = %v", err)
	}
	if avatarReader.calls != 1 ||
		!reflect.DeepEqual(avatarReader.sessionIDs, []uuid.UUID{firstSession, secondSession}) {
		t.Fatalf("avatar reader calls=%d sessionIDs=%v", avatarReader.calls, avatarReader.sessionIDs)
	}
	if !reflect.DeepEqual(page.Catalog.Cards[0].ParticipantAvatars,
		[]string{"/avatars/a.png", "/avatars/b.png", "/avatars/c.png"}) ||
		len(page.Catalog.Cards[1].ParticipantAvatars) != 0 {
		t.Fatalf("participant avatars = %+v", page.Catalog.Cards)
	}
	if avatarReader.favoriteCalls != 1 ||
		!reflect.DeepEqual(avatarReader.seriesIDs, []uuid.UUID{apiUUID(93), apiUUID(94)}) ||
		!reflect.DeepEqual(page.Catalog.Cards[0].FavoriteAvatars, []string{
			"/avatars/favorite-a.png", "/avatars/favorite-b.png", "/avatars/favorite-c.png",
		}) || len(page.Catalog.Cards[1].FavoriteAvatars) != 0 {
		t.Fatalf("favorite avatars = %+v (calls=%d seriesIDs=%v)", page.Catalog.Cards, avatarReader.favoriteCalls, avatarReader.seriesIDs)
	}
}

func TestPublicHomeServiceSkipsAvatarReadForEmptyPage(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	catalog := activity.HomeCatalog{
		AvailableQuickTags: profile.AvailableQuickTags,
		BusinessTimezone:   activity.BusinessTimezone,
		Cards:              []activity.HomeCard{},
		AsOf:               now,
	}
	avatarReader := &fakePublicHomeAvatarReader{}
	service, err := NewPublicHomeService(
		apiUUID(81),
		&fakePublicHomeProfileReader{profile: profile},
		&fakePublicHomeCatalogReader{catalog: catalog},
		avatarReader,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewPublicHomeService() error = %v", err)
	}
	if _, err := service.ReadHome(context.Background(), activity.HomeFilter{}); err != nil {
		t.Fatalf("ReadHome() error = %v", err)
	}
	if avatarReader.calls != 0 {
		t.Fatalf("avatar reader calls = %d, want 0 for an empty page", avatarReader.calls)
	}
}

type fakePublicHomeAvatarReader struct {
	avatars   map[uuid.UUID][]string
	favorites map[uuid.UUID][]string
	err       error

	calls         int
	favoriteCalls int
	tenantID      uuid.UUID
	sessionIDs    []uuid.UUID
	seriesIDs     []uuid.UUID
}

func (fake *fakePublicHomeAvatarReader) ListPublicParticipantAvatars(
	_ context.Context,
	tenantID uuid.UUID,
	sessionIDs []uuid.UUID,
) (map[uuid.UUID][]string, error) {
	fake.calls++
	fake.tenantID = tenantID
	fake.sessionIDs = append([]uuid.UUID(nil), sessionIDs...)
	if fake.avatars == nil {
		return map[uuid.UUID][]string{}, fake.err
	}
	return fake.avatars, fake.err
}

func (fake *fakePublicHomeAvatarReader) ListPublicFavoriteAvatars(
	_ context.Context,
	tenantID uuid.UUID,
	seriesIDs []uuid.UUID,
) (map[uuid.UUID][]string, error) {
	fake.favoriteCalls++
	fake.tenantID = tenantID
	fake.seriesIDs = append([]uuid.UUID(nil), seriesIDs...)
	if fake.favorites == nil {
		return map[uuid.UUID][]string{}, fake.err
	}
	return fake.favorites, fake.err
}

type fakePublicHomeCatalogReader struct {
	catalog activity.HomeCatalog
	err     error

	tenantID  uuid.UUID
	filter    activity.HomeFilter
	quickTags []activity.HomeQuickTag
	now       time.Time
}

func (fake *fakePublicHomeCatalogReader) ReadHomeCatalog(
	_ context.Context,
	tenantID uuid.UUID,
	filter activity.HomeFilter,
	quickTags []activity.HomeQuickTag,
	now time.Time,
) (activity.HomeCatalog, error) {
	fake.tenantID = tenantID
	fake.filter = filter
	fake.quickTags = append([]activity.HomeQuickTag(nil), quickTags...)
	fake.now = now
	return fake.catalog, fake.err
}
