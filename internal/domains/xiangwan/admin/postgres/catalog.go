package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	resourcepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxVenueNameLength               = 200
	maxOnlineParticipationModeLength = 80
	maxBrandQuickTags                = 20
	maxPostgresInteger               = 1<<31 - 1
	adminOperationLockReleaseTimeout = 5 * time.Second
)

func (catalog *Catalog) GetBrandProfile(
	ctx context.Context,
	principal xiangwanadmin.Principal,
) (xiangwanadmin.BrandProfile, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil {
		return xiangwanadmin.BrandProfile{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.BrandProfile{}, err
	}
	defer func() { _ = tx.Rollback() }()
	value, err := readBrandProfile(ctx, tx, catalog.tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		value = xiangwanadmin.BrandProfile{
			HeroMode:  activity.HomeHeroModeText,
			QuickTags: []activity.HomeQuickTag{},
		}
	} else if err != nil {
		return xiangwanadmin.BrandProfile{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.BrandProfile{}, fmt.Errorf("commit admin BrandProfile read: %w", err)
	}
	return value, nil
}

func (catalog *Catalog) PublishBrandProfile(
	ctx context.Context,
	command xiangwanadmin.PublishBrandProfileCommand,
) (result xiangwanadmin.BrandProfile, resultErr error) {
	defer func() {
		targetID := uuid.Nil
		if catalog != nil {
			targetID = catalog.tenantID
		}
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"brand_profile.publish", "brand_profile", targetID,
			command.RequestID, resultErr,
		)
	}()
	command.CommunityName = strings.TrimSpace(command.CommunityName)
	command.BrandIntro = strings.TrimSpace(command.BrandIntro)
	command.HeroEyebrow = strings.TrimSpace(command.HeroEyebrow)
	command.HeroSubtitle = strings.TrimSpace(command.HeroSubtitle)
	command.HeroImageURL = strings.TrimSpace(command.HeroImageURL)
	command.HeroImageAlt = strings.TrimSpace(command.HeroImageAlt)
	// Seed with a non-nil empty slice: appending zero elements to a nil base
	// returns nil, and json.Marshal renders a nil slice as null — which the
	// xiangwan_valid_brand_quick_tags CHECK rejects because it is not an
	// array (2026-09-19 production incident: first publish without quick
	// tags failed with SQLSTATE 23514).
	command.QuickTags = append([]activity.HomeQuickTag{}, command.QuickTags...)
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.ExpectedVersion < 0 || len(command.QuickTags) > maxBrandQuickTags {
		return xiangwanadmin.BrandProfile{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	candidate := activity.PublicHomeProfile{
		LifecycleStatus: activity.BrandLifecycleActive, PublicationVersion: 1,
		CommunityName: command.CommunityName, BrandIntro: command.BrandIntro,
		HeroMode: command.HeroMode, HeroEyebrow: command.HeroEyebrow,
		HeroSubtitle: command.HeroSubtitle, HeroImageURL: command.HeroImageURL,
		HeroImageAlt: command.HeroImageAlt, AvailableQuickTags: command.QuickTags,
		PublishedAt: catalog.now().UTC(),
	}
	if activity.ValidatePublicHomeProfile(candidate) != nil {
		return xiangwanadmin.BrandProfile{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		ExpectedVersion int64                   `json:"expected_version"`
		CommunityName   string                  `json:"community_name"`
		BrandIntro      string                  `json:"brand_intro"`
		HeroMode        activity.HomeHeroMode   `json:"hero_mode"`
		HeroEyebrow     string                  `json:"hero_eyebrow"`
		HeroSubtitle    string                  `json:"hero_subtitle"`
		HeroImageURL    string                  `json:"hero_image_url"`
		HeroImageAlt    string                  `json:"hero_image_alt"`
		QuickTags       []activity.HomeQuickTag `json:"quick_tags"`
	}{
		command.ExpectedVersion, command.CommunityName, command.BrandIntro,
		command.HeroMode, command.HeroEyebrow, command.HeroSubtitle,
		command.HeroImageURL, command.HeroImageAlt, command.QuickTags,
	})
	if err != nil {
		return xiangwanadmin.BrandProfile{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return xiangwanadmin.BrandProfile{}, err
	}
	defer func() { _ = tx.Rollback() }()
	brandHeroFilenames := brandHeroFilenamesFromReferences(command.HeroImageURL)
	for _, filename := range brandHeroFilenames {
		if err := lockBrandHeroFilename(ctx, tx.Tx, filename); err != nil {
			return xiangwanadmin.BrandProfile{}, err
		}
	}
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.BrandProfile]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"brand_profile.publish", digest,
	); err != nil {
		return xiangwanadmin.BrandProfile{}, err
	} else if replay {
		if !receipt.Value.Configured || receipt.Value.Version < 1 ||
			receipt.Value.PublicationVersion < 1 {
			return xiangwanadmin.BrandProfile{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return xiangwanadmin.BrandProfile{}, fmt.Errorf("commit admin BrandProfile replay: %w", err)
		}
		return receipt.Value, nil
	}
	// A replay must return the immutable receipt even when a later presentation
	// change orphaned this hero and GC tombstoned it. The tombstone fence applies
	// only to a new attachment.
	if err := rejectDeletedBrandHeroReferences(ctx, tx.Tx, brandHeroFilenames); err != nil {
		return xiangwanadmin.BrandProfile{}, err
	}

	var currentPublicationVersion sql.NullInt64
	var currentVersion int64
	var currentLifecycleStatus activity.BrandLifecycleStatus
	err = tx.QueryRowContext(ctx, `
SELECT current_publication_version, version, lifecycle_status
FROM xiangwan_brand_profiles
WHERE tenant_id = $1
FOR UPDATE
`, catalog.tenantID).Scan(
		&currentPublicationVersion, &currentVersion, &currentLifecycleStatus,
	)
	newProfile := errors.Is(err, sql.ErrNoRows)
	if err != nil && !newProfile {
		return xiangwanadmin.BrandProfile{}, fmt.Errorf("lock admin BrandProfile: %w", err)
	}
	if (newProfile && command.ExpectedVersion != 0) ||
		(!newProfile && currentVersion != command.ExpectedVersion) {
		return xiangwanadmin.BrandProfile{}, xiangwanadmin.ErrVersionConflict
	}
	publicationVersion := int64(1)
	profileVersion := int64(1)
	lifecycleStatus := activity.BrandLifecycleActive
	if !newProfile {
		if currentPublicationVersion.Valid {
			publicationVersion = currentPublicationVersion.Int64 + 1
		}
		profileVersion = currentVersion + 1
		lifecycleStatus = currentLifecycleStatus
	}
	// A published Instance pins its quick-tag codes into the public catalog:
	// removing a code that a published Instance still exposes makes every
	// home read fail fact validation and takes the whole Mini Program home
	// down. Reject the removal inside this transaction (codex review
	// 2026-09-19, P1).
	if currentPublicationVersion.Valid {
		var publishedTagsJSON []byte
		if err := tx.QueryRowContext(ctx, `
SELECT publication.quick_tags
FROM xiangwan_brand_profile_publications AS publication
WHERE publication.tenant_id = $1
  AND publication.publication_version = $2
`, catalog.tenantID, currentPublicationVersion.Int64).Scan(&publishedTagsJSON); err != nil {
			return xiangwanadmin.BrandProfile{}, fmt.Errorf(
				"read current BrandProfile publication: %w", err,
			)
		}
		var publishedTags []activity.HomeQuickTag
		if err := json.Unmarshal(publishedTagsJSON, &publishedTags); err != nil {
			return xiangwanadmin.BrandProfile{}, fmt.Errorf(
				"decode current BrandProfile quick tags: %w", err,
			)
		}
		if removed := removedBrandTagCodes(publishedTags, command.QuickTags); len(removed) > 0 {
			var stillReferenced bool
			if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM xiangwan_activity_instances AS instance
    JOIN xiangwan_activity_series AS series
      ON series.tenant_id = instance.tenant_id
     AND series.id = instance.series_id
     AND series.current_public_instance_id = instance.id
    WHERE instance.tenant_id = $1
      AND series.status = 'active'
      AND series.home_visible
      AND instance.status IN ('published', 'completed', 'cancelled')
      AND instance.quick_tag_codes && $2::TEXT[]
)`, catalog.tenantID, removed).Scan(&stillReferenced); err != nil {
				return xiangwanadmin.BrandProfile{}, fmt.Errorf(
					"read published Instance quick tags: %w", err,
				)
			}
			if stillReferenced {
				return xiangwanadmin.BrandProfile{}, xiangwanadmin.ErrBrandQuickTagInUse
			}
		}
	}
	quickTagsJSON, err := encodeBrandQuickTags(command.QuickTags)
	if err != nil {
		return xiangwanadmin.BrandProfile{}, fmt.Errorf("encode BrandProfile quick tags: %w", err)
	}
	var publishedAt time.Time
	err = tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_brand_profile_publications (
    tenant_id, publication_version, community_name, brand_intro,
    hero_mode, hero_eyebrow, hero_subtitle, hero_image_url, hero_image_alt,
    quick_tags, published_by, published_at, recorded_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::JSONB, $11,
          clock_timestamp(), clock_timestamp())
RETURNING published_at
`, catalog.tenantID, publicationVersion, command.CommunityName, command.BrandIntro,
		command.HeroMode, command.HeroEyebrow, command.HeroSubtitle,
		command.HeroImageURL, command.HeroImageAlt, string(quickTagsJSON),
		command.ActorID).Scan(&publishedAt)
	if err != nil {
		return xiangwanadmin.BrandProfile{}, fmt.Errorf(
			"publish admin BrandProfile: %w", classifyBrandPublicationWriteError(err),
		)
	}
	if newProfile {
		_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_brand_profiles (
    tenant_id, lifecycle_status, current_publication_version, version,
    created_at, updated_at
) VALUES ($1, 'active', $2, 1, $3, $3)
`, catalog.tenantID, publicationVersion, publishedAt)
	} else {
		var updateResult sql.Result
		updateResult, err = tx.ExecContext(ctx, `
UPDATE xiangwan_brand_profiles
SET current_publication_version = $2, version = version + 1, updated_at = $3
WHERE tenant_id = $1 AND version = $4
`, catalog.tenantID, publicationVersion, publishedAt, command.ExpectedVersion)
		if err == nil {
			if rows, rowsErr := updateResult.RowsAffected(); rowsErr != nil {
				err = rowsErr
			} else if rows != 1 {
				err = xiangwanadmin.ErrVersionConflict
			}
		}
	}
	if err != nil {
		return xiangwanadmin.BrandProfile{}, fmt.Errorf("select admin BrandProfile publication: %w", err)
	}
	if err := clearBrandHeroGCMarkers(ctx, tx.Tx, brandHeroFilenames); err != nil {
		return xiangwanadmin.BrandProfile{}, err
	}
	value := xiangwanadmin.BrandProfile{
		Configured: true, LifecycleStatus: lifecycleStatus,
		PublicationVersion: publicationVersion, Version: profileVersion,
		CommunityName: command.CommunityName, BrandIntro: command.BrandIntro,
		HeroMode: command.HeroMode, HeroEyebrow: command.HeroEyebrow,
		HeroSubtitle: command.HeroSubtitle, HeroImageURL: command.HeroImageURL,
		HeroImageAlt: command.HeroImageAlt,
		QuickTags:    append([]activity.HomeQuickTag(nil), command.QuickTags...),
		PublishedAt:  &publishedAt, UpdatedAt: &publishedAt,
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "brand_profile.publish", digest,
		operationResult[xiangwanadmin.BrandProfile]{Value: value}, catalog.tenantID,
		value.Version, command.RequestID, publishedAt,
	); err != nil {
		return xiangwanadmin.BrandProfile{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.BrandProfile{}, fmt.Errorf("commit admin BrandProfile publish: %w", err)
	}
	return value, nil
}

func readBrandProfile(
	ctx context.Context,
	tx activitypostgres.DBTX,
	tenantID uuid.UUID,
) (xiangwanadmin.BrandProfile, error) {
	var value xiangwanadmin.BrandProfile
	var quickTagsJSON []byte
	var publishedAt time.Time
	var updatedAt time.Time
	err := tx.QueryRowContext(ctx, `
SELECT profile.lifecycle_status, profile.current_publication_version, profile.version,
       publication.community_name, publication.brand_intro,
       publication.hero_mode, publication.hero_eyebrow, publication.hero_subtitle,
       publication.hero_image_url, publication.hero_image_alt,
       publication.quick_tags, publication.published_at, profile.updated_at
FROM xiangwan_brand_profiles AS profile
JOIN xiangwan_brand_profile_publications AS publication
  ON publication.tenant_id = profile.tenant_id
 AND publication.publication_version = profile.current_publication_version
WHERE profile.tenant_id = $1
`, tenantID).Scan(
		&value.LifecycleStatus, &value.PublicationVersion, &value.Version,
		&value.CommunityName, &value.BrandIntro, &value.HeroMode,
		&value.HeroEyebrow, &value.HeroSubtitle, &value.HeroImageURL,
		&value.HeroImageAlt, &quickTagsJSON, &publishedAt, &updatedAt,
	)
	if err != nil {
		return xiangwanadmin.BrandProfile{}, err
	}
	if err := json.Unmarshal(quickTagsJSON, &value.QuickTags); err != nil {
		return xiangwanadmin.BrandProfile{}, fmt.Errorf("decode admin BrandProfile quick tags: %w", err)
	}
	if value.QuickTags == nil {
		value.QuickTags = []activity.HomeQuickTag{}
	}
	value.Configured = true
	value.PublishedAt = &publishedAt
	value.UpdatedAt = &updatedAt
	return value, nil
}

type Catalog struct {
	db                            *sql.DB
	tenantID                      uuid.UUID
	generationID                  uuid.UUID
	authorizer                    *GrantAuthorizer
	publisher                     *activitypostgres.Publisher
	instanceCancellationPreviewer *activitypostgres.InstanceCancellationPreviewer
	instanceCanceller             *activitypostgres.InstanceCanceller
	sessionCancellationPreviewer  *activitypostgres.SessionCancellationPreviewer
	sessionCanceller              *activitypostgres.SessionCanceller
	checkinRevoker                *checkinpostgres.Revoker
	externalDomains               resource.ExternalDomainPolicy
	bindingInvitationKey          []byte
	now                           func() time.Time
}

func NewCatalog(
	db *sql.DB,
	tenantID uuid.UUID,
	generationID uuid.UUID,
	authorizer *GrantAuthorizer,
) (*Catalog, error) {
	return NewCatalogWithCouponRefundPolicy(
		db, tenantID, generationID, authorizer, nil,
	)
}

// NewCatalogWithCouponRefundPolicy wires the administrator catalog to the
// versioned coupon refund policy used when an activity is taken offline. A
// nil policy deliberately fails closed for coupon-backed registrations; the
// runtime constructor supplies the configured policy evaluator.
func NewCatalogWithCouponRefundPolicy(
	db *sql.DB,
	tenantID uuid.UUID,
	generationID uuid.UUID,
	authorizer *GrantAuthorizer,
	couponRefundPolicy coupon.RefundPolicyEvaluator,
) (*Catalog, error) {
	emptyExternalDomains, _ := resource.NewExternalDomainPolicy(nil)
	return NewCatalogWithCouponRefundPolicyAndExternalDomains(
		db, tenantID, generationID, authorizer, couponRefundPolicy,
		emptyExternalDomains,
	)
}

// NewCatalogWithCouponRefundPolicyAndExternalDomains wires the administrator
// catalog with the same external-link policy used by the public review reader.
// Keeping that policy on the read catalog means review-status can report only
// resources that the public review endpoint can actually project.
func NewCatalogWithCouponRefundPolicyAndExternalDomains(
	db *sql.DB,
	tenantID uuid.UUID,
	generationID uuid.UUID,
	authorizer *GrantAuthorizer,
	couponRefundPolicy coupon.RefundPolicyEvaluator,
	externalDomains resource.ExternalDomainPolicy,
) (*Catalog, error) {
	if db == nil || tenantID == uuid.Nil || generationID == uuid.Nil || authorizer == nil {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	authorizeCancellation := func(
		ctx context.Context,
		tx *sql.Tx,
		callbackTenantID uuid.UUID,
		principalID uuid.UUID,
		identityLinkID uuid.UUID,
	) error {
		if callbackTenantID != tenantID {
			return xiangwanadmin.ErrScopeForbidden
		}
		// Cancellation is a destructive activity write.  Keep its
		// transaction-bound authorization contract identical to the catalog
		// write path: the active runtime generation is locked before the
		// Principal, exact identity link, or Grant, so a cutover cannot race
		// the fact transaction and leave an old generation able to take an
		// activity offline.
		var writeEpoch int64
		err := tx.QueryRowContext(ctx, `
SELECT write_epoch
FROM xiangwan_runtime_generations
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
  AND active_generation_id = $2
  AND write_epoch > 0
  AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, tenantID, generationID).Scan(&writeEpoch)
		if errors.Is(err, sql.ErrNoRows) {
			return xiangwanadmin.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("lock administrator cancellation generation: %w", err)
		}
		return authorizer.lockActivityOperatorIdentity(
			ctx, tx, principalID, identityLinkID,
		)
	}
	hooks := newAdminSessionCancellationHooks(tenantID, authorizeCancellation)
	return &Catalog{
		db: db, tenantID: tenantID, generationID: generationID,
		authorizer:                   authorizer,
		externalDomains:              externalDomains,
		publisher:                    activitypostgres.NewAdminPublisher(db, generationID),
		sessionCancellationPreviewer: activitypostgres.NewSessionCancellationPreviewerWithHooks(db, hooks),
		sessionCanceller:             activitypostgres.NewSessionCancellerWithHooks(db, couponRefundPolicy, hooks),
		checkinRevoker:               newAdminCheckinRevoker(db, tenantID, generationID, authorizer),
		instanceCancellationPreviewer: activitypostgres.NewInstanceCancellationPreviewerWithAuthorization(
			db, authorizeCancellation,
		),
		instanceCanceller: activitypostgres.NewInstanceCancellerWithCouponRefundPolicyAndAuthorization(
			db, couponRefundPolicy, authorizeCancellation,
		),
		now: time.Now,
	}, nil
}

func (catalog *Catalog) PreviewInstanceCancellation(
	ctx context.Context,
	command xiangwanadmin.PreviewInstanceCancellationCommand,
) (activity.InstanceCancellationPreview, error) {
	command.Reason = strings.TrimSpace(command.Reason)
	if !catalog.valid(ctx) || catalog.instanceCancellationPreviewer == nil ||
		!validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) ||
		command.InstanceID == uuid.Nil || command.Reason == "" || len([]rune(command.Reason)) > 500 {
		return activity.InstanceCancellationPreview{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if err := catalog.authorizeCancellation(ctx, command.ActorID, command.IdentityLinkID); err != nil {
		return activity.InstanceCancellationPreview{}, err
	}
	return catalog.instanceCancellationPreviewer.Preview(ctx, activitypostgres.PreviewInstanceCancellationCommand{
		TenantID: catalog.tenantID, InstanceID: command.InstanceID, RequestedBy: command.ActorID,
		IdentityLinkID: command.IdentityLinkID,
		IdempotencyKey: command.OperationID.String(), Reason: command.Reason,
	})
}

func (catalog *Catalog) CancelInstance(
	ctx context.Context,
	command xiangwanadmin.CancelInstanceCommand,
) (xiangwanadmin.InstanceCancellationResult, error) {
	command.Reason = strings.TrimSpace(command.Reason)
	if !catalog.valid(ctx) || catalog.instanceCanceller == nil ||
		!validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) ||
		command.InstanceID == uuid.Nil || command.PreviewID == uuid.Nil ||
		command.ExpectedInstanceVersion < 1 || command.Reason == "" || len([]rune(command.Reason)) > 500 {
		return xiangwanadmin.InstanceCancellationResult{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if err := catalog.authorizeCancellation(ctx, command.ActorID, command.IdentityLinkID); err != nil {
		return xiangwanadmin.InstanceCancellationResult{}, err
	}
	result, err := catalog.instanceCanceller.Cancel(ctx, activitypostgres.InstanceCancellationCommand{
		TenantID: catalog.tenantID, InstanceID: command.InstanceID, PreviewID: command.PreviewID,
		ActorID: command.ActorID, IdentityLinkID: command.IdentityLinkID,
		ExpectedInstanceVersion: command.ExpectedInstanceVersion,
		IdempotencyKey:          command.OperationID.String(), Reason: command.Reason,
		NotificationStrategy: activity.CancellationNotificationManualRequired,
	})
	if err != nil {
		return xiangwanadmin.InstanceCancellationResult{}, err
	}
	return xiangwanadmin.InstanceCancellationResult{
		Instance: result.Instance, Receipt: result.Receipt, SessionReceipts: result.SessionReceipts,
	}, nil
}

// authorizeCancellation is a preflight scope check that preserves the
// endpoint's no-existence-leak behavior. The cancellation adapters repeat the
// same lock inside their Serializable fact transaction; this check is not the
// correctness fence.
func (catalog *Catalog) authorizeCancellation(
	ctx context.Context,
	principalID uuid.UUID,
	identityLinkID uuid.UUID,
) error {
	tx, err := catalog.beginAuthorizedRead(ctx, xiangwanadmin.Principal{
		PrincipalID: principalID, IdentityLinkID: identityLinkID,
	})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.RequireActivityOperatorIdentity(ctx, tx, principalID, identityLinkID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit administrator cancellation authorization: %w", err)
	}
	return nil
}

func (catalog *Catalog) ListSeries(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	page int,
	pageSize int,
) (xiangwanadmin.SeriesPage, error) {
	page, pageSize, err := normalizePage(page, pageSize)
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || err != nil {
		return xiangwanadmin.SeriesPage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.SeriesPage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	items, total, err := activitypostgres.NewRepository(tx).ListSeries(
		ctx,
		catalog.tenantID,
		(page-1)*pageSize,
		pageSize,
	)
	if err != nil {
		return xiangwanadmin.SeriesPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.SeriesPage{}, fmt.Errorf("commit admin Series read: %w", err)
	}
	return xiangwanadmin.SeriesPage{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	}, nil
}

func (catalog *Catalog) CreateSeries(
	ctx context.Context,
	command xiangwanadmin.CreateSeriesCommand,
) (result activity.Series, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID, "series.create",
			"operation", command.OperationID, command.RequestID, resultErr,
		)
	}()
	command.Title = strings.TrimSpace(command.Title)
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || utf8.RuneCountInString(command.Title) < 1 ||
		utf8.RuneCountInString(command.Title) > 200 {
		return activity.Series{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		Title       string `json:"title"`
		HomeVisible bool   `json:"home_visible"`
	}{Title: command.Title, HomeVisible: command.HomeVisible})
	if err != nil {
		return activity.Series{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return activity.Series{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[activity.Series]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"series.create", digest,
	); err != nil {
		return activity.Series{}, err
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.Version < 1 {
			return activity.Series{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Series{}, fmt.Errorf("commit admin Series replay: %w", err)
		}
		return receipt.Value, nil
	}
	value, err := activitypostgres.NewRepository(tx).CreateSeries(ctx, activity.Series{
		TenantID: catalog.tenantID, Title: command.Title,
		Status: activity.SeriesStatusDraft,
	})
	if err != nil {
		return activity.Series{}, err
	}
	if !command.HomeVisible {
		if _, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_series
SET home_visible = FALSE, updated_at = $3, version = version + 1
WHERE tenant_id = $1 AND id = $2
`, catalog.tenantID, value.ID, catalog.now().UTC()); err != nil {
			return activity.Series{}, fmt.Errorf("set admin Series visibility: %w", err)
		}
		value, err = activitypostgres.NewRepository(tx).GetSeries(ctx, catalog.tenantID, value.ID)
		if err != nil {
			return activity.Series{}, err
		}
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "series.create", digest,
		operationResult[activity.Series]{Value: value}, value.ID, value.Version,
		command.RequestID, catalog.now().UTC(),
	); err != nil {
		return activity.Series{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Series{}, fmt.Errorf("commit admin Series create: %w", err)
	}
	return value, nil
}

func (catalog *Catalog) ListInstances(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	seriesID *uuid.UUID,
	page int,
	pageSize int,
) (xiangwanadmin.InstancePage, error) {
	page, pageSize, err := normalizePage(page, pageSize)
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || err != nil ||
		(seriesID != nil && *seriesID == uuid.Nil) {
		return xiangwanadmin.InstancePage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.InstancePage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var total int64
	err = tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM xiangwan_activity_instances
WHERE tenant_id = $1
  AND ($2::UUID IS NULL OR series_id = $2)
`, catalog.tenantID, nullableUUID(seriesID)).Scan(&total)
	if err != nil {
		return xiangwanadmin.InstancePage{}, fmt.Errorf("count admin Instances: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT
    instance.id, instance.tenant_id, instance.series_id, instance.issue_no, instance.title,
    instance.status, instance.activity_type, TO_JSON(instance.quick_tag_codes),
    instance.cover_image_url, instance.detail_blocks, instance.publication_version,
    instance.presentation_revision,
    instance.scheduled_at, instance.published_at, instance.completed_at,
    instance.version,
    instance.created_at, instance.updated_at,
    series.title,
    COUNT(session.id)
FROM xiangwan_activity_instances AS instance
JOIN xiangwan_activity_series AS series
  ON series.tenant_id = instance.tenant_id
 AND series.id = instance.series_id
LEFT JOIN xiangwan_activity_sessions AS session
  ON session.tenant_id = instance.tenant_id
 AND session.instance_id = instance.id
WHERE instance.tenant_id = $1
  AND ($2::UUID IS NULL OR instance.series_id = $2)
GROUP BY instance.id, series.title
ORDER BY instance.updated_at DESC, instance.id DESC
OFFSET $3 LIMIT $4
`, catalog.tenantID, nullableUUID(seriesID), (page-1)*pageSize, pageSize)
	if err != nil {
		return xiangwanadmin.InstancePage{}, fmt.Errorf("list admin Instances: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]xiangwanadmin.InstanceListItem, 0)
	for rows.Next() {
		item, err := scanAdminInstance(rows)
		if err != nil {
			return xiangwanadmin.InstancePage{}, fmt.Errorf("scan admin Instance: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.InstancePage{}, fmt.Errorf("iterate admin Instances: %w", err)
	}
	if err := rows.Close(); err != nil {
		return xiangwanadmin.InstancePage{}, fmt.Errorf("close admin Instances: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.InstancePage{}, fmt.Errorf("commit admin Instance read: %w", err)
	}
	return xiangwanadmin.InstancePage{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	}, nil
}

func (catalog *Catalog) GetInstance(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	instanceID uuid.UUID,
) (xiangwanadmin.InstanceDetail, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || instanceID == uuid.Nil {
		return xiangwanadmin.InstanceDetail{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.InstanceDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()
	repository := activitypostgres.NewRepository(tx)
	instance, err := repository.GetInstance(ctx, catalog.tenantID, instanceID)
	if errors.Is(err, activitypostgres.ErrInstanceNotFound) {
		return xiangwanadmin.InstanceDetail{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.InstanceDetail{}, err
	}
	series, err := repository.GetSeries(ctx, catalog.tenantID, instance.SeriesID)
	if err != nil {
		return xiangwanadmin.InstanceDetail{}, err
	}
	sessions, err := repository.ListInstanceSessions(ctx, catalog.tenantID, instanceID)
	if err != nil {
		return xiangwanadmin.InstanceDetail{}, err
	}
	questionnaire, err := readInstanceQuestionnaire(ctx, tx, catalog.tenantID, instanceID)
	if err != nil {
		return xiangwanadmin.InstanceDetail{}, err
	}
	var copyLineage *xiangwanadmin.InstanceCopyLineage
	var sourceID uuid.UUID
	var sourceVersion, sourcePresentationRevision int64
	var sourceQuestionnaireVersionID uuid.NullUUID
	err = tx.QueryRowContext(ctx, `
SELECT source_instance_id, source_instance_version, source_presentation_revision,
       source_questionnaire_version_id
FROM xiangwan_activity_instance_copies
WHERE tenant_id = $1 AND instance_id = $2
`, catalog.tenantID, instanceID).Scan(&sourceID, &sourceVersion,
		&sourcePresentationRevision, &sourceQuestionnaireVersionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.InstanceDetail{}, fmt.Errorf("read admin Instance copy lineage: %w", err)
	}
	if err == nil {
		copyLineage = &xiangwanadmin.InstanceCopyLineage{
			SourceInstanceID: sourceID, SourceInstanceVersion: sourceVersion,
			SourcePresentationRevision:   sourcePresentationRevision,
			SourceQuestionnaireVersionID: sourceQuestionnaireVersionID.UUID,
		}
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.InstanceDetail{}, fmt.Errorf("commit admin Instance detail read: %w", err)
	}
	return xiangwanadmin.InstanceDetail{
		Series: series, Instance: instance, Sessions: sessions,
		Questionnaire: questionnaire, CopyLineage: copyLineage,
	}, nil
}

// GetInstanceReviewStatus reads only the current, public side of the
// relation -> snapshot -> approved observation -> publication chain. It also
// runs the same public review projection used by the consumer endpoint, so a
// publication row alone cannot make the admin UI claim that a review is
// readable. The administrator grant is checked by the same authorized read
// transaction as the rest of the catalog. No Content body, storage key or raw
// external URL is returned through this surface.
func (catalog *Catalog) GetInstanceReviewStatus(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	instanceID uuid.UUID,
) (xiangwanadmin.InstanceReviewStatus, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || instanceID == uuid.Nil {
		return xiangwanadmin.InstanceReviewStatus{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.InstanceReviewStatus{}, err
	}
	defer func() { _ = tx.Rollback() }()

	activityRepository := activitypostgres.NewRepository(tx)
	instance, err := activityRepository.GetInstance(ctx, catalog.tenantID, instanceID)
	if errors.Is(err, activitypostgres.ErrInstanceNotFound) {
		return xiangwanadmin.InstanceReviewStatus{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.InstanceReviewStatus{}, err
	}
	sessions, err := activityRepository.ListInstanceSessions(
		ctx, catalog.tenantID, instanceID,
	)
	if err != nil {
		return xiangwanadmin.InstanceReviewStatus{}, err
	}

	status, err := readInstanceReviewStatus(
		ctx, catalog.tenantID, instance, sessions, catalog.externalDomains,
		resourcepostgres.NewRepository(tx),
	)
	if err != nil {
		return xiangwanadmin.InstanceReviewStatus{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.InstanceReviewStatus{}, fmt.Errorf(
			"commit admin Instance review status read: %w", err,
		)
	}
	return status, nil
}

func readInstanceReviewStatus(
	ctx context.Context,
	tenantID uuid.UUID,
	instance activity.Instance,
	sessions []activity.Session,
	externalDomains resource.ExternalDomainPolicy,
	resources publishedReviewStatusReader,
) (xiangwanadmin.InstanceReviewStatus, error) {
	status := xiangwanadmin.InstanceReviewStatus{
		InstanceID:           instance.ID,
		InstanceStatus:       instance.Status,
		PublicReviewEligible: instance.Status == activity.InstanceStatusCompleted || instance.Status == activity.InstanceStatusArchived,
		Sessions:             make([]xiangwanadmin.SessionReviewStatus, 0, len(sessions)),
	}
	if !status.PublicReviewEligible {
		for _, session := range sessions {
			status.Sessions = append(status.Sessions, xiangwanadmin.SessionReviewStatus{
				SessionID: session.ID, Status: session.Status,
			})
		}
		return status, nil
	}
	if resources == nil {
		return xiangwanadmin.InstanceReviewStatus{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	instanceResources, err := resources.ListPublishedInstanceReview(ctx, tenantID, instance.ID)
	if err != nil {
		return xiangwanadmin.InstanceReviewStatus{}, err
	}
	instanceReview, err := resources.ReadPublicReview(
		ctx, tenantID,
		resource.PastHighlightReviewTarget{SeriesID: instance.SeriesID, InstanceID: instance.ID},
		externalDomains,
	)
	if err != nil && !errors.Is(err, resourcepostgres.ErrPublicReviewTargetNotFound) {
		return xiangwanadmin.InstanceReviewStatus{}, err
	}
	status.InstanceReviewDocumentCount = countReviewDocuments(instanceReview, resource.RelationKindInstanceReview)
	status.PublicReviewAvailable = status.InstanceReviewDocumentCount > 0
	status.LatestPublishedAt = latestPublishedResourceTime(instanceResources)
	for _, session := range sessions {
		sessionID := session.ID
		publishedResources, listErr := resources.ListPublishedSessionResources(ctx, tenantID, session.ID, nil)
		if listErr != nil {
			return xiangwanadmin.InstanceReviewStatus{}, listErr
		}
		sessionReview, readErr := resources.ReadPublicReview(
			ctx, tenantID,
			resource.PastHighlightReviewTarget{
				SeriesID: instance.SeriesID, InstanceID: instance.ID, SessionID: &sessionID,
			},
			externalDomains,
		)
		if readErr != nil && !errors.Is(readErr, resourcepostgres.ErrPublicReviewTargetNotFound) {
			return xiangwanadmin.InstanceReviewStatus{}, readErr
		}
		sessionDocumentCount := countReviewDocuments(sessionReview, resource.RelationKindSessionResources)
		sessionStatus := xiangwanadmin.SessionReviewStatus{
			SessionID:             session.ID,
			Status:                session.Status,
			PublicResourceCount:   sessionDocumentCount,
			PublicReviewAvailable: sessionDocumentCount > 0,
			LatestPublishedAt:     latestPublishedResourceTime(publishedResources),
		}
		status.Sessions = append(status.Sessions, sessionStatus)
		if sessionStatus.PublicReviewAvailable {
			status.PublicReviewAvailable = true
			status.PublicSessionResourceCount += sessionDocumentCount
		}
		status.LatestPublishedAt = laterResourceTime(status.LatestPublishedAt, sessionStatus.LatestPublishedAt)
	}
	return status, nil
}

type publishedReviewStatusReader interface {
	ListPublishedInstanceReview(context.Context, uuid.UUID, uuid.UUID) ([]resource.PublishedResource, error)
	ListPublishedSessionResources(context.Context, uuid.UUID, uuid.UUID, *uuid.UUID) ([]resource.PublishedResource, error)
	ReadPublicReview(context.Context, uuid.UUID, resource.PastHighlightReviewTarget, resource.ExternalDomainPolicy) (resource.PublicReviewDetail, error)
}

func countReviewDocuments(detail resource.PublicReviewDetail, kind resource.RelationKind) int {
	count := 0
	for _, document := range detail.Documents {
		if document.Kind == kind {
			count++
		}
	}
	return count
}

func latestPublishedResourceTime(
	resources []resource.PublishedResource,
) *time.Time {
	var latest *time.Time
	for _, value := range resources {
		candidate := value.PublishedAt.UTC()
		latest = laterResourceTime(latest, &candidate)
	}
	return latest
}

func laterResourceTime(left, right *time.Time) *time.Time {
	if left == nil {
		if right == nil {
			return nil
		}
		copy := right.UTC()
		return &copy
	}
	if right == nil || !right.After(*left) {
		return left
	}
	copy := right.UTC()
	return &copy
}

func (catalog *Catalog) CreateInstance(
	ctx context.Context,
	command xiangwanadmin.CreateInstanceCommand,
) (result activity.Instance, resultErr error) {
	action := "instance.create"
	if command.SourceInstanceID != uuid.Nil {
		action = "instance.copy"
	}
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID, action,
			"operation", command.OperationID, command.RequestID, resultErr,
		)
	}()
	command.Title = strings.TrimSpace(command.Title)
	command.CoverImageURL = strings.TrimSpace(command.CoverImageURL)
	command.QuickTagCodes = append([]string(nil), command.QuickTagCodes...)
	detailBlocks, detailBlocksErr := activity.NormalizeDetailBlocks(command.DetailBlocks)
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.SeriesID == uuid.Nil || command.ExpectedSeriesVersion < 1 ||
		(command.IssueNo < 0) ||
		(command.SourceInstanceID == uuid.Nil &&
			(command.ExpectedSourceVersion != 0 || command.ExpectedSourcePresentationRevision != 0 ||
				command.ExpectedSourceQuestionnaireVersionID != uuid.Nil)) ||
		(command.SourceInstanceID != uuid.Nil &&
			(command.ExpectedSourceVersion < 1 || command.ExpectedSourcePresentationRevision < 1)) ||
		(command.Title != "" && utf8.RuneCountInString(command.Title) > 200) ||
		!validActivityType(command.ActivityType) ||
		!activity.ValidQuickTagCodes(command.QuickTagCodes) ||
		!activity.ValidInstanceCoverImageURL(command.CoverImageURL) ||
		detailBlocksErr != nil {
		return activity.Instance{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	command.DetailBlocks = detailBlocks
	digestPayload := struct {
		SeriesID      uuid.UUID              `json:"series_id"`
		SeriesVersion int64                  `json:"series_version"`
		IssueNo       int                    `json:"issue_no"`
		Title         string                 `json:"title"`
		ActivityType  activity.ActivityType  `json:"activity_type"`
		QuickTags     []string               `json:"quick_tag_codes"`
		CoverImageURL string                 `json:"cover_image_url"`
		DetailBlocks  []activity.DetailBlock `json:"detail_blocks"`
	}{command.SeriesID, command.ExpectedSeriesVersion, command.IssueNo, command.Title,
		command.ActivityType, command.QuickTagCodes, command.CoverImageURL,
		command.DetailBlocks}
	digest, err := commandDigest(digestPayload)
	if command.SourceInstanceID != uuid.Nil {
		digest, err = commandDigest(struct {
			Instance struct {
				SeriesID      uuid.UUID              `json:"series_id"`
				SeriesVersion int64                  `json:"series_version"`
				IssueNo       int                    `json:"issue_no"`
				Title         string                 `json:"title"`
				ActivityType  activity.ActivityType  `json:"activity_type"`
				QuickTags     []string               `json:"quick_tag_codes"`
				CoverImageURL string                 `json:"cover_image_url"`
				DetailBlocks  []activity.DetailBlock `json:"detail_blocks"`
			} `json:"instance"`
			SourceInstanceID                     uuid.UUID `json:"source_instance_id"`
			ExpectedSourceVersion                int64     `json:"expected_source_version"`
			ExpectedSourcePresentationRevision   int64     `json:"expected_source_presentation_revision"`
			ExpectedSourceQuestionnaireVersionID uuid.UUID `json:"expected_source_questionnaire_version_id"`
		}{digestPayload, command.SourceInstanceID, command.ExpectedSourceVersion,
			command.ExpectedSourcePresentationRevision,
			command.ExpectedSourceQuestionnaireVersionID})
	}
	if err != nil {
		return activity.Instance{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return activity.Instance{}, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, filename := range coverFilenamesFromCreateInstance(command) {
		if err := lockCoverFilename(ctx, tx.Tx, filename); err != nil {
			return activity.Instance{}, err
		}
	}
	if receipt, replay, err := readOperation[operationResult[activity.Instance]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		action, digest,
	); err != nil {
		return activity.Instance{}, err
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.Version < 1 {
			return activity.Instance{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Instance{}, fmt.Errorf("commit admin Instance replay: %w", err)
		}
		return receipt.Value, nil
	}
	// A replay must return the immutable receipt even when a later presentation
	// change orphaned this cover and GC tombstoned it. The tombstone fence applies
	// only to a new attachment.
	if err := rejectDeletedCoverReferences(ctx, tx.Tx, coverFilenamesFromCreateInstance(command)); err != nil {
		return activity.Instance{}, err
	}
	if err := activitypostgres.ValidatePublishedQuickTagCodes(
		ctx, tx.Tx, catalog.tenantID, command.QuickTagCodes,
	); err != nil {
		if errors.Is(err, activitypostgres.ErrPublicationQuickTagUnavailable) {
			return activity.Instance{}, xiangwanadmin.ErrQuickTagNotPublished
		}
		return activity.Instance{}, err
	}
	var seriesTitle string
	var seriesStatus activity.SeriesStatus
	var seriesVersion int64
	err = tx.QueryRowContext(ctx, `
SELECT title, status, version
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.SeriesID).Scan(&seriesTitle, &seriesStatus, &seriesVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Instance{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.Instance{}, fmt.Errorf("lock admin Series: %w", err)
	}
	if seriesStatus == activity.SeriesStatusArchived ||
		seriesVersion != command.ExpectedSeriesVersion {
		return activity.Instance{}, xiangwanadmin.ErrVersionConflict
	}
	if command.SourceInstanceID != uuid.Nil {
		var sourceVersion, sourcePresentationRevision int64
		if err := tx.QueryRowContext(ctx, `
SELECT version, presentation_revision
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2 AND id = $3
FOR SHARE
`, catalog.tenantID, command.SeriesID, command.SourceInstanceID).Scan(
			&sourceVersion, &sourcePresentationRevision,
		); errors.Is(err, sql.ErrNoRows) {
			return activity.Instance{}, xiangwanadmin.ErrTargetNotFound
		} else if err != nil {
			return activity.Instance{}, fmt.Errorf("lock copied Instance source: %w", err)
		}
		if sourceVersion != command.ExpectedSourceVersion ||
			sourcePresentationRevision != command.ExpectedSourcePresentationRevision {
			return activity.Instance{}, xiangwanadmin.ErrVersionConflict
		}
		var sourceQuestionnaireVersionID uuid.UUID
		err = tx.QueryRowContext(ctx, `
SELECT questionnaire_version_id
FROM xiangwan_instance_questionnaires
WHERE tenant_id = $1 AND instance_id = $2
ORDER BY assignment_version DESC
LIMIT 1
`, catalog.tenantID, command.SourceInstanceID).Scan(&sourceQuestionnaireVersionID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return activity.Instance{}, fmt.Errorf("read copied Instance questionnaire fence: %w", err)
		}
		if sourceQuestionnaireVersionID != command.ExpectedSourceQuestionnaireVersionID {
			return activity.Instance{}, xiangwanadmin.ErrVersionConflict
		}
	}
	issueNo := command.IssueNo
	if issueNo == 0 {
		if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(issue_no), 0) + 1
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2
`, catalog.tenantID, command.SeriesID).Scan(&issueNo); err != nil {
			return activity.Instance{}, fmt.Errorf("suggest admin Instance issue number: %w", err)
		}
	} else {
		var alreadyExists bool
		if err := tx.QueryRowContext(ctx, `
SELECT EXISTS(
    SELECT 1 FROM xiangwan_activity_instances
    WHERE tenant_id = $1 AND series_id = $2 AND issue_no = $3
)
`, catalog.tenantID, command.SeriesID, issueNo).Scan(&alreadyExists); err != nil {
			return activity.Instance{}, fmt.Errorf("check admin Instance issue number: %w", err)
		}
		if alreadyExists {
			return activity.Instance{}, xiangwanadmin.ErrVersionConflict
		}
	}
	if issueNo < 1 {
		return activity.Instance{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	canonicalTitle := formatInstanceTitle(issueNo, seriesTitle)
	if utf8.RuneCountInString(canonicalTitle) > 200 {
		return activity.Instance{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	value, err := activitypostgres.NewRepository(tx).CreateInstance(ctx, activity.Instance{
		TenantID: catalog.tenantID, SeriesID: command.SeriesID,
		IssueNo: issueNo, Title: canonicalTitle, Status: activity.InstanceStatusDraft,
		ActivityType:  &command.ActivityType,
		QuickTagCodes: command.QuickTagCodes,
		CoverImageURL: command.CoverImageURL,
		DetailBlocks:  command.DetailBlocks,
	})
	if err != nil {
		return activity.Instance{}, err
	}
	if command.SourceInstanceID != uuid.Nil {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_activity_instance_copies (
    tenant_id, series_id, instance_id, source_instance_id,
    source_instance_version, source_presentation_revision,
    source_questionnaire_version_id, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
`, catalog.tenantID, command.SeriesID, value.ID, command.SourceInstanceID,
			command.ExpectedSourceVersion, command.ExpectedSourcePresentationRevision,
			uuid.NullUUID{UUID: command.ExpectedSourceQuestionnaireVersionID,
				Valid: command.ExpectedSourceQuestionnaireVersionID != uuid.Nil},
			catalog.now().UTC()); err != nil {
			return activity.Instance{}, fmt.Errorf("record independent Instance copy lineage: %w", err)
		}
	}
	// Consume the matched Series version inside this transaction: without the
	// advance, a second command carrying the same stale expected_series_version
	// also passed the fence and created a duplicate draft Instance from one
	// reviewed Series state (codex review 2026-09-19; mirrors the Session
	// create advance below).
	advanceResult, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_series
SET version = version + 1, updated_at = $3
WHERE tenant_id = $1 AND id = $2 AND version = $4
`, catalog.tenantID, command.SeriesID, catalog.now().UTC(), command.ExpectedSeriesVersion)
	if err != nil {
		return activity.Instance{}, fmt.Errorf(
			"advance admin Series after Instance create: %w", err,
		)
	}
	if rows, rowsErr := advanceResult.RowsAffected(); rowsErr != nil {
		return activity.Instance{}, fmt.Errorf(
			"read advanced admin Series count: %w", rowsErr,
		)
	} else if rows != 1 {
		return activity.Instance{}, xiangwanadmin.ErrVersionConflict
	}
	if err := clearCoverGCMarkers(ctx, tx.Tx, coverFilenamesFromCreateInstance(command)); err != nil {
		return activity.Instance{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, action, digest,
		operationResult[activity.Instance]{Value: value}, value.ID, value.Version,
		command.RequestID, catalog.now().UTC(),
	); err != nil {
		return activity.Instance{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Instance{}, fmt.Errorf("commit admin Instance create: %w", err)
	}
	return value, nil
}

func (catalog *Catalog) CreateSession(
	ctx context.Context,
	command xiangwanadmin.CreateSessionCommand,
) (result activity.Session, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID, "session.create",
			"operation", command.OperationID, command.RequestID, resultErr,
		)
	}()
	command.Title = strings.TrimSpace(command.Title)
	command.VenueName = strings.TrimSpace(command.VenueName)
	command.Address = strings.TrimSpace(command.Address)
	command.OnlineParticipationMode = strings.TrimSpace(command.OnlineParticipationMode)
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.ExpectedInstanceVersion < 1 ||
		utf8.RuneCountInString(command.Title) < 1 ||
		utf8.RuneCountInString(command.Title) > 200 || command.SortOrder < 0 ||
		!sessionIntegersFitPostgres(
			command.Capacity,
			command.GroupMinimum,
			command.LowStockThreshold,
			command.SortOrder,
		) ||
		!validSessionLocationLengths(
			command.VenueName,
			command.OnlineParticipationMode,
		) {
		return activity.Session{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID              uuid.UUID             `json:"instance_id"`
		ExpectedInstanceVersion int64                 `json:"expected_instance_version"`
		Title                   string                `json:"title"`
		RegistrationStartAt     time.Time             `json:"registration_start_at"`
		RegistrationEndAt       time.Time             `json:"registration_end_at"`
		SessionStartAt          time.Time             `json:"session_start_at"`
		SessionEndAt            time.Time             `json:"session_end_at"`
		Capacity                int                   `json:"capacity"`
		GroupMinimum            int                   `json:"group_minimum"`
		LowStockThreshold       int                   `json:"low_stock_threshold"`
		PriceCents              int64                 `json:"price_cents"`
		DeliveryMode            activity.DeliveryMode `json:"delivery_mode"`
		Area                    activity.AreaCode     `json:"area"`
		VenueName               string                `json:"venue_name"`
		Address                 string                `json:"address"`
		Longitude               *float64              `json:"longitude,omitempty"`
		Latitude                *float64              `json:"latitude,omitempty"`
		OnlineMode              string                `json:"online_participation_mode"`
		OnlineCompliant         bool                  `json:"online_compliant"`
		SortOrder               int                   `json:"sort_order"`
	}{
		command.InstanceID, command.ExpectedInstanceVersion, command.Title,
		command.RegistrationStartAt, command.RegistrationEndAt,
		command.SessionStartAt, command.SessionEndAt, command.Capacity,
		command.GroupMinimum, command.LowStockThreshold, command.PriceCents,
		command.DeliveryMode, command.Area, command.VenueName, command.Address,
		command.Longitude, command.Latitude, command.OnlineParticipationMode,
		command.OnlineCompliant, command.SortOrder,
	})
	if err != nil {
		return activity.Session{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return activity.Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[activity.Session]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"session.create", digest,
	); err != nil {
		return activity.Session{}, err
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.Version < 1 {
			return activity.Session{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Session{}, fmt.Errorf("commit admin Session replay: %w", err)
		}
		return receipt.Value, nil
	}
	var seriesID uuid.UUID
	var instanceStatus activity.InstanceStatus
	var instanceVersion int64
	var activityType sql.NullString
	var quickTags activitypostgres.StringArrayJSON
	err = tx.QueryRowContext(ctx, `
SELECT series_id, status, version, activity_type, TO_JSON(quick_tag_codes)
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.InstanceID).Scan(
		&seriesID, &instanceStatus, &instanceVersion, &activityType, &quickTags,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Session{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.Session{}, fmt.Errorf("lock admin Instance: %w", err)
	}
	if instanceStatus != activity.InstanceStatusDraft ||
		instanceVersion != command.ExpectedInstanceVersion || !activityType.Valid {
		return activity.Session{}, xiangwanadmin.ErrVersionConflict
	}
	sessionID := uuid.New()
	candidate := publicationSessionCandidate(sessionID, command)
	if err := activity.CheckInstancePublication(activity.InstancePublicationCandidate{
		SeriesID: seriesID, InstanceID: command.InstanceID,
		ActivityType:  activity.ActivityType(activityType.String),
		QuickTagCodes: []string(quickTags),
		Sessions:      []activity.SessionPublicationCandidate{candidate},
	}); err != nil {
		return activity.Session{}, fmt.Errorf("%w: %w", xiangwanadmin.ErrPublicationInvalid, err)
	}
	value, err := activitypostgres.NewRepository(tx).CreateSession(
		ctx,
		activitySessionFromCommand(sessionID, catalog.tenantID, command),
	)
	if err != nil {
		return activity.Session{}, err
	}
	advanceResult, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_instances
SET version = version + 1, updated_at = $3
WHERE tenant_id = $1 AND id = $2 AND version = $4
`, catalog.tenantID, command.InstanceID, catalog.now().UTC(), command.ExpectedInstanceVersion)
	if err != nil {
		return activity.Session{}, fmt.Errorf("advance admin Instance after Session create: %w", err)
	}
	if rows, err := advanceResult.RowsAffected(); err != nil {
		return activity.Session{}, fmt.Errorf("read advanced admin Instance count: %w", err)
	} else if rows != 1 {
		return activity.Session{}, xiangwanadmin.ErrVersionConflict
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "session.create", digest,
		operationResult[activity.Session]{Value: value}, value.ID, value.Version,
		command.RequestID, catalog.now().UTC(),
	); err != nil {
		return activity.Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Session{}, fmt.Errorf("commit admin Session create: %w", err)
	}
	return value, nil
}

func (catalog *Catalog) PublishInstance(
	ctx context.Context,
	command xiangwanadmin.PublishInstanceCommand,
) (result activity.PublicationEvent, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID, "instance.publish",
			"instance", command.InstanceID, command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.ExpectedInstanceVersion < 1 {
		return activity.PublicationEvent{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	// Fail uniformly before exposing target existence or readiness details. The
	// preflight authorization and the reads it guards share one snapshot, and
	// the Publisher repeats this authorization inside the commit transaction.
	tx, err := catalog.beginAuthorizedRead(ctx, xiangwanadmin.Principal{
		PrincipalID: command.ActorID, IdentityLinkID: command.IdentityLinkID,
	})
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	repository := activitypostgres.NewRepository(tx)
	instance, err := repository.GetInstance(ctx, catalog.tenantID, command.InstanceID)
	if errors.Is(err, activitypostgres.ErrInstanceNotFound) {
		return activity.PublicationEvent{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	sessions, err := repository.ListInstanceSessions(ctx, catalog.tenantID, command.InstanceID)
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	if err := activitypostgres.ValidatePublishedQuickTagCodes(
		ctx, tx, catalog.tenantID, instance.QuickTagCodes,
	); err != nil {
		if errors.Is(err, activitypostgres.ErrPublicationQuickTagUnavailable) {
			return activity.PublicationEvent{}, xiangwanadmin.ErrQuickTagNotPublished
		}
		return activity.PublicationEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.PublicationEvent{}, fmt.Errorf(
			"commit admin publication preflight read: %w", err,
		)
	}
	readiness := activity.PublicationReferenceReadiness{
		QuestionnaireReady: true,
		PeopleReady:        true,
		ContentReady:       true,
		ResourcesReady:     true,
		QuickTagsReady:     true,
	}
	candidate := activity.InstancePublicationCandidate{
		SeriesID: instance.SeriesID, InstanceID: instance.ID,
		QuickTagCodes: append([]string(nil), instance.QuickTagCodes...),
		Sessions:      make([]activity.SessionPublicationCandidate, 0, len(sessions)),
	}
	if instance.ActivityType != nil {
		candidate.ActivityType = *instance.ActivityType
	}
	for _, session := range sessions {
		value, conversionErr := publicationCandidateFromSession(session, readiness)
		if conversionErr != nil {
			return activity.PublicationEvent{}, conversionErr
		}
		candidate.Sessions = append(candidate.Sessions, value)
	}
	if err := activity.CheckInstancePublication(candidate); err != nil {
		return activity.PublicationEvent{}, fmt.Errorf("%w: %w", xiangwanadmin.ErrPublicationInvalid, err)
	}
	event, err := catalog.publisher.Publish(ctx, activitypostgres.PublishInstanceCommand{
		TenantID: catalog.tenantID, PublishedBy: command.ActorID,
		AdminIdentityLinkID:     command.IdentityLinkID,
		ExpectedInstanceVersion: command.ExpectedInstanceVersion,
		Candidate:               candidate, IdempotencyKey: command.OperationID,
		RequestID: command.RequestID,
	})
	if errors.Is(err, activitypostgres.ErrPublicationConflict) ||
		errors.Is(err, activitypostgres.ErrPublicationSessionSet) ||
		errors.Is(err, activitypostgres.ErrPublicationState) {
		return activity.PublicationEvent{}, xiangwanadmin.ErrVersionConflict
	}
	if errors.Is(err, activitypostgres.ErrAdminPublicationForbidden) {
		return activity.PublicationEvent{}, xiangwanadmin.ErrScopeForbidden
	}
	if errors.Is(err, activitypostgres.ErrAdminPublicationGenerationInactive) {
		return activity.PublicationEvent{}, xiangwanadmin.ErrVersionConflict
	}
	if errors.Is(err, activitypostgres.ErrAdminPublicationOperationConflict) {
		return activity.PublicationEvent{}, xiangwanadmin.ErrOperationConflict
	}
	if errors.Is(err, activitypostgres.ErrPublicationQuickTagUnavailable) {
		return activity.PublicationEvent{}, xiangwanadmin.ErrQuickTagNotPublished
	}
	var invalid *activity.InvalidInstancePublicationError
	if errors.As(err, &invalid) {
		return activity.PublicationEvent{}, fmt.Errorf(
			"%w: %w", xiangwanadmin.ErrPublicationInvalid, err,
		)
	}
	return event, err
}

type completionSessionRow struct {
	Status       activity.SessionStatus
	SessionEndAt sql.NullTime
}

// completionSessionFacts converts the locked database facts into the domain
// closure input. A published Session is considered ended only when its own
// end instant has passed; a cancelled Session is already terminal regardless
// of its scheduled window. The returned count identifies the published rows
// that must be persisted as ended in the surrounding transaction.
func completionSessionFacts(
	rows []completionSessionRow,
	at time.Time,
) ([]activity.SessionTerminalFact, int) {
	facts := make([]activity.SessionTerminalFact, 0, len(rows))
	dueSessionCount := 0
	for _, row := range rows {
		switch row.Status {
		case activity.SessionStatusCancelled:
			facts = append(facts, activity.SessionTerminalFactCancelled)
		case activity.SessionStatusEnded, activity.SessionStatusArchived:
			facts = append(facts, activity.SessionTerminalFactEnded)
		case activity.SessionStatusPublished:
			if row.SessionEndAt.Valid && !row.SessionEndAt.Time.After(at) {
				facts = append(facts, activity.SessionTerminalFactEnded)
				dueSessionCount++
			} else {
				facts = append(facts, activity.SessionTerminalFactNonTerminal)
			}
		default:
			facts = append(facts, activity.SessionTerminalFactNonTerminal)
		}
	}
	return facts, dueSessionCount
}

// CompleteInstance moves a published Instance into its terminal
// completed state, which is what makes it eligible for the public
// past-activities catalog. It is an explicit operator decision. Published
// Sessions whose authoritative end instant has passed are sealed as ended in
// this same transaction; cancellation remains a terminal Session fact, so an
// Instance may complete when at least one Session ended and every other
// Session ended or was cancelled.
func (catalog *Catalog) CompleteInstance(
	ctx context.Context,
	command xiangwanadmin.CompleteInstanceCommand,
) (result activity.Instance, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID, "instance.complete",
			"instance", command.InstanceID, command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.ExpectedInstanceVersion < 1 {
		return activity.Instance{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID              uuid.UUID `json:"instance_id"`
		ExpectedInstanceVersion int64     `json:"expected_instance_version"`
	}{command.InstanceID, command.ExpectedInstanceVersion})
	if err != nil {
		return activity.Instance{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return activity.Instance{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[activity.Instance]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"instance.complete", digest,
	); err != nil {
		return activity.Instance{}, err
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.Version < 1 {
			return activity.Instance{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Instance{}, fmt.Errorf(
				"commit admin Instance completion replay: %w", err,
			)
		}
		return receipt.Value, nil
	}
	var status activity.InstanceStatus
	var version int64
	err = tx.QueryRowContext(ctx, `
SELECT instance.status, instance.version
FROM xiangwan_activity_instances AS instance
WHERE instance.tenant_id = $1 AND instance.id = $2
FOR UPDATE
`, catalog.tenantID, command.InstanceID).Scan(&status, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Instance{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.Instance{}, fmt.Errorf("lock admin Instance for completion: %w", err)
	}
	if status != activity.InstanceStatusPublished ||
		version != command.ExpectedInstanceVersion {
		return activity.Instance{}, xiangwanadmin.ErrVersionConflict
	}
	completionAt := catalog.now().UTC()
	sessionRows, err := tx.QueryContext(ctx, `
SELECT status, session_end_at
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2
ORDER BY id
FOR UPDATE
`, catalog.tenantID, command.InstanceID)
	if err != nil {
		return activity.Instance{}, fmt.Errorf("lock admin Instance Sessions for completion: %w", err)
	}
	completionRows := make([]completionSessionRow, 0)
	for sessionRows.Next() {
		var sessionStatus string
		var sessionEndAt sql.NullTime
		if err := sessionRows.Scan(&sessionStatus, &sessionEndAt); err != nil {
			_ = sessionRows.Close()
			return activity.Instance{}, fmt.Errorf("scan admin Instance Session for completion: %w", err)
		}
		completionRows = append(completionRows, completionSessionRow{
			Status:       activity.SessionStatus(sessionStatus),
			SessionEndAt: sessionEndAt,
		})
	}
	if err := sessionRows.Err(); err != nil {
		_ = sessionRows.Close()
		return activity.Instance{}, fmt.Errorf("iterate admin Instance Sessions for completion: %w", err)
	}
	if err := sessionRows.Close(); err != nil {
		return activity.Instance{}, fmt.Errorf("close admin Instance Sessions for completion: %w", err)
	}
	facts, dueSessionCount := completionSessionFacts(completionRows, completionAt)
	decision, err := activity.DecideInstanceClosure(facts)
	if err != nil {
		if errors.Is(err, activity.ErrInstanceHasNoSessions) {
			return activity.Instance{}, xiangwanadmin.ErrInstanceNotEnded
		}
		return activity.Instance{}, fmt.Errorf("decide admin Instance completion: %w", err)
	}
	if decision.Closure != activity.InstanceClosureCompleted {
		return activity.Instance{}, xiangwanadmin.ErrInstanceNotEnded
	}
	if dueSessionCount > 0 {
		endResult, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_sessions
SET status = 'ended', version = version + 1, updated_at = $3
WHERE tenant_id = $1
  AND instance_id = $2
  AND status = 'published'
  AND session_end_at IS NOT NULL
  AND session_end_at <= $3
`, catalog.tenantID, command.InstanceID, completionAt)
		if err != nil {
			return activity.Instance{}, fmt.Errorf("end admin Instance Sessions for completion: %w", err)
		}
		if rows, rowsErr := endResult.RowsAffected(); rowsErr != nil {
			return activity.Instance{}, fmt.Errorf("read ended admin Instance Session count: %w", rowsErr)
		} else if rows != int64(dueSessionCount) {
			return activity.Instance{}, xiangwanadmin.ErrVersionConflict
		}
	}
	advanceResult, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_instances
SET status = $4, completed_at = clock_timestamp(), version = version + 1, updated_at = clock_timestamp()
WHERE tenant_id = $1 AND id = $2 AND version = $3 AND status = 'published'
`, catalog.tenantID, command.InstanceID, command.ExpectedInstanceVersion,
		activity.InstanceStatusCompleted,
	)
	if err != nil {
		return activity.Instance{}, fmt.Errorf(
			"advance admin Instance completion: %w", err,
		)
	}
	if rows, rowsErr := advanceResult.RowsAffected(); rowsErr != nil {
		return activity.Instance{}, fmt.Errorf(
			"read advanced admin Instance completion count: %w", rowsErr,
		)
	} else if rows != 1 {
		return activity.Instance{}, xiangwanadmin.ErrVersionConflict
	}
	value, err := activitypostgres.NewRepository(tx).GetInstance(
		ctx, catalog.tenantID, command.InstanceID,
	)
	if err != nil {
		return activity.Instance{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "instance.complete", digest,
		operationResult[activity.Instance]{Value: value}, value.ID, value.Version,
		command.RequestID, catalog.now().UTC(),
	); err != nil {
		return activity.Instance{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Instance{}, fmt.Errorf(
			"commit admin Instance completion: %w", err,
		)
	}
	return value, nil
}

// UpdateInstance patches the presentational fields (cover image and detail
// content blocks) of one Instance without touching lifecycle, schedule, or
// any other fact. It mirrors the publish/complete write pattern: one
// serializable transaction holding the operation lock, the original-response
// receipt, and the audit event. The concurrency fence is the dedicated
// presentation_revision (request key expected_presentation_revision), not the
// operational version: public instance_review resources pin
// expected_target_version = instance.version, so a cosmetic edit used to
// bump version and silently take every published review of that Instance
// offline. The revision advances only when cover_image_url or detail_blocks
// actually change; a byte-identical PATCH is a successful no-op. The create,
// publish, and complete paths keep their existing version semantics.
func (catalog *Catalog) UpdateInstance(
	ctx context.Context,
	command xiangwanadmin.UpdateInstanceCommand,
) (result activity.Instance, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID, "instance.update",
			"instance", command.InstanceID, command.RequestID, resultErr,
		)
	}()
	var coverImageURL string
	if command.CoverImageURL != nil {
		coverImageURL = strings.TrimSpace(*command.CoverImageURL)
	}
	var detailBlocks []activity.DetailBlock
	if command.DetailBlocks != nil {
		normalized, err := activity.NormalizeDetailBlocks(*command.DetailBlocks)
		if err != nil {
			return activity.Instance{}, xiangwanadmin.ErrInvalidCatalogRequest
		}
		detailBlocks = normalized
	}
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.ExpectedPresentationRevision < 1 ||
		(command.CoverImageURL == nil && command.DetailBlocks == nil) ||
		(command.CoverImageURL != nil &&
			!activity.ValidInstanceCoverImageURL(coverImageURL)) {
		return activity.Instance{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID                   uuid.UUID               `json:"instance_id"`
		ExpectedPresentationRevision int64                   `json:"expected_presentation_revision"`
		CoverImageURL                *string                 `json:"cover_image_url"`
		DetailBlocks                 *[]activity.DetailBlock `json:"detail_blocks"`
	}{command.InstanceID, command.ExpectedPresentationRevision,
		command.CoverImageURL, command.DetailBlocks})
	if err != nil {
		return activity.Instance{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return activity.Instance{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// Cover GC and presentation edits share a PostgreSQL advisory lock per
	// stored filename. This keeps a collector from deleting an object between
	// its reference probe and this transaction's attachment.
	for _, filename := range coverFilenamesFromInstanceUpdate(command, coverImageURL, detailBlocks) {
		if err := lockCoverFilename(ctx, tx.Tx, filename); err != nil {
			return activity.Instance{}, err
		}
	}
	if receipt, replay, err := readOperation[operationResult[activity.Instance]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"instance.update", digest,
	); err != nil {
		return activity.Instance{}, err
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.Version < 1 {
			return activity.Instance{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Instance{}, fmt.Errorf("commit admin Instance update replay: %w", err)
		}
		return receipt.Value, nil
	}
	// A replay must return the immutable receipt even when a later presentation
	// change orphaned this cover and GC tombstoned it. The tombstone fence applies
	// only to a new attachment.
	if err := rejectDeletedCoverReferences(
		ctx, tx.Tx, coverFilenamesFromInstanceUpdate(command, coverImageURL, detailBlocks),
	); err != nil {
		return activity.Instance{}, err
	}
	var storedCoverImageURL string
	var storedDetailBlocks []byte
	var presentationRevision int64
	err = tx.QueryRowContext(ctx, `
SELECT cover_image_url, detail_blocks, presentation_revision
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.InstanceID).Scan(
		&storedCoverImageURL, &storedDetailBlocks, &presentationRevision,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Instance{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.Instance{}, fmt.Errorf("lock admin Instance for update: %w", err)
	}
	if presentationRevision != command.ExpectedPresentationRevision {
		return activity.Instance{}, xiangwanadmin.ErrVersionConflict
	}
	coverChanged := command.CoverImageURL != nil && coverImageURL != storedCoverImageURL
	blocksChanged := false
	if command.DetailBlocks != nil {
		projected, projectErr := activity.ProjectDetailBlocks(storedDetailBlocks)
		if projectErr != nil {
			return activity.Instance{}, fmt.Errorf(
				"decode stored admin Instance detail blocks: %w", projectErr,
			)
		}
		incoming, encodeErr := json.Marshal(detailBlocks)
		if encodeErr != nil {
			return activity.Instance{}, fmt.Errorf(
				"encode incoming admin Instance detail blocks: %w", encodeErr,
			)
		}
		stored, encodeErr := json.Marshal(projected)
		if encodeErr != nil {
			return activity.Instance{}, fmt.Errorf(
				"encode stored admin Instance detail blocks: %w", encodeErr,
			)
		}
		blocksChanged = string(incoming) != string(stored)
	}
	if coverChanged || blocksChanged {
		updateQuery := `
UPDATE xiangwan_activity_instances
SET presentation_revision = presentation_revision + 1, updated_at = $3
`
		args := []any{catalog.tenantID, command.InstanceID, catalog.now().UTC()}
		if command.CoverImageURL != nil {
			args = append(args, coverImageURL)
			updateQuery += fmt.Sprintf(", cover_image_url = $%d\n", len(args))
		}
		if command.DetailBlocks != nil {
			encoded, encodeErr := json.Marshal(detailBlocks)
			if encodeErr != nil {
				return activity.Instance{}, fmt.Errorf(
					"encode admin Instance detail blocks: %w", encodeErr,
				)
			}
			args = append(args, string(encoded))
			updateQuery += fmt.Sprintf(", detail_blocks = $%d::JSONB\n", len(args))
		}
		args = append(args, command.ExpectedPresentationRevision)
		updateQuery += fmt.Sprintf(
			"WHERE tenant_id = $1 AND id = $2 AND presentation_revision = $%d\n", len(args),
		)
		advanceResult, err := tx.ExecContext(ctx, updateQuery, args...)
		if err != nil {
			return activity.Instance{}, fmt.Errorf("advance admin Instance presentation: %w", err)
		}
		if rows, rowsErr := advanceResult.RowsAffected(); rowsErr != nil {
			return activity.Instance{}, fmt.Errorf(
				"read advanced admin Instance presentation count: %w", rowsErr,
			)
		} else if rows != 1 {
			return activity.Instance{}, xiangwanadmin.ErrVersionConflict
		}
	}
	if err := clearCoverGCMarkers(
		ctx, tx.Tx, coverFilenamesFromInstanceUpdate(command, coverImageURL, detailBlocks),
	); err != nil {
		return activity.Instance{}, err
	}
	value, err := activitypostgres.NewRepository(tx).GetInstance(
		ctx, catalog.tenantID, command.InstanceID,
	)
	if err != nil {
		return activity.Instance{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "instance.update", digest,
		operationResult[activity.Instance]{Value: value}, value.ID, value.Version,
		command.RequestID, catalog.now().UTC(),
	); err != nil {
		return activity.Instance{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Instance{}, fmt.Errorf("commit admin Instance update: %w", err)
	}
	return value, nil
}

// beginAuthorizedRead locks the live authorization rows before opening one
// coherent catalog snapshot. A concurrent revocation must commit after this
// read transaction rather than between its authorization and data statements.
func (catalog *Catalog) beginAuthorizedRead(
	ctx context.Context,
	principal xiangwanadmin.Principal,
) (*sql.Tx, error) {
	tx, err := catalog.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
	})
	if err != nil {
		return nil, fmt.Errorf("begin administrator read: %w", err)
	}
	if err := catalog.authorizer.lockActivityOperatorIdentity(
		ctx, tx, principal.PrincipalID, principal.IdentityLinkID,
	); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Finance access is a separate, live tenant Grant. The same transaction holds
// its authorization row lock through the sensitive read and access audit.
func (catalog *Catalog) beginAuthorizedFinanceRead(
	ctx context.Context,
	principal xiangwanadmin.Principal,
) (*sql.Tx, error) {
	tx, err := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("begin administrator finance read: %w", err)
	}
	if err := catalog.authorizer.require(
		ctx, tx, principal.PrincipalID, &principal.IdentityLinkID,
		string(xiangwanadmin.CapabilityFinance), nil,
	); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Order amounts are visible to either activity operations or finance; the
// second path is attempted only on an actual scope denial, never a DB error.
func (catalog *Catalog) beginAuthorizedOrderRead(
	ctx context.Context,
	principal xiangwanadmin.Principal,
) (*sql.Tx, error) {
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if errors.Is(err, xiangwanadmin.ErrScopeForbidden) {
		return catalog.beginAuthorizedFinanceRead(ctx, principal)
	}
	return tx, err
}

func coverFilenamesFromInstanceUpdate(
	command xiangwanadmin.UpdateInstanceCommand,
	coverURL string,
	detailBlocks []activity.DetailBlock,
) []string {
	values := make([]string, 0, 1+len(detailBlocks))
	if command.CoverImageURL != nil {
		values = append(values, coverURL)
	}
	if command.DetailBlocks != nil {
		for _, block := range detailBlocks {
			if block.Type == activity.DetailBlockTypeImage {
				values = append(values, block.URL)
			}
		}
	}
	return coverFilenamesFromReferences(values...)
}

func coverFilenamesFromCreateInstance(command xiangwanadmin.CreateInstanceCommand) []string {
	values := []string{command.CoverImageURL}
	for _, block := range command.DetailBlocks {
		if block.Type == activity.DetailBlockTypeImage {
			values = append(values, block.URL)
		}
	}
	return coverFilenamesFromReferences(values...)
}

// formatInstanceTitle is the single server-side title rule shared by newly
// created periods. The prototype and PRD use the compact Chinese form
// “第X期系列名”; keeping it derived from the immutable issue number prevents
// edits to a Series name or publication count from changing a period's title.
func formatInstanceTitle(issueNo int, seriesTitle string) string {
	return fmt.Sprintf("第%d期%s", issueNo, strings.TrimSpace(seriesTitle))
}

func coverFilenamesFromReferences(values ...string) []string {
	seen := make(map[string]struct{})
	filenames := make([]string, 0, len(values))
	for _, value := range values {
		for _, filename := range coverFilenamePattern.FindAllString(value, -1) {
			if _, ok := seen[filename]; ok {
				continue
			}
			seen[filename] = struct{}{}
			filenames = append(filenames, filename)
		}
	}
	sort.Strings(filenames)
	return filenames
}

func clearCoverGCMarkers(ctx context.Context, tx *sql.Tx, filenames []string) error {
	for _, filename := range filenames {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM xiangwan_cover_gc_markers
WHERE filename = $1
`, filename); err != nil {
			return fmt.Errorf("clear attached cover GC marker: %w", err)
		}
	}
	return nil
}

func rejectDeletedCoverReferences(ctx context.Context, tx *sql.Tx, filenames []string) error {
	for _, filename := range filenames {
		var deleted bool
		if err := tx.QueryRowContext(ctx, `
SELECT deleted_at IS NOT NULL
FROM xiangwan_cover_gc_markers
WHERE filename = $1
`, filename).Scan(&deleted); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read attached cover deletion state: %w", err)
		}
		if deleted {
			return xiangwanadmin.ErrInvalidCatalogRequest
		}
	}
	return nil
}

func rejectDeletedBrandHeroReferences(ctx context.Context, tx *sql.Tx, filenames []string) error {
	for _, filename := range filenames {
		var deleted bool
		if err := tx.QueryRowContext(ctx, `
SELECT deleted_at IS NOT NULL
FROM xiangwan_brand_hero_gc_markers
WHERE filename = $1
`, filename).Scan(&deleted); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read brand hero deletion state: %w", err)
		}
		if deleted {
			return xiangwanadmin.ErrInvalidCatalogRequest
		}
	}
	return nil
}

func clearBrandHeroGCMarkers(ctx context.Context, tx *sql.Tx, filenames []string) error {
	for _, filename := range filenames {
		if _, err := tx.ExecContext(ctx, `
DELETE FROM xiangwan_brand_hero_gc_markers
WHERE filename = $1
`, filename); err != nil {
			return fmt.Errorf("clear attached brand hero GC marker: %w", err)
		}
	}
	return nil
}

func (catalog *Catalog) beginActivityWrite(
	ctx context.Context,
	actorID uuid.UUID,
	identityLinkID uuid.UUID,
	operationID uuid.UUID,
) (*activityWriteTransaction, error) {
	conn, release, err := catalog.lockAdminOperation(ctx, actorID, operationID)
	if err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf(
			"begin administrator write: %w",
			errors.Join(err, release()),
		)
	}
	write := &activityWriteTransaction{Tx: tx, release: release}
	var writeEpoch int64
	err = tx.QueryRowContext(ctx, `
SELECT write_epoch
FROM xiangwan_runtime_generations
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
  AND active_generation_id = $2
  AND write_epoch > 0
  AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, catalog.tenantID, catalog.generationID).Scan(&writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		_ = write.Rollback()
		return nil, xiangwanadmin.ErrVersionConflict
	}
	if err != nil {
		_ = write.Rollback()
		return nil, fmt.Errorf("lock administrator generation: %w", err)
	}
	if err := catalog.authorizer.RequireActivityOperatorIdentity(
		ctx, tx, actorID, identityLinkID,
	); err != nil {
		_ = write.Rollback()
		return nil, err
	}
	return write, nil
}

type activityWriteTransaction struct {
	*sql.Tx
	release func() error
}

func (transaction *activityWriteTransaction) Commit() error {
	return errors.Join(transaction.Tx.Commit(), transaction.release())
}

func (transaction *activityWriteTransaction) Rollback() error {
	rollbackErr := transaction.Tx.Rollback()
	if errors.Is(rollbackErr, sql.ErrTxDone) {
		rollbackErr = nil
	}
	return errors.Join(rollbackErr, transaction.release())
}

func (catalog *Catalog) lockAdminOperation(
	ctx context.Context,
	actorID uuid.UUID,
	operationID uuid.UUID,
) (*sql.Conn, func() error, error) {
	conn, err := catalog.db.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("open administrator operation lock connection: %w", err)
	}
	lockKey := catalog.tenantID.String() + ":" + actorID.String() +
		":" + operationID.String()
	var locked bool
	err = conn.QueryRowContext(ctx, `
SELECT TRUE
FROM (SELECT pg_advisory_lock(hashtextextended($1, 0))) AS operation_lock
`, lockKey).Scan(&locked)
	if err != nil || !locked {
		discardAdminOperationLockConnection(conn)
		if err == nil {
			err = xiangwanadmin.ErrOperationConflict
		}
		return nil, nil, fmt.Errorf("lock administrator operation: %w", err)
	}
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			releaseCtx, cancelRelease := context.WithTimeout(
				context.WithoutCancel(ctx), adminOperationLockReleaseTimeout,
			)
			defer cancelRelease()
			var unlocked bool
			unlockErr := conn.QueryRowContext(
				releaseCtx,
				`SELECT pg_advisory_unlock(hashtextextended($1, 0))`,
				lockKey,
			).Scan(&unlocked)
			if unlockErr == nil && !unlocked {
				unlockErr = errors.New(
					"administrator operation lock was not held by its connection",
				)
			}
			if unlockErr != nil {
				discardAdminOperationLockConnection(conn)
				releaseErr = unlockErr
				return
			}
			releaseErr = conn.Close()
		})
		return releaseErr
	}
	return conn, release, nil
}

func discardAdminOperationLockConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func (catalog *Catalog) valid(ctx context.Context) bool {
	return catalog != nil && catalog.db != nil && catalog.tenantID != uuid.Nil &&
		catalog.generationID != uuid.Nil && catalog.authorizer != nil &&
		catalog.publisher != nil && catalog.now != nil && ctx != nil
}

func normalizePage(page int, pageSize int) (int, int, error) {
	if page == 0 {
		page = 1
	}
	if pageSize == 0 {
		pageSize = xiangwanadmin.DefaultPageSize
	}
	if page < 1 || pageSize < 1 || pageSize > xiangwanadmin.MaxPageSize {
		return 0, 0, xiangwanadmin.ErrInvalidCatalogRequest
	}
	return page, pageSize, nil
}

func validSessionLocationLengths(venueName string, onlineMode string) bool {
	return utf8.RuneCountInString(venueName) <= maxVenueNameLength &&
		utf8.RuneCountInString(onlineMode) <= maxOnlineParticipationModeLength
}

// encodeBrandQuickTags keeps an empty tag list a JSON array. json.Marshal
// renders a nil slice as null, and the xiangwan_valid_brand_quick_tags CHECK
// rejects a non-array, so a first publish without quick tags would surface
// as SQLSTATE 23514 instead of succeeding (2026-09-19 production incident).
func encodeBrandQuickTags(tags []activity.HomeQuickTag) ([]byte, error) {
	if tags == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(tags)
}

// removedBrandTagCodes lists the codes present in the currently published
// tag list but absent from the incoming one.
func removedBrandTagCodes(
	published []activity.HomeQuickTag,
	next []activity.HomeQuickTag,
) []string {
	nextCodes := make(map[string]struct{}, len(next))
	for _, tag := range next {
		nextCodes[tag.Code] = struct{}{}
	}
	removed := make([]string, 0)
	for _, tag := range published {
		if _, kept := nextCodes[tag.Code]; !kept {
			removed = append(removed, tag.Code)
		}
	}
	return removed
}

// classifyBrandPublicationWriteError maps a PostgreSQL CHECK violation on the
// immutable BrandProfile publication back to a client error. A CHECK firing
// here means a domain-validation gap, never a server fault: reporting 500
// sends the operator hunting for an outage instead of their input.
func classifyBrandPublicationWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23514" {
		return xiangwanadmin.ErrInvalidCatalogRequest
	}
	return err
}

