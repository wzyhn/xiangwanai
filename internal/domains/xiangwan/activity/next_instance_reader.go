package activity

import (
	"context"

	"github.com/google/uuid"
)

// NextInstanceRouteReader exposes the canonical zero/one/many Session route
// for the first later published Instance in the same Series.
type NextInstanceRouteReader interface {
	ResolveNextInstanceSessionRoute(
		ctx context.Context,
		tenantID uuid.UUID,
		sourceInstanceID uuid.UUID,
	) (NextInstanceRouteResolution, error)
}
