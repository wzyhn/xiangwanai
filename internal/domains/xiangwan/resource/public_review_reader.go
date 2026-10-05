package resource

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrPublicReviewTargetUnavailable = errors.New(
	"xiangwan public review target unavailable",
)

// PublicReviewTargetResolver resolves an anonymous review request to one
// eligible Instance and, when explicitly selected, one Session in that
// Instance. Implementations must collapse missing, private, and cross-tenant
// targets to the same unavailable result.
type PublicReviewTargetResolver interface {
	ResolvePublicReviewTarget(
		ctx context.Context,
		tenantID uuid.UUID,
		instanceID uuid.UUID,
		sessionID *uuid.UUID,
		at time.Time,
	) (PastHighlightReviewTarget, error)
}

// PublicReviewReader hydrates only the exact target returned by a
// PublicReviewTargetResolver.
type PublicReviewReader interface {
	ReadPublicReview(
		ctx context.Context,
		tenantID uuid.UUID,
		target PastHighlightReviewTarget,
		externalDomains ExternalDomainPolicy,
	) (PublicReviewDetail, error)
}
