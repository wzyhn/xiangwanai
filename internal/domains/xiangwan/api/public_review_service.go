package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicReviewService = errors.New(
		"invalid xiangwan public review service",
	)
	ErrInvalidPublicReviewRequest = errors.New(
		"invalid xiangwan public review request",
	)
	ErrPublicReviewResponseConflict = errors.New(
		"xiangwan public review response conflict",
	)
)

type PublicReviewPage struct {
	Activity      activity.PastActivityItem
	ContentBlocks []activity.DetailBlock
	Review        resource.PublicReviewDetail
	NextRoute     activity.NextInstanceRouteResolution
}

type publicPastActivitySummaryReader interface {
	ReadPastActivity(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) (activity.PastActivityItem, error)
	ReadPastActivityDetailBlocks(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) ([]activity.DetailBlock, error)
}

type PublicReviewService struct {
	tenantID        uuid.UUID
	targetResolver  resource.PublicReviewTargetResolver
	summaryReader   publicPastActivitySummaryReader
	reviewReader    resource.PublicReviewReader
	nextRouteReader activity.NextInstanceRouteReader
	externalDomains resource.ExternalDomainPolicy
	clock           func() time.Time
}

func NewPublicReviewService(
	tenantID uuid.UUID,
	targetResolver resource.PublicReviewTargetResolver,
	summaryReader publicPastActivitySummaryReader,
	reviewReader resource.PublicReviewReader,
	nextRouteReader activity.NextInstanceRouteReader,
	externalDomains resource.ExternalDomainPolicy,
	clock func() time.Time,
) (*PublicReviewService, error) {
	if tenantID == uuid.Nil || targetResolver == nil || summaryReader == nil ||
		reviewReader == nil || nextRouteReader == nil || clock == nil {
		return nil, ErrInvalidPublicReviewService
	}
	return &PublicReviewService{
		tenantID:        tenantID,
		targetResolver:  targetResolver,
		summaryReader:   summaryReader,
		reviewReader:    reviewReader,
		nextRouteReader: nextRouteReader,
		externalDomains: externalDomains,
		clock:           clock,
	}, nil
}

func (service *PublicReviewService) ReadInstanceReview(
	ctx context.Context,
	instanceID uuid.UUID,
	sessionID *uuid.UUID,
) (PublicReviewPage, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.targetResolver == nil || service.summaryReader == nil ||
		service.reviewReader == nil || service.nextRouteReader == nil ||
		service.clock == nil {
		return PublicReviewPage{}, ErrInvalidPublicReviewService
	}
	if instanceID == uuid.Nil || (sessionID != nil && *sessionID == uuid.Nil) {
		return PublicReviewPage{}, ErrInvalidPublicReviewRequest
	}
	asOf := service.clock().UTC()
	if asOf.IsZero() {
		return PublicReviewPage{}, ErrInvalidPublicReviewService
	}
	target, err := service.targetResolver.ResolvePublicReviewTarget(
		ctx,
		service.tenantID,
		instanceID,
		sessionID,
		asOf,
	)
	if err != nil {
		return PublicReviewPage{}, fmt.Errorf(
			"resolve public review target: %w",
			err,
		)
	}
	summary, err := service.summaryReader.ReadPastActivity(
		ctx,
		service.tenantID,
		instanceID,
		asOf,
	)
	if err != nil {
		return PublicReviewPage{}, fmt.Errorf(
			"read public review activity summary: %w",
			err,
		)
	}
	if activity.ValidatePastActivityItem(summary) != nil ||
		summary.CompletedAt.After(asOf) ||
		summary.SeriesID != target.SeriesID ||
		summary.InstanceID != target.InstanceID {
		return PublicReviewPage{}, ErrPublicReviewResponseConflict
	}
	contentBlocks, err := service.summaryReader.ReadPastActivityDetailBlocks(
		ctx,
		service.tenantID,
		instanceID,
		asOf,
	)
	if err != nil {
		return PublicReviewPage{}, fmt.Errorf(
			"read public review content blocks: %w",
			err,
		)
	}
	// The PostgreSQL reader already applies ProjectDetailBlocks, but keep the
	// API boundary defensive for alternate implementations and tests: only the
	// typed, normalized text/image shapes may cross the anonymous read surface.
	contentBlocks, err = activity.NormalizeDetailBlocks(contentBlocks)
	if err != nil {
		return PublicReviewPage{}, ErrPublicReviewResponseConflict
	}
	detail, err := service.reviewReader.ReadPublicReview(
		ctx,
		service.tenantID,
		target,
		service.externalDomains,
	)
	if err != nil {
		return PublicReviewPage{}, fmt.Errorf(
			"read public review: %w",
			err,
		)
	}
	nextRoute, err := service.nextRouteReader.ResolveNextInstanceSessionRoute(
		ctx,
		service.tenantID,
		instanceID,
	)
	if err != nil {
		return PublicReviewPage{}, fmt.Errorf(
			"resolve next Instance route: %w",
			err,
		)
	}
	if !publicReviewPageConsistent(summary, target, detail, nextRoute) {
		return PublicReviewPage{}, ErrPublicReviewResponseConflict
	}
	return PublicReviewPage{
		Activity:      summary,
		ContentBlocks: contentBlocks,
		Review:        detail,
		NextRoute:     nextRoute,
	}, nil
}

func publicReviewPageConsistent(
	summary activity.PastActivityItem,
	target resource.PastHighlightReviewTarget,
	detail resource.PublicReviewDetail,
	nextRoute activity.NextInstanceRouteResolution,
) bool {
	if summary.SeriesID != target.SeriesID ||
		summary.InstanceID != target.InstanceID ||
		detail.Target.SeriesID != target.SeriesID ||
		detail.Target.InstanceID != target.InstanceID ||
		!sameOptionalUUID(detail.Target.SessionID, target.SessionID) ||
		nextRoute.SeriesID != target.SeriesID ||
		nextRoute.SourceInstanceID != target.InstanceID ||
		!validNextRouteShape(nextRoute) {
		return false
	}
	for _, document := range detail.Documents {
		switch document.Kind {
		case resource.RelationKindInstanceReview:
			if document.SessionID != nil {
				return false
			}
		case resource.RelationKindSessionResources:
			if target.SessionID == nil || document.SessionID == nil ||
				*document.SessionID != *target.SessionID {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validNextRouteShape(value activity.NextInstanceRouteResolution) bool {
	route := value.SessionRoute
	switch route.Kind {
	case activity.SessionRouteUnavailable:
		return value.TargetInstanceID == nil && route.SessionID == nil &&
			len(route.CandidateSessionIDs) == 0
	case activity.SessionRouteDirect:
		return value.TargetInstanceID != nil &&
			*value.TargetInstanceID != uuid.Nil &&
			*value.TargetInstanceID != value.SourceInstanceID &&
			route.SessionID != nil &&
			*route.SessionID != uuid.Nil && len(route.CandidateSessionIDs) == 0
	case activity.SessionRouteSelectionRequired:
		if value.TargetInstanceID == nil ||
			*value.TargetInstanceID == uuid.Nil ||
			*value.TargetInstanceID == value.SourceInstanceID ||
			route.SessionID != nil ||
			len(route.CandidateSessionIDs) < 2 {
			return false
		}
		seen := make(map[uuid.UUID]struct{}, len(route.CandidateSessionIDs))
		for _, sessionID := range route.CandidateSessionIDs {
			if sessionID == uuid.Nil {
				return false
			}
			if _, duplicate := seen[sessionID]; duplicate {
				return false
			}
			seen[sessionID] = struct{}{}
		}
		return true
	default:
		return false
	}
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
