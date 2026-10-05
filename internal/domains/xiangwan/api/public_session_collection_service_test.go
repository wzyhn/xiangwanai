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

func TestPublicSessionCollectionServicePreservesExplicitStableSelection(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(130)
	seriesID := apiUUID(131)
	instanceID := apiUUID(132)
	lateID := apiUUID(133)
	earlyID := apiUUID(134)
	now := time.Date(2026, time.September, 15, 4, 0, 0, 0, time.UTC)
	reader := &fakePublicInstanceSessionCandidateReader{candidates: []activity.SessionRouteCandidate{
		{
			SeriesID:       seriesID,
			InstanceID:     instanceID,
			SessionID:      lateID,
			SessionTitle:   "Evening workshop",
			SessionStatus:  activity.SessionStatusPublished,
			SessionStartAt: now.Add(2 * time.Hour),
			SortOrder:      2,
		},
		{
			SeriesID:       seriesID,
			InstanceID:     instanceID,
			SessionID:      earlyID,
			SessionTitle:   "Morning workshop",
			SessionStatus:  activity.SessionStatusPublished,
			SessionStartAt: now.Add(time.Hour),
			SortOrder:      1,
		},
	}}
	service, err := NewPublicSessionCollectionService(
		tenantID,
		&fakePublicHomeProfileReader{
			profile: validPublicHomeProfile(now.Add(-time.Hour)),
		},
		reader,
	)
	if err != nil {
		t.Fatalf("NewPublicSessionCollectionService() error = %v", err)
	}
	page, err := service.ReadInstanceSessions(context.Background(), instanceID)
	if err != nil {
		t.Fatalf("ReadInstanceSessions() error = %v", err)
	}
	if page.SeriesID == nil || *page.SeriesID != seriesID ||
		page.InstanceID != instanceID ||
		page.Route.Kind != activity.SessionRouteSelectionRequired ||
		page.Route.SessionID != nil ||
		!reflect.DeepEqual(
			page.Route.CandidateSessionIDs,
			[]uuid.UUID{earlyID, lateID},
		) || len(page.Sessions) != 2 ||
		page.Sessions[0].SessionID != earlyID ||
		page.Sessions[1].SessionID != lateID ||
		reader.tenantID != tenantID || reader.instanceID != instanceID {
		t.Fatalf("Session collection page=%+v reader=%+v", page, reader)
	}
}

func TestPublicSessionCollectionServiceResolvesSeriesCurrentPublicInstance(
	t *testing.T,
) {
	t.Parallel()

	tenantID := apiUUID(142)
	seriesID := apiUUID(143)
	instanceID := apiUUID(144)
	sessionID := apiUUID(145)
	now := time.Date(2026, time.September, 15, 5, 0, 0, 0, time.UTC)
	reader := &fakePublicInstanceSessionCandidateReader{
		seriesCandidates: []activity.SessionRouteCandidate{{
			SeriesID:       seriesID,
			InstanceID:     instanceID,
			SessionID:      sessionID,
			SessionTitle:   "Current Session",
			SessionStatus:  activity.SessionStatusPublished,
			SessionStartAt: now.Add(time.Hour),
		}},
	}
	service, err := NewPublicSessionCollectionService(
		tenantID,
		&fakePublicHomeProfileReader{
			profile: validPublicHomeProfile(now.Add(-time.Hour)),
		},
		reader,
	)
	if err != nil {
		t.Fatalf("NewPublicSessionCollectionService() error = %v", err)
	}
	page, err := service.ReadSeriesSessions(context.Background(), seriesID)
	if err != nil {
		t.Fatalf("ReadSeriesSessions() error = %v", err)
	}
	if page.SeriesID == nil || *page.SeriesID != seriesID ||
		page.InstanceID != instanceID ||
		page.Route.Kind != activity.SessionRouteDirect ||
		page.Route.SessionID == nil || *page.Route.SessionID != sessionID ||
		len(page.Sessions) != 1 || page.Sessions[0].SessionID != sessionID ||
		reader.tenantID != tenantID || reader.seriesID != seriesID ||
		reader.instanceID != uuid.Nil {
		t.Fatalf("Series Session collection page=%+v reader=%+v", page, reader)
	}
}

