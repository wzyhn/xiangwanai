package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicPeopleService = errors.New(
		"invalid xiangwan public People service",
	)
	ErrInvalidPublicPeopleRequest = errors.New(
		"invalid xiangwan public People request",
	)
	ErrPublicPeopleResponseConflict = errors.New(
		"xiangwan public People response conflict",
	)
)

type publicPeopleReader interface {
	ListPublishedProfiles(
		context.Context,
		people.PublicProfileFilter,
	) (people.PublicProfilesPage, error)
	GetPublishedProfile(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (people.Profile, error)
}

type PublicPeopleRequest struct {
	Cursor string
	Limit  int
}

type PublicPeopleService struct {
	tenantID uuid.UUID
	reader   publicPeopleReader
	now      func() time.Time
}

func NewPublicPeopleService(
	tenantID uuid.UUID,
	reader publicPeopleReader,
	now func() time.Time,
) (*PublicPeopleService, error) {
	if tenantID == uuid.Nil || reader == nil || now == nil {
		return nil, ErrInvalidPublicPeopleService
	}
	return &PublicPeopleService{
		tenantID: tenantID,
		reader:   reader,
		now:      now,
	}, nil
}

func (service *PublicPeopleService) List(
	ctx context.Context,
	request PublicPeopleRequest,
) (people.PublicProfilesPage, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.reader == nil || service.now == nil {
		return people.PublicProfilesPage{}, ErrInvalidPublicPeopleService
	}
	page, err := service.reader.ListPublishedProfiles(
		ctx,
		people.PublicProfileFilter{
			TenantID: service.tenantID,
			Cursor:   request.Cursor,
			Limit:    request.Limit,
			At:       service.now(),
		},
	)
	if err != nil {
		return people.PublicProfilesPage{}, fmt.Errorf(
			"read public PeopleProfiles: %w",
			err,
		)
	}
	if err := validatePublicPeoplePage(service.tenantID, page); err != nil {
		return people.PublicProfilesPage{}, err
	}
	return page, nil
}

func (service *PublicPeopleService) Read(
	ctx context.Context,
	peopleID uuid.UUID,
) (people.Profile, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.reader == nil || service.now == nil {
		return people.Profile{}, ErrInvalidPublicPeopleService
	}
	if peopleID == uuid.Nil {
		return people.Profile{}, ErrInvalidPublicPeopleRequest
	}
	profile, err := service.reader.GetPublishedProfile(
		ctx,
		service.tenantID,
		peopleID,
	)
	if err != nil {
		return people.Profile{}, fmt.Errorf(
			"read public PeopleProfile: %w",
			err,
		)
	}
	if err := validatePublicProfile(service.tenantID, profile); err != nil ||
		profile.ID != peopleID {
		return people.Profile{}, ErrPublicPeopleResponseConflict
	}
	return profile, nil
}

func validatePublicPeoplePage(
	tenantID uuid.UUID,
	page people.PublicProfilesPage,
) error {
	if page.AsOf.IsZero() || (len(page.Items) == 0 && page.NextCursor != "") {
		return ErrPublicPeopleResponseConflict
	}
	seen := make(map[uuid.UUID]struct{}, len(page.Items))
	for index, profile := range page.Items {
		if validatePublicProfile(tenantID, profile) != nil ||
			profile.UpdatedAt.After(page.AsOf) {
			return ErrPublicPeopleResponseConflict
		}
		if _, exists := seen[profile.ID]; exists {
			return ErrPublicPeopleResponseConflict
		}
		seen[profile.ID] = struct{}{}
		if index > 0 {
			previous := page.Items[index-1]
			if profile.UpdatedAt.After(previous.UpdatedAt) ||
				(profile.UpdatedAt.Equal(previous.UpdatedAt) &&
					bytesCompareUUID(profile.ID, previous.ID) >= 0) {
				return ErrPublicPeopleResponseConflict
			}
		}
	}
	return nil
}

func validatePublicProfile(tenantID uuid.UUID, profile people.Profile) error {
	if people.ValidateProfile(profile) != nil || profile.TenantID != tenantID ||
		profile.ProfileStatus != people.ProfileStatusPublished ||
		profile.ModerationStatus != people.ModerationStatusApproved ||
		profile.ModeratedAt == nil {
		return ErrPublicPeopleResponseConflict
	}
	return nil
}

func bytesCompareUUID(left uuid.UUID, right uuid.UUID) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	return 0
}
