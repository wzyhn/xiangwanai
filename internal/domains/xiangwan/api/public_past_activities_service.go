package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicPastActivitiesService = errors.New(
		"invalid xiangwan public past activities service",
	)
	ErrInvalidPublicPastActivitiesRequest = errors.New(
		"invalid xiangwan public past activities request",
	)
	ErrPublicPastActivitiesResponseConflict = errors.New(
		"xiangwan public past activities response conflict",
	)
)

type publicPastActivitiesReader interface {
	ListPastActivities(
		context.Context,
		activity.PastActivitiesFilter,
	) (activity.PastActivitiesPage, error)
}

type PublicPastActivitiesRequest struct {
	ActivityType activity.ActivityType
	Cursor       string
	Limit        int
}

type PublicPastActivitiesService struct {
	tenantID uuid.UUID
	reader   publicPastActivitiesReader
	clock    func() time.Time
}

func NewPublicPastActivitiesService(
	tenantID uuid.UUID,
	reader publicPastActivitiesReader,
	clock func() time.Time,
) (*PublicPastActivitiesService, error) {
	if tenantID == uuid.Nil || reader == nil || clock == nil {
		return nil, ErrInvalidPublicPastActivitiesService
	}
	return &PublicPastActivitiesService{
		tenantID: tenantID,
		reader:   reader,
		clock:    clock,
	}, nil
}

func (service *PublicPastActivitiesService) Read(
	ctx context.Context,
	request PublicPastActivitiesRequest,
) (activity.PastActivitiesPage, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.reader == nil || service.clock == nil {
		return activity.PastActivitiesPage{},
			ErrInvalidPublicPastActivitiesService
	}
	if ctx == nil ||
		(request.ActivityType != "" &&
			!activity.ValidPastActivityType(request.ActivityType)) ||
		request.Limit < 0 || request.Limit > activity.MaxPastActivitiesLimit ||
		len(request.Cursor) > 2048 {
		return activity.PastActivitiesPage{},
			ErrInvalidPublicPastActivitiesRequest
	}
	at := service.clock().UTC()
	if at.IsZero() {
		return activity.PastActivitiesPage{},
			ErrInvalidPublicPastActivitiesService
	}
	page, err := service.reader.ListPastActivities(
		ctx,
		activity.PastActivitiesFilter{
			TenantID:     service.tenantID,
			ActivityType: request.ActivityType,
			Cursor:       request.Cursor,
			Limit:        request.Limit,
			At:           at,
		},
	)
	if err != nil {
		return activity.PastActivitiesPage{}, fmt.Errorf(
			"read xiangwan public past activities: %w",
			err,
		)
	}
	if !publicPastActivitiesPageConsistent(page, request, at) {
		return activity.PastActivitiesPage{},
			ErrPublicPastActivitiesResponseConflict
	}
	return page, nil
}

func publicPastActivitiesPageConsistent(
	page activity.PastActivitiesPage,
	request PublicPastActivitiesRequest,
	requestedAt time.Time,
) bool {
	activityType := request.ActivityType
	if activityType == "" {
		activityType = activity.ActivityTypeAll
	}
	limit := request.Limit
	if limit == 0 {
		limit = activity.DefaultPastActivitiesLimit
	}
	if page.ActiveActivityType != activityType || page.AsOf.IsZero() ||
		page.AsOf.After(requestedAt.Add(activity.PastActivitiesFutureSkew)) ||
		requestedAt.Sub(page.AsOf) > activity.MaxPastActivitiesCursorAge ||
		len(page.Items) > limit || len(page.NextCursor) > 2048 ||
		(len(page.Items) == 0 && page.NextCursor != "") {
		return false
	}
	if request.Cursor == "" && !page.AsOf.Equal(requestedAt) {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(page.Items))
	for index, item := range page.Items {
		if activity.ValidatePastActivityItem(item) != nil ||
			item.CompletedAt.After(page.AsOf) ||
			(activityType != activity.ActivityTypeAll &&
				item.ActivityType != activityType) {
			return false
		}
		if _, duplicate := seen[item.InstanceID]; duplicate {
			return false
		}
		seen[item.InstanceID] = struct{}{}
		if index > 0 && !pastActivityOrderBefore(page.Items[index-1], item) {
			return false
		}
	}
	return true
}

func pastActivityOrderBefore(
	left activity.PastActivityItem,
	right activity.PastActivityItem,
) bool {
	if !left.CompletedAt.Equal(right.CompletedAt) {
		return left.CompletedAt.After(right.CompletedAt)
	}
	if !left.PublishedAt.Equal(right.PublishedAt) {
		return left.PublishedAt.After(right.PublishedAt)
	}
	return left.InstanceID.String() > right.InstanceID.String()
}
