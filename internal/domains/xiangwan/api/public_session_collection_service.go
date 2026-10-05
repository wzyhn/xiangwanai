package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

const maxPublicInstanceSessions = 200

var (
	ErrInvalidPublicSessionCollectionService = errors.New(
		"invalid xiangwan public Session collection service",
	)
	ErrInvalidPublicSessionCollectionRequest = errors.New(
		"invalid xiangwan public Session collection request",
	)
	ErrPublicSessionCollectionConflict = errors.New(
		"xiangwan public Session collection conflict",
	)
)

type publicSessionCandidateReader interface {
	ListSeriesSessionRouteCandidates(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]activity.SessionRouteCandidate, error)
	ListInstanceSessionRouteCandidates(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]activity.SessionRouteCandidate, error)
}

type PublicInstanceSession struct {
	SessionID      uuid.UUID
	Title          string
	Status         activity.SessionStatus
	ReviewOnly     bool
	SessionStartAt time.Time
	SortOrder      int
}

type PublicSessionCollectionPage struct {
	BrandStatus activity.BrandLifecycleStatus
	SeriesID    *uuid.UUID
	InstanceID  uuid.UUID
	Route       activity.SessionRouteResolution
	Sessions    []PublicInstanceSession
}

type PublicSessionCollectionService struct {
	tenantID        uuid.UUID
	profileReader   publicHomeProfileReader
	candidateReader publicSessionCandidateReader
}

func NewPublicSessionCollectionService(
	tenantID uuid.UUID,
	profileReader publicHomeProfileReader,
	candidateReader publicSessionCandidateReader,
) (*PublicSessionCollectionService, error) {
	if tenantID == uuid.Nil || profileReader == nil || candidateReader == nil {
		return nil, ErrInvalidPublicSessionCollectionService
	}
	return &PublicSessionCollectionService{
		tenantID:        tenantID,
		profileReader:   profileReader,
		candidateReader: candidateReader,
	}, nil
}

func (service *PublicSessionCollectionService) ReadInstanceSessions(
	ctx context.Context,
	instanceID uuid.UUID,
) (PublicSessionCollectionPage, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.profileReader == nil || service.candidateReader == nil {
		return PublicSessionCollectionPage{},
			ErrInvalidPublicSessionCollectionService
	}
	if instanceID == uuid.Nil {
		return PublicSessionCollectionPage{},
			ErrInvalidPublicSessionCollectionRequest
	}
	profile, err := service.readPublicBrandProfile(ctx)
	if err != nil {
		return PublicSessionCollectionPage{}, err
	}
	candidates, err := service.candidateReader.ListInstanceSessionRouteCandidates(
		ctx,
		service.tenantID,
		instanceID,
	)
	if err != nil {
		return PublicSessionCollectionPage{}, fmt.Errorf(
			"read public Instance Sessions: %w",
			err,
		)
	}
	return buildPublicSessionCollectionPage(
		profile,
		uuid.Nil,
		instanceID,
		candidates,
	)
}

func (service *PublicSessionCollectionService) ReadSeriesSessions(
	ctx context.Context,
	seriesID uuid.UUID,
) (PublicSessionCollectionPage, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.profileReader == nil || service.candidateReader == nil {
		return PublicSessionCollectionPage{},
			ErrInvalidPublicSessionCollectionService
	}
	if seriesID == uuid.Nil {
		return PublicSessionCollectionPage{},
			ErrInvalidPublicSessionCollectionRequest
	}
	profile, err := service.readPublicBrandProfile(ctx)
	if err != nil {
		return PublicSessionCollectionPage{}, err
	}
	candidates, err := service.candidateReader.ListSeriesSessionRouteCandidates(
		ctx,
		service.tenantID,
		seriesID,
	)
	if err != nil {
		return PublicSessionCollectionPage{}, fmt.Errorf(
			"read public Series Sessions: %w",
			err,
		)
	}
	return buildPublicSessionCollectionPage(
		profile,
		seriesID,
		uuid.Nil,
		candidates,
	)
}

func (service *PublicSessionCollectionService) readPublicBrandProfile(
	ctx context.Context,
) (activity.PublicHomeProfile, error) {
	profile, err := service.profileReader.ReadPublicHomeProfile(
		ctx,
		service.tenantID,
	)
	if err != nil {
		return activity.PublicHomeProfile{}, fmt.Errorf(
			"read Session collection BrandProfile: %w",
			err,
		)
	}
	if err := activity.ValidatePublicHomeProfile(profile); err != nil {
		return activity.PublicHomeProfile{}, err
	}
	return profile, nil
}

func buildPublicSessionCollectionPage(
	profile activity.PublicHomeProfile,
	requestedSeriesID uuid.UUID,
	requestedInstanceID uuid.UUID,
	candidates []activity.SessionRouteCandidate,
) (PublicSessionCollectionPage, error) {
	if len(candidates) > maxPublicInstanceSessions {
		return PublicSessionCollectionPage{}, ErrPublicSessionCollectionConflict
	}
	route, err := activity.ResolveSessionRoute(candidates)
	if err != nil {
		return PublicSessionCollectionPage{},
			ErrPublicSessionCollectionConflict
	}
	page := PublicSessionCollectionPage{
		BrandStatus: profile.LifecycleStatus,
		InstanceID:  requestedInstanceID,
		Route:       route,
		Sessions:    make([]PublicInstanceSession, 0, len(candidates)),
	}
	if requestedSeriesID != uuid.Nil {
		seriesID := requestedSeriesID
		page.SeriesID = &seriesID
	}
	if len(candidates) == 0 {
		return page, nil
	}
	actualSeriesID := candidates[0].SeriesID
	actualInstanceID := candidates[0].InstanceID
	if (requestedSeriesID != uuid.Nil && actualSeriesID != requestedSeriesID) ||
		(requestedInstanceID != uuid.Nil && actualInstanceID != requestedInstanceID) {
		return PublicSessionCollectionPage{}, ErrPublicSessionCollectionConflict
	}
	if page.SeriesID == nil {
		page.SeriesID = &actualSeriesID
	}
	if page.InstanceID == uuid.Nil {
		page.InstanceID = actualInstanceID
	}
	byID := make(map[uuid.UUID]activity.SessionRouteCandidate, len(candidates))
	for _, candidate := range candidates {
		byID[candidate.SessionID] = candidate
	}
	orderedIDs := route.CandidateSessionIDs
	if route.SessionID != nil {
		orderedIDs = []uuid.UUID{*route.SessionID}
	}
	for _, sessionID := range orderedIDs {
		candidate, exists := byID[sessionID]
		if !exists || candidate.InstanceID != page.InstanceID ||
			page.SeriesID == nil || candidate.SeriesID != *page.SeriesID ||
			strings.TrimSpace(candidate.SessionTitle) == "" {
			return PublicSessionCollectionPage{},
				ErrPublicSessionCollectionConflict
		}
		page.Sessions = append(page.Sessions, PublicInstanceSession{
			SessionID:      candidate.SessionID,
			Title:          strings.TrimSpace(candidate.SessionTitle),
			Status:         candidate.SessionStatus,
			ReviewOnly:     candidate.ReviewOnly,
			SessionStartAt: candidate.SessionStartAt.UTC(),
			SortOrder:      candidate.SortOrder,
		})
	}
	return page, nil
}