func sessionIntegersFitPostgres(values ...int) bool {
	for _, value := range values {
		if value > maxPostgresInteger {
			return false
		}
	}
	return true
}

func validWriteIdentity(
	actorID uuid.UUID,
	identityLinkID uuid.UUID,
	operationID uuid.UUID,
	requestID string,
) bool {
	return actorID != uuid.Nil && identityLinkID != uuid.Nil &&
		operationID != uuid.Nil && requestID != "" &&
		len(requestID) <= 128 && strings.TrimSpace(requestID) == requestID &&
		!strings.ContainsAny(requestID, "\r\n\x00")
}

func validActivityType(value activity.ActivityType) bool {
	switch value {
	case activity.ActivityTypeAIRoundtable,
		activity.ActivityTypeSpecialEvent,
		activity.ActivityTypeCourse,
		activity.ActivityTypeCompetition,
		activity.ActivityTypeCustom:
		return true
	default:
		return false
	}
}

func commandDigest(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode administrator operation: %w", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

type operationResult[T any] struct {
	Value T `json:"value"`
}

func readOperation[T any](
	ctx context.Context,
	tx activitypostgres.DBTX,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	operationID uuid.UUID,
	kind string,
	digest string,
) (T, bool, error) {
	var zero T
	var storedKind string
	var storedDigest string
	var raw []byte
	err := tx.QueryRowContext(ctx, `
SELECT operation_kind, request_digest, result
FROM xiangwan_admin_operations
WHERE tenant_id = $1 AND actor_id = $2 AND operation_id = $3
FOR UPDATE
`, tenantID, actorID, operationID).Scan(&storedKind, &storedDigest, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, false, nil
	}
	if err != nil {
		return zero, false, fmt.Errorf("read administrator operation: %w", err)
	}
	if storedKind != kind || storedDigest != digest {
		return zero, false, xiangwanadmin.ErrOperationConflict
	}
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return zero, false, xiangwanadmin.ErrOperationConflict
	}
	return result, true, nil
}