func TestPublicSessionCollectionServiceReturnsDirectOrUnavailableWithoutDefaulting(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	seriesID := apiUUID(135)
	instanceID := apiUUID(136)
	sessionID := apiUUID(137)
	tests := []struct {
		name           string
		candidates     []activity.SessionRouteCandidate
		wantKind       activity.SessionRouteResolutionKind
		wantDirect     bool
		wantReviewOnly bool
	}{
		{name: "unavailable", candidates: nil, wantKind: activity.SessionRouteUnavailable},
		{
			name: "direct",
			candidates: []activity.SessionRouteCandidate{{
				SeriesID:       seriesID,
				InstanceID:     instanceID,
				SessionID:      sessionID,
				SessionTitle:   "Review Session",
				SessionStatus:  activity.SessionStatusEnded,
				ReviewOnly:     true,
				SessionStartAt: now.Add(-time.Hour),
			}},
			wantKind:       activity.SessionRouteDirect,
			wantDirect:     true,
			wantReviewOnly: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := NewPublicSessionCollectionService(
				apiUUID(138),
				&fakePublicHomeProfileReader{
					profile: validPublicHomeProfile(now.Add(-time.Hour)),
				},
				&fakePublicInstanceSessionCandidateReader{candidates: test.candidates},
			)
			if err != nil {
				t.Fatalf("NewPublicSessionCollectionService() error = %v", err)
			}
			page, err := service.ReadInstanceSessions(context.Background(), instanceID)
			if err != nil {
				t.Fatalf("ReadInstanceSessions() error = %v", err)
			}
			if page.Route.Kind != test.wantKind ||
				(page.Route.SessionID != nil) != test.wantDirect ||
				len(page.Sessions) != len(test.candidates) ||
				(len(page.Sessions) == 1 && page.Sessions[0].ReviewOnly != test.wantReviewOnly) {
				t.Fatalf("Session collection page = %+v", page)
			}
		})
	}
}

func TestPublicSessionCollectionServiceFailsClosed(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	instanceID := apiUUID(139)
	tooMany := make([]activity.SessionRouteCandidate, maxPublicInstanceSessions+1)
	for index := range tooMany {
		tooMany[index] = activity.SessionRouteCandidate{
			SeriesID:       apiUUID(140),
			InstanceID:     instanceID,
			SessionID:      uuid.New(),
			SessionTitle:   "Public Session",
			SessionStatus:  activity.SessionStatusPublished,
			SessionStartAt: now.Add(time.Duration(index) * time.Minute),
			SortOrder:      index,
		}
	}
	tests := []struct {
		name          string
		profileReader *fakePublicHomeProfileReader
		reader        *fakePublicInstanceSessionCandidateReader
		instanceID    uuid.UUID
		wantErr       error
	}{
		{
			name:          "invalid identity",
			profileReader: &fakePublicHomeProfileReader{profile: profile},
			reader:        &fakePublicInstanceSessionCandidateReader{},
			instanceID:    uuid.Nil,
			wantErr:       ErrInvalidPublicSessionCollectionRequest,
		},
		{
			name: "profile unavailable",
			profileReader: &fakePublicHomeProfileReader{
				err: activity.ErrPublicHomeProfileUnavailable,
			},
			reader:     &fakePublicInstanceSessionCandidateReader{},
			instanceID: instanceID,
			wantErr:    activity.ErrPublicHomeProfileUnavailable,
		},
		{
			name:          "too many Sessions",
			profileReader: &fakePublicHomeProfileReader{profile: profile},
			reader: &fakePublicInstanceSessionCandidateReader{
				candidates: tooMany,
			},
			instanceID: instanceID,
			wantErr:    ErrPublicSessionCollectionConflict,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service, err := NewPublicSessionCollectionService(
				apiUUID(141),
				test.profileReader,
				test.reader,
			)
			if err != nil {
				t.Fatalf("NewPublicSessionCollectionService() error = %v", err)
			}
			_, err = service.ReadInstanceSessions(context.Background(), test.instanceID)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadInstanceSessions() error=%v want=%v", err, test.wantErr)
			}
		})
	}
}

type fakePublicInstanceSessionCandidateReader struct {
	candidates       []activity.SessionRouteCandidate
	seriesCandidates []activity.SessionRouteCandidate
	err              error

	tenantID   uuid.UUID
	seriesID   uuid.UUID
	instanceID uuid.UUID
}

func (fake *fakePublicInstanceSessionCandidateReader) ListSeriesSessionRouteCandidates(
	_ context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) ([]activity.SessionRouteCandidate, error) {
	fake.tenantID = tenantID
	fake.seriesID = seriesID
	return fake.seriesCandidates, fake.err
}

func (fake *fakePublicInstanceSessionCandidateReader) ListInstanceSessionRouteCandidates(
	_ context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) ([]activity.SessionRouteCandidate, error) {
	fake.tenantID = tenantID
	fake.instanceID = instanceID
	return fake.candidates, fake.err
}
