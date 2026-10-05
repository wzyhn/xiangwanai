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
	ErrInvalidPublicHomeService = errors.New(
		"invalid xiangwan public home service",
	)
	ErrInvalidPublicHomeRequest = errors.New(
		"invalid xiangwan public home request",
	)
	ErrPublicHomeResponseConflict = errors.New(
		"xiangwan public home response conflict",
	)
)

type publicHomeProfileReader interface {
	ReadPublicHomeProfile(
		context.Context,
		uuid.UUID,
	) (activity.PublicHomeProfile, error)
}

type publicHomeCatalogReader interface {
	ReadHomeCatalog(
		context.Context,
		uuid.UUID,
		activity.HomeFilter,
		[]activity.HomeQuickTag,
		time.Time,
	) (activity.HomeCatalog, error)
}

// publicHomeParticipantReader loads the public avatar stack for the page's
// Sessions in one batch query so the home read never degrades into N+1.
type publicHomeParticipantReader interface {
	ListPublicParticipantAvatars(
		context.Context,
		uuid.UUID,
		[]uuid.UUID,
	) (map[uuid.UUID][]string, error)
	ListPublicFavoriteAvatars(
		context.Context,
		uuid.UUID,
		[]uuid.UUID,
	) (map[uuid.UUID][]string, error)
}

type PublicHomePage struct {
	Profile activity.PublicHomeProfile
	Catalog activity.HomeCatalog
}

type PublicHomeService struct {
	tenantID      uuid.UUID
	profileReader publicHomeProfileReader
	catalogReader publicHomeCatalogReader
	avatarReader  publicHomeParticipantReader
	now           func() time.Time
}

func NewPublicHomeService(
	tenantID uuid.UUID,
	profileReader publicHomeProfileReader,
	catalogReader publicHomeCatalogReader,
	avatarReader publicHomeParticipantReader,
	now func() time.Time,
) (*PublicHomeService, error) {
	if tenantID == uuid.Nil || profileReader == nil || catalogReader == nil ||
		avatarReader == nil || now == nil {
		return nil, ErrInvalidPublicHomeService
	}
	return &PublicHomeService{
		tenantID:      tenantID,
		profileReader: profileReader,
		catalogReader: catalogReader,
		avatarReader:  avatarReader,
		now:           now,
	}, nil
}

func (service *PublicHomeService) ReadHome(
	ctx context.Context,
	filter activity.HomeFilter,
) (PublicHomePage, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.profileReader == nil || service.catalogReader == nil ||
		service.avatarReader == nil || service.now == nil {
		return PublicHomePage{}, ErrInvalidPublicHomeService
	}
	profile, err := service.profileReader.ReadPublicHomeProfile(
		ctx,
		service.tenantID,
	)
	if err != nil {
		return PublicHomePage{}, fmt.Errorf("read public home profile: %w", err)
	}
	if err := activity.ValidatePublicHomeProfile(profile); err != nil {
		return PublicHomePage{}, err
	}
	catalog, err := service.catalogReader.ReadHomeCatalog(
		ctx,
		service.tenantID,
		filter,
		profile.AvailableQuickTags,
		service.now(),
	)
	if err != nil {
		return PublicHomePage{}, fmt.Errorf("read public home catalog: %w", err)
	}
	// Compare the tag sequence semantically: the catalog clones the profile's
	// list, and cloning an empty list through append(nil, ...) yields nil,
	// which reflect.DeepEqual treats as different from an empty non-nil slice
	// — so a brand profile published without quick tags failed every home
	// read with a bogus response conflict (2026-09-19 production incident).
	sameQuickTags := len(catalog.AvailableQuickTags) == len(profile.AvailableQuickTags)
	if sameQuickTags {
		for index := range catalog.AvailableQuickTags {
			if catalog.AvailableQuickTags[index] != profile.AvailableQuickTags[index] {
				sameQuickTags = false
				break
			}
		}
	}
	if catalog.BusinessTimezone != activity.BusinessTimezone || !sameQuickTags {
		return PublicHomePage{}, ErrPublicHomeResponseConflict
	}
	if err := service.attachParticipantAvatars(ctx, &catalog); err != nil {
		return PublicHomePage{}, err
	}
	if err := service.attachFavoriteAvatars(ctx, &catalog); err != nil {
		return PublicHomePage{}, err
	}
	return PublicHomePage{Profile: profile, Catalog: catalog}, nil
}

// attachParticipantAvatars enriches the returned page in place through one
// batch read keyed by the page's Session identities; Sessions without any
// public confirmed participant keep an empty stack.
func (service *PublicHomeService) attachParticipantAvatars(
	ctx context.Context,
	catalog *activity.HomeCatalog,
) error {
	if len(catalog.Cards) == 0 {
		return nil
	}
	sessionIDs := make([]uuid.UUID, 0, len(catalog.Cards))
	for _, card := range catalog.Cards {
		sessionIDs = append(sessionIDs, card.SessionID)
	}
	avatars, err := service.avatarReader.ListPublicParticipantAvatars(
		ctx,
		service.tenantID,
		sessionIDs,
	)
	if err != nil {
		return fmt.Errorf("read public participant avatars: %w", err)
	}
	for index := range catalog.Cards {
		stack := avatars[catalog.Cards[index].SessionID]
		if len(stack) > activity.MaxPublicParticipantAvatars {
			stack = stack[:activity.MaxPublicParticipantAvatars]
		}
		catalog.Cards[index].ParticipantAvatars = append([]string(nil), stack...)
	}
	return nil
}

// attachFavoriteAvatars enriches the page with a separate, bounded preview of
// current Series favorites. Favorite relations are Series-scoped, so the
// lookup is keyed by each card's Series identity rather than its Session.
func (service *PublicHomeService) attachFavoriteAvatars(
	ctx context.Context,
	catalog *activity.HomeCatalog,
) error {
	if len(catalog.Cards) == 0 {
		return nil
	}
	seriesIDs := make([]uuid.UUID, 0, len(catalog.Cards))
	seen := make(map[uuid.UUID]struct{}, len(catalog.Cards))
	for _, card := range catalog.Cards {
		if _, ok := seen[card.SeriesID]; ok {
			continue
		}
		seen[card.SeriesID] = struct{}{}
		seriesIDs = append(seriesIDs, card.SeriesID)
	}
	avatars, err := service.avatarReader.ListPublicFavoriteAvatars(
		ctx,
		service.tenantID,
		seriesIDs,
	)
	if err != nil {
		return fmt.Errorf("read public favorite avatars: %w", err)
	}
	for index := range catalog.Cards {
		stack := avatars[catalog.Cards[index].SeriesID]
		if len(stack) > activity.MaxPublicFavoriteAvatars {
			stack = stack[:activity.MaxPublicFavoriteAvatars]
		}
		catalog.Cards[index].FavoriteAvatars = append([]string(nil), stack...)
	}
	return nil
}