func writeOperationAndAudit(
	ctx context.Context,
	tx activitypostgres.DBTX,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	identityLinkID uuid.UUID,
	operationID uuid.UUID,
	kind string,
	digest string,
	operationResult any,
	targetID uuid.UUID,
	resultVersion int64,
	requestID string,
	now time.Time,
) error {
	result, err := json.Marshal(operationResult)
	if err != nil {
		return fmt.Errorf("encode administrator operation result: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_operations (
    tenant_id, actor_id, operation_id, operation_kind,
    request_digest, result, created_at
) VALUES ($1, $2, $3, $4, $5, $6::JSONB, $7)
`, tenantID, actorID, operationID, kind, digest, string(result), now); err != nil {
		return fmt.Errorf("store administrator operation: %w", err)
	}
	details, err := json.Marshal(map[string]any{
		"identity_link_id": identityLinkID.String(),
		"operation_id":     operationID.String(),
		"result_id":        targetID.String(),
		"result_version":   resultVersion,
	})
	if err != nil {
		return fmt.Errorf("encode administrator audit details: %w", err)
	}
	targetType := strings.Split(kind, ".")[0]
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::JSONB, $9, $9)
`, uuid.New(), tenantID, actorID, kind, targetType, targetID, requestID,
		string(details), now); err != nil {
		return fmt.Errorf("store administrator audit event: %w", err)
	}
	return nil
}

