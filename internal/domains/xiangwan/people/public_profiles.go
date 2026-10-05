package people

import (
	"time"

	"github.com/google/uuid"
)

const (
	DefaultPublicProfilesLimit = 20
	MaxPublicProfilesLimit     = 100
	MaxPublicProfilesCursorAge = 30 * time.Minute
	PublicProfilesFutureSkew   = 5 * time.Second
)

// PublicProfileFilter is the stable, tenant-scoped public PeopleProfile page
// boundary. Cursor contents remain opaque outside the PostgreSQL adapter.
type PublicProfileFilter struct {
	TenantID uuid.UUID
	Limit    int
	Cursor   string
	At       time.Time
}

// PublicProfilesPage contains only moderated, published PeopleProfile facts.
type PublicProfilesPage struct {
	Items      []Profile
	AsOf       time.Time
	NextCursor string
}
