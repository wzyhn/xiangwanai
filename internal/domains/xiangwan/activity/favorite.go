package activity

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultMyFavoritesLimit = 20
	MaxMyFavoritesLimit     = 100
	MaxMyFavoritesCursorAge = 30 * time.Minute
	MyFavoritesFutureSkew   = 5 * time.Second
)

var ErrInvalidSeriesFavoriteState = errors.New(
	"invalid xiangwan Series favorite state",
)

var ErrInvalidMyFavoriteItem = errors.New(
	"invalid xiangwan My Favorite item",
)

type SeriesFavoriteState struct {
	TenantID      uuid.UUID
	PrincipalID   uuid.UUID
	SeriesID      uuid.UUID
	Favorited     bool
	Changed       bool
	FavoriteCount int64
	SeriesVersion int64
	OccurredAt    time.Time
}

type MyFavoriteFilter struct {
	TenantID    uuid.UUID
	PrincipalID uuid.UUID
	Limit       int
	Cursor      string
	At          time.Time
}

type MyFavoriteItem struct {
	SeriesID          uuid.UUID
	Title             string
	SeriesStatus      SeriesStatus
	FavoriteCount     int64
	FavoriteAvatars   []string
	FavoritedAt       time.Time
	SessionsAvailable bool
}

type MyFavoritesPage struct {
	Items      []MyFavoriteItem
	AsOf       time.Time
	NextCursor string
}

func (SeriesFavoriteState) String() string {
	return "xiangwan SeriesFavoriteState{owner:[REDACTED]}"
}

func (value SeriesFavoriteState) GoString() string {
	return value.String()
}

func ValidateSeriesFavoriteState(value SeriesFavoriteState) error {
	if value.TenantID == uuid.Nil || value.PrincipalID == uuid.Nil ||
		value.SeriesID == uuid.Nil || value.FavoriteCount < 0 ||
		value.SeriesVersion < 1 || value.OccurredAt.IsZero() {
		return ErrInvalidSeriesFavoriteState
	}
	return nil
}

func ValidateMyFavoriteItem(value MyFavoriteItem) error {
	if value.SeriesID == uuid.Nil || value.Title == "" ||
		value.Title != strings.TrimSpace(value.Title) ||
		len([]rune(value.Title)) > 200 || value.FavoriteCount < 0 ||
		len(value.FavoriteAvatars) > MaxPublicFavoriteAvatars ||
		value.FavoritedAt.IsZero() {
		return ErrInvalidMyFavoriteItem
	}
	switch value.SeriesStatus {
	case SeriesStatusDraft, SeriesStatusActive, SeriesStatusArchived:
	default:
		return ErrInvalidMyFavoriteItem
	}
	if value.SessionsAvailable && value.SeriesStatus != SeriesStatusActive {
		return ErrInvalidMyFavoriteItem
	}
	return nil
}