func (catalog *Catalog) auditRejectedCommand(
	ctx context.Context,
	actorID uuid.UUID,
	identityLinkID uuid.UUID,
	operationID uuid.UUID,
	kind string,
	targetType string,
	targetID uuid.UUID,
	requestID string,
	commandErr error,
) error {
	reasonCode, auditable := rejectedCommandReason(commandErr)
	if !auditable || ctx == nil || catalog == nil || catalog.db == nil || catalog.now == nil ||
		catalog.tenantID == uuid.Nil || actorID == uuid.Nil ||
		identityLinkID == uuid.Nil ||
		operationID == uuid.Nil || targetID == uuid.Nil || requestID == "" ||
		len(requestID) > 128 || strings.TrimSpace(requestID) != requestID ||
		strings.ContainsAny(requestID, "\r\n\x00") {
		return commandErr
	}
	details, err := json.Marshal(map[string]string{
		"outcome":          "rejected",
		"reason_code":      reasonCode,
		"operation_id":     operationID.String(),
		"identity_link_id": identityLinkID.String(),
	})
	if err != nil {
		return errors.Join(commandErr, fmt.Errorf("encode rejected administrator audit: %w", err))
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	now := catalog.now().UTC()
	_, err = catalog.db.ExecContext(auditCtx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::JSONB, $9, $9)
`, uuid.New(), catalog.tenantID, actorID, kind+".rejected", targetType,
		targetID, requestID, string(details), now)
	if err != nil {
		return errors.Join(commandErr, fmt.Errorf("store rejected administrator audit: %w", err))
	}
	return commandErr
}

func rejectedCommandReason(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, xiangwanadmin.ErrInvalidCatalogRequest):
		return "invalid_request", true
	case errors.Is(err, xiangwanadmin.ErrOperationConflict):
		return "operation_conflict", true
	case errors.Is(err, xiangwanadmin.ErrVersionConflict):
		return "version_conflict", true
	case errors.Is(err, xiangwanadmin.ErrTargetNotFound):
		return "target_not_found", true
	case errors.Is(err, xiangwanadmin.ErrPublicationInvalid):
		return "publication_invalid", true
	case errors.Is(err, xiangwanadmin.ErrArchiveNotAllowed):
		return "archive_not_allowed", true
	case errors.Is(err, xiangwanadmin.ErrScopeForbidden):
		return "scope_forbidden", true
	default:
		// A rejection the domain errors do not describe is still a rejection:
		// DB-level failures (serialization aborts, constraint rejections) used
		// to vanish from the audit trail exactly when they were hardest to
		// diagnose (2026-09-19 incident). Classify them instead of skipping.
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			switch postgresError.Code {
			case "40001", "40P01":
				return "store_serialization_conflict", true
			case "23503", "23505", "23514":
				return "store_constraint_rejected", true
			default:
				return "store_error", true
			}
		}
		return "", false
	}
}

func nullableUUID(value *uuid.UUID) any {
	if value == nil {
		return nil
	}
	return *value
}

func publicationSessionCandidate(
	sessionID uuid.UUID,
	command xiangwanadmin.CreateSessionCommand,
) activity.SessionPublicationCandidate {
	result := activity.SessionPublicationCandidate{
		SessionID: sessionID, Title: command.Title,
		RegistrationStartAt: command.RegistrationStartAt,
		RegistrationEndAt:   command.RegistrationEndAt,
		SessionStartAt:      command.SessionStartAt,
		SessionEndAt:        command.SessionEndAt,
		Capacity:            command.Capacity, GroupMinimum: command.GroupMinimum,
		PriceCents: command.PriceCents, DeliveryMode: command.DeliveryMode,
		Area: command.Area,
		References: activity.PublicationReferenceReadiness{
			QuestionnaireReady: true, PeopleReady: true, ContentReady: true,
			ResourcesReady: true, QuickTagsReady: true,
		},
	}
	result.LowStockThreshold = &command.LowStockThreshold
	if command.DeliveryMode == activity.DeliveryModeOffline {
		result.OfflineLocation = &activity.OfflineLocation{
			VenueName: command.VenueName, Address: command.Address,
			Longitude: command.Longitude, Latitude: command.Latitude,
		}
	} else if command.DeliveryMode == activity.DeliveryModeOnline {
		result.OnlineParticipation = &activity.OnlineParticipation{
			Mode:      command.OnlineParticipationMode,
			Compliant: command.OnlineCompliant,
		}
	}
	return result
}

func activitySessionFromCommand(
	sessionID uuid.UUID,
	tenantID uuid.UUID,
	command xiangwanadmin.CreateSessionCommand,
) activity.Session {
	result := activity.Session{
		ID: sessionID, TenantID: tenantID, InstanceID: command.InstanceID,
		Title: command.Title, Status: activity.SessionStatusDraft,
		RegistrationStartAt: &command.RegistrationStartAt,
		RegistrationEndAt:   &command.RegistrationEndAt,
		SessionStartAt:      &command.SessionStartAt,
		SessionEndAt:        &command.SessionEndAt,
		Capacity:            &command.Capacity, GroupMinimum: &command.GroupMinimum,
		LowStockThreshold: &command.LowStockThreshold,
		PriceCents:        &command.PriceCents, DeliveryMode: &command.DeliveryMode,
		Area: &command.Area, SortOrder: command.SortOrder,
	}
	if command.DeliveryMode == activity.DeliveryModeOffline {
		result.VenueName = &command.VenueName
		result.Address = &command.Address
		result.Longitude = command.Longitude
		result.Latitude = command.Latitude
	} else if command.DeliveryMode == activity.DeliveryModeOnline {
		result.OnlineParticipationMode = &command.OnlineParticipationMode
		result.OnlineParticipationCompliant = &command.OnlineCompliant
	}
	return result
}

func publicationCandidateFromSession(
	session activity.Session,
	readiness activity.PublicationReferenceReadiness,
) (activity.SessionPublicationCandidate, error) {
	if session.RegistrationStartAt == nil || session.RegistrationEndAt == nil ||
		session.SessionStartAt == nil || session.SessionEndAt == nil ||
		session.Capacity == nil || session.GroupMinimum == nil ||
		session.LowStockThreshold == nil || session.PriceCents == nil ||
		session.DeliveryMode == nil || session.Area == nil {
		return activity.SessionPublicationCandidate{}, xiangwanadmin.ErrPublicationInvalid
	}
	value := activity.SessionPublicationCandidate{
		SessionID: session.ID, Title: session.Title,
		RegistrationStartAt: *session.RegistrationStartAt,
		RegistrationEndAt:   *session.RegistrationEndAt,
		SessionStartAt:      *session.SessionStartAt,
		SessionEndAt:        *session.SessionEndAt,
		Capacity:            *session.Capacity, GroupMinimum: *session.GroupMinimum,
		LowStockThreshold: session.LowStockThreshold,
		PriceCents:        *session.PriceCents, DeliveryMode: *session.DeliveryMode,
		Area: *session.Area, References: readiness,
	}
	if *session.DeliveryMode == activity.DeliveryModeOffline {
		if session.VenueName == nil || session.Address == nil {
			return activity.SessionPublicationCandidate{}, xiangwanadmin.ErrPublicationInvalid
		}
		value.OfflineLocation = &activity.OfflineLocation{
			VenueName: *session.VenueName, Address: *session.Address,
			Longitude: session.Longitude, Latitude: session.Latitude,
		}
	} else if *session.DeliveryMode == activity.DeliveryModeOnline {
		if session.OnlineParticipationMode == nil ||
			session.OnlineParticipationCompliant == nil {
			return activity.SessionPublicationCandidate{}, xiangwanadmin.ErrPublicationInvalid
		}
		value.OnlineParticipation = &activity.OnlineParticipation{
			Mode:      *session.OnlineParticipationMode,
			Compliant: *session.OnlineParticipationCompliant,
		}
	}
	return value, nil
}

// scanAdminInstance scans the shared Instance fields followed by the list-only
// Series title and Session count columns.
func scanAdminInstance(
	row interface{ Scan(...any) error },
) (xiangwanadmin.InstanceListItem, error) {
	var item xiangwanadmin.InstanceListItem
	var activityType sql.NullString
	var quickTagCodes activitypostgres.StringArrayJSON
	var detailBlocks []byte
	var scheduledAt sql.NullTime
	var publishedAt sql.NullTime
	var completedAt sql.NullTime
	err := row.Scan(
		&item.Instance.ID, &item.Instance.TenantID, &item.Instance.SeriesID,
		&item.Instance.IssueNo,
		&item.Instance.Title, &item.Instance.Status, &activityType,
		&quickTagCodes, &item.Instance.CoverImageURL, &detailBlocks,
		&item.Instance.PublicationVersion, &item.Instance.PresentationRevision,
		&scheduledAt, &publishedAt, &completedAt, &item.Instance.Version,
		&item.Instance.CreatedAt, &item.Instance.UpdatedAt,
		&item.SeriesTitle, &item.SessionCount,
	)
	if err != nil {
		return xiangwanadmin.InstanceListItem{}, err
	}
	blocks, err := activity.ProjectDetailBlocks(detailBlocks)
	if err != nil {
		return xiangwanadmin.InstanceListItem{}, fmt.Errorf(
			"decode admin Instance detail blocks: %w", err,
		)
	}
	if len(blocks) > 0 {
		item.Instance.DetailBlocks = blocks
	}
	if activityType.Valid {
		value := activity.ActivityType(activityType.String)
		item.Instance.ActivityType = &value
	}
	item.Instance.QuickTagCodes = append([]string(nil), quickTagCodes...)
	if scheduledAt.Valid {
		item.Instance.ScheduledAt = &scheduledAt.Time
	}
	if publishedAt.Valid {
		item.Instance.PublishedAt = &publishedAt.Time
	}
	if completedAt.Valid {
		item.Instance.CompletedAt = &completedAt.Time
	}
	return item, nil
}
