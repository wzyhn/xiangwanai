package xiangwanapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestPublicSessionDetailServiceUsesFixedTenantSessionAndClock(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(100)
	sessionID := apiUUID(101)
	now := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	profile.AvailableQuickTags = []activity.HomeQuickTag{{Code: "ai", Label: "AI 社区"}}
	profileReader := &fakePublicHomeProfileReader{profile: profile}
	detailReader := &fakePublicSessionDetailReader{
		detail: validPublicSessionDetail(sessionID, now),
	}
	grantedAt := now.Add(-48 * time.Hour)
	leaderReader := &fakePublicSessionLeaderReader{
		leaders: []people.SessionLeaderFacts{
			{
				BindingID:   apiUUID(130),
				RoleCode:    people.InstanceRoleHost,
				DisplayName: "主理",
				GrantedAt:   grantedAt,
			},
		},
	}
	previousReviewReader := &fakePublicPreviousReviewReader{
		facts: resource.PreviousReviewFacts{
			Instance: &resource.PreviousInstanceFacts{
				InstanceID:  apiUUID(131),
				Title:       "第八期圆桌",
				CompletedAt: now.Add(-7 * 24 * time.Hour),
			},
			Images: []resource.PreviousReviewImageFacts{
				{
					RelationID: apiUUID(132), BlockID: apiUUID(133),
					FileID: apiUUID(134),
				},
				{RelationID: apiUUID(132), BlockID: apiUUID(135), BlockSortOrder: 1,
					ExternalURL: "https://images.example.com/review.jpg"},
			},
		},
	}
	policy, err := resource.NewExternalDomainPolicy([]string{"images.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPublicSessionDetailServiceWithExternalDomains(
		tenantID,
		profileReader,
		detailReader,
		leaderReader,
		previousReviewReader,
		policy,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewPublicSessionDetailService() error = %v", err)
	}
	page, err := service.ReadSessionDetail(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ReadSessionDetail() error = %v", err)
	}
	if page.BrandStatus != activity.BrandLifecycleActive ||
		page.Detail.SessionID != sessionID ||
		page.Detail.CTA.Action != activity.SessionDetailCTAActionStartRegistration ||
		profileReader.tenantID != tenantID || detailReader.tenantID != tenantID ||
		detailReader.sessionID != sessionID || !detailReader.now.Equal(now) {
		t.Fatalf("page=%+v profileReader=%+v detailReader=%+v", page, profileReader, detailReader)
	}
	if len(page.QuickTags) != 1 || page.QuickTags[0].Code != "ai" ||
		page.QuickTags[0].Label != "AI 社区" {
		t.Fatalf("page.QuickTags = %+v", page.QuickTags)
	}
	if len(page.Leaders) != 1 || page.Leaders[0].DisplayName != "主理" ||
		page.Leaders[0].RoleLabel != "主理人" {
		t.Fatalf("page.Leaders = %+v", page.Leaders)
	}
	if leaderReader.tenantID != tenantID ||
		leaderReader.instanceID != page.Detail.InstanceID {
		t.Fatalf("leaderReader = %+v, want tenant %s instance %s",
			leaderReader, tenantID, page.Detail.InstanceID)
	}
	if page.PreviousReview == nil ||
		page.PreviousReview.InstanceID != apiUUID(131) ||
		len(page.PreviousReview.Images) != 2 ||
		page.PreviousReview.Images[0].FileID != apiUUID(134) ||
		page.PreviousReview.Images[1].ExternalURL != "https://images.example.com/review.jpg" {
		t.Fatalf("page.PreviousReview = %+v", page.PreviousReview)
	}
	if previousReviewReader.tenantID != tenantID ||
		previousReviewReader.seriesID != page.Detail.SeriesID ||
		previousReviewReader.instanceID != page.Detail.InstanceID {
		t.Fatalf("previousReviewReader = %+v", previousReviewReader)
	}
}

type fakePastHighlightsReads struct {
	*fakePublicReviewReads
	highlight    *resource.PastHighlightContext
	highlightErr error
	anchorID     uuid.UUID
}

func (fake *fakePastHighlightsReads) ReadPastHighlightContext(_ context.Context, _ uuid.UUID, anchorID uuid.UUID) (*resource.PastHighlightContext, error) {
	fake.anchorID = anchorID
	return fake.highlight, fake.highlightErr
}

func TestPublicSessionPreviousHighlightsKeepExactSessionAndSharedProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := publicReviewAsOf()
	current := validPublicSessionDetail(apiUUID(101), now)
	previousSession := apiUUID(154)
	target := resource.PastHighlightReviewTarget{SeriesID: current.SeriesID, InstanceID: apiUUID(153), SessionID: &previousSession}
	summary := publicReviewSummary(target.SeriesID, target.InstanceID)
	reads := &fakePastHighlightsReads{fakePublicReviewReads: &fakePublicReviewReads{
		summary: summary, detail: resource.PublicReviewDetail{Target: target, Documents: []resource.PublicReviewDocument{
			{RelationID: apiUUID(155), Blocks: []resource.PublicReviewBlock{
				{BlockID: apiUUID(156), Type: resource.PublicReviewBlockTypeImage, SortOrder: 0, ExternalURL: "https://images.example.com/previous.webp", Availability: resource.PublicReviewBlockAvailable},
				{BlockID: apiUUID(157), Type: resource.PublicReviewBlockTypeImage, SortOrder: 1, ExternalURL: "https://denied.example.com/hidden.webp", Availability: resource.PublicReviewBlockPolicyBlocked},
			}},
		}},
	}, highlight: &resource.PastHighlightContext{SeriesID: current.SeriesID, AnchorInstanceID: current.InstanceID,
		PreviousInstanceID: target.InstanceID, FeaturedSessionID: &previousSession,
		PreviousInstancePublishedAt: summary.PublishedAt, PreviousInstanceCompletedAt: summary.CompletedAt}}
	policy, _ := resource.NewExternalDomainPolicy([]string{"images.example.com"})
	service, err := NewPublicSessionDetailServiceWithPastHighlights(apiUUID(100),
		&fakePublicHomeProfileReader{profile: validPublicHomeProfile(now.Add(-time.Hour))},
		&fakePublicSessionDetailReader{detail: current}, &fakePublicSessionLeaderReader{}, reads, reads, policy, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.ReadSessionDetail(ctx, current.SessionID)
	if err != nil || page.PreviousReview == nil || page.PreviousReview.SessionID == nil || *page.PreviousReview.SessionID != previousSession ||
		len(page.PreviousReview.Images) != 1 || page.PreviousReview.Images[0].ExternalURL != "https://images.example.com/previous.webp" ||
		reads.anchorID != current.InstanceID || reads.readTarget.SessionID == nil || *reads.readTarget.SessionID != previousSession {
		t.Fatalf("selected Session preview: %+v %v reads=%+v", page, err, reads)
	}
	response := projectPublicPreviousReview(page.PreviousReview)
	if response.SessionID != previousSession.String() {
		t.Fatalf("navigation target lost: %+v", response)
	}
	reads.detail.Target.SessionID = nil
	if _, err := service.ReadSessionDetail(ctx, current.SessionID); !errors.Is(err, ErrPublicSessionDetailConflict) {
		t.Fatalf("crossed Session accepted: %v", err)
	}
	reads.highlightErr = resource.ErrPastHighlightAnchorUnavailable
	if page, err := service.ReadSessionDetail(ctx, current.SessionID); err != nil || page.PreviousReview != nil {
		t.Fatalf("nonrecurring activity failed: %+v %v", page, err)
	}
}

func TestPublicSessionDetailServiceEmptyExtrasStayEmpty(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionID := apiUUID(107)
	service, err := NewPublicSessionDetailService(
		apiUUID(108),
		&fakePublicHomeProfileReader{profile: validPublicHomeProfile(now.Add(-time.Hour))},
		&fakePublicSessionDetailReader{
			detail: validPublicSessionDetail(sessionID, now),
		},
		&fakePublicSessionLeaderReader{},
		&fakePublicPreviousReviewReader{},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewPublicSessionDetailService() error = %v", err)
	}
	page, err := service.ReadSessionDetail(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ReadSessionDetail() error = %v", err)
	}
	if page.Leaders == nil || len(page.Leaders) != 0 || page.PreviousReview != nil {
		t.Fatalf("page extras = %+v / %+v, want empty leaders and null review",
			page.Leaders, page.PreviousReview)
	}
}

func TestPublicSessionDetailServicePropagatesExtrasReadFailures(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	sessionID := apiUUID(109)
	leaderFailure := errors.New("leader store unavailable")
	reviewFailure := errors.New("review store unavailable")
	tests := []struct {
		name                 string
		leaderReader         *fakePublicSessionLeaderReader
		previousReviewReader *fakePublicPreviousReviewReader
		wantErr              error
	}{
		{
			name:                 "leaders",
			leaderReader:         &fakePublicSessionLeaderReader{err: leaderFailure},
			previousReviewReader: &fakePublicPreviousReviewReader{},
			wantErr:              leaderFailure,
		},
		{
			name:                 "previous review",
			leaderReader:         &fakePublicSessionLeaderReader{},
			previousReviewReader: &fakePublicPreviousReviewReader{err: reviewFailure},
			wantErr:              reviewFailure,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := NewPublicSessionDetailService(
				apiUUID(110),
				&fakePublicHomeProfileReader{profile: profile},
				&fakePublicSessionDetailReader{
					detail: validPublicSessionDetail(sessionID, now),
				},
				test.leaderReader,
				test.previousReviewReader,
				func() time.Time { return now },
			)
			if err != nil {
				t.Fatalf("NewPublicSessionDetailService() error = %v", err)
			}
			_, err = service.ReadSessionDetail(context.Background(), sessionID)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadSessionDetail() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestPublicSessionDetailServiceBlocksCommitmentsWhenBrandIsSuspended(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	profile.LifecycleStatus = activity.BrandLifecycleSuspended
	sessionID := apiUUID(102)
	service, err := NewPublicSessionDetailService(
		apiUUID(103),
		&fakePublicHomeProfileReader{profile: profile},
		&fakePublicSessionDetailReader{
			detail: validPublicSessionDetail(sessionID, now),
		},
		&fakePublicSessionLeaderReader{},
		&fakePublicPreviousReviewReader{},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewPublicSessionDetailService() error = %v", err)
	}
	page, err := service.ReadSessionDetail(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ReadSessionDetail() error = %v", err)
	}
	if page.BrandStatus != activity.BrandLifecycleSuspended ||
		page.Detail.CTA.Enabled ||
		page.Detail.CTA.Action != activity.SessionDetailCTAActionNone ||
		page.Detail.CTA.Label != activity.SessionDetailCTALabelBrandSuspended {
		t.Fatalf("suspended Session page = %+v", page)
	}
}

func TestPublicSessionDetailServiceFailsClosed(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	sessionID := apiUUID(104)
	tests := []struct {
		name          string
		profileReader *fakePublicHomeProfileReader
		detailReader  *fakePublicSessionDetailReader
		requestID     uuid.UUID
		wantErr       error
	}{
		{
			name:          "missing identity",
			profileReader: &fakePublicHomeProfileReader{profile: profile},
			detailReader:  &fakePublicSessionDetailReader{},
			requestID:     uuid.Nil,
			wantErr:       ErrInvalidPublicSessionDetailRequest,
		},
		{
			name: "profile unavailable",
			profileReader: &fakePublicHomeProfileReader{
				err: activity.ErrPublicHomeProfileUnavailable,
			},
			detailReader: &fakePublicSessionDetailReader{},
			requestID:    sessionID,
			wantErr:      activity.ErrPublicHomeProfileUnavailable,
		},
		{
			name:          "detail unavailable",
			profileReader: &fakePublicHomeProfileReader{profile: profile},
			detailReader: &fakePublicSessionDetailReader{
				err: activity.ErrSessionDetailUnavailable,
			},
			requestID: sessionID,
			wantErr:   activity.ErrSessionDetailUnavailable,
		},
		{
			name:          "wrong Session response",
			profileReader: &fakePublicHomeProfileReader{profile: profile},
			detailReader: &fakePublicSessionDetailReader{
				detail: validPublicSessionDetail(apiUUID(105), now),
			},
			requestID: sessionID,
			wantErr:   ErrPublicSessionDetailConflict,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := NewPublicSessionDetailService(
				apiUUID(106),
				test.profileReader,
				test.detailReader,
				&fakePublicSessionLeaderReader{},
				&fakePublicPreviousReviewReader{},
				func() time.Time { return now },
			)
			if err != nil {
				t.Fatalf("NewPublicSessionDetailService() error = %v", err)
			}
			_, err = service.ReadSessionDetail(context.Background(), test.requestID)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadSessionDetail() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func validPublicSessionDetail(
	sessionID uuid.UUID,
	now time.Time,
) activity.SessionDetail {
	return activity.SessionDetail{
		SeriesID:                         apiUUID(110),
		InstanceID:                       apiUUID(111),
		SessionID:                        sessionID,
		PublicationVersion:               2,
		SuccessfulPublishedInstanceCount: 4,
		HistoricalRegistrationCount:      28,
		InstanceTitle:                    "AI Roundtable",
		SessionTitle:                     "Sunday Session",
		ActivityType:                     activity.ActivityTypeAIRoundtable,
		QuickTagCodes:                    []string{"ai"},
		CoverImageURL:                    "https://cdn.example.com/covers/ai-roundtable.jpg",
		RegistrationStartAt:              now.Add(-time.Hour),
		RegistrationEndAt:                now.Add(time.Hour),
		SessionStartAt:                   now.Add(2 * time.Hour),
		SessionEndAt:                     now.Add(4 * time.Hour),
		PriceCents:                       9900,
		DeliveryMode:                     activity.DeliveryModeOffline,
		Area:                             activity.AreaCodeHeping,
		VenueName:                        "Xiangwan Lab",
		Address:                          "Heping District",
		Display: activity.SessionDisplayDecision{
			State:                      activity.DisplayStateOpen,
			Capacity:                   20,
			ConfirmedRegistrationCount: 5,
			SellableCapacity:           15,
		},
		CTA: activity.SessionDetailCTA{
			Action:  activity.SessionDetailCTAActionStartRegistration,
			Label:   activity.SessionDetailCTALabelRegisterNow,
			Enabled: true,
		},
	}
}

type fakePublicSessionDetailReader struct {
	detail activity.SessionDetail
	err    error

	tenantID  uuid.UUID
	sessionID uuid.UUID
	now       time.Time
}

func (fake *fakePublicSessionDetailReader) ReadSessionDetail(
	_ context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	now time.Time,
) (activity.SessionDetail, error) {
	fake.tenantID = tenantID
	fake.sessionID = sessionID
	fake.now = now
	return fake.detail, fake.err
}

type fakePublicSessionLeaderReader struct {
	leaders []people.SessionLeaderFacts
	err     error

	tenantID   uuid.UUID
	instanceID uuid.UUID
}

func (fake *fakePublicSessionLeaderReader) ListInstanceSessionLeaders(
	_ context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) ([]people.SessionLeaderFacts, error) {
	fake.tenantID = tenantID
	fake.instanceID = instanceID
	return fake.leaders, fake.err
}

type fakePublicPreviousReviewReader struct {
	facts resource.PreviousReviewFacts
	err   error

	tenantID   uuid.UUID
	seriesID   uuid.UUID
	instanceID uuid.UUID
}

func (fake *fakePublicPreviousReviewReader) ReadPreviousInstanceReview(
	_ context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (resource.PreviousReviewFacts, error) {
	fake.tenantID = tenantID
	fake.seriesID = seriesID
	fake.instanceID = instanceID
	return fake.facts, fake.err
}
