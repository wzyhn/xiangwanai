package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

// UpdateSession edits the complete operational shape of a draft Session. The
// parent Instance and the Session are locked in one serializable transaction;
// both expected versions therefore fence stale administrator screens.
func (catalog *Catalog) UpdateSession(
	ctx context.Context,
	command xiangwanadmin.UpdateSessionCommand,
) (result activity.Session, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"session.update", "session", command.SessionID, command.RequestID, resultErr,
		)
	}()

	command.Title = strings.TrimSpace(command.Title)
	command.VenueName = strings.TrimSpace(command.VenueName)
	command.Address = strings.TrimSpace(command.Address)
	command.OnlineParticipationMode = strings.TrimSpace(command.OnlineParticipationMode)
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.SessionID == uuid.Nil ||
		command.ExpectedInstanceVersion < 1 || command.ExpectedSessionVersion < 1 ||
		utf8.RuneCountInString(command.Title) < 1 ||
		utf8.RuneCountInString(command.Title) > 200 || command.SortOrder < 0 ||
		!sessionIntegersFitPostgres(
			command.Capacity,
			command.GroupMinimum,
			command.LowStockThreshold,
			command.SortOrder,
		) || !validSessionLocationLengths(
		command.VenueName, command.OnlineParticipationMode,
	) {
		return activity.Session{}, xiangwanadmin.ErrInvalidCatalogRequest
	}

	digest, err := commandDigest(struct {
		InstanceID              uuid.UUID             `json:"instance_id"`
		SessionID               uuid.UUID             `json:"session_id"`
		ExpectedInstanceVersion int64                 `json:"expected_instance_version"`
		ExpectedSessionVersion  int64                 `json:"expected_session_version"`
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
		InstanceID: command.InstanceID, SessionID: command.SessionID,
		ExpectedInstanceVersion: command.ExpectedInstanceVersion,
		ExpectedSessionVersion:  command.ExpectedSessionVersion,
		Title:                   command.Title,
		RegistrationStartAt:     command.RegistrationStartAt,
		RegistrationEndAt:       command.RegistrationEndAt,
		SessionStartAt:          command.SessionStartAt,
		SessionEndAt:            command.SessionEndAt,
		Capacity:                command.Capacity,
		GroupMinimum:            command.GroupMinimum,
		LowStockThreshold:       command.LowStockThreshold,
		PriceCents:              command.PriceCents,
		DeliveryMode:            command.DeliveryMode,
		Area:                    command.Area,
		VenueName:               command.VenueName,
		Address:                 command.Address,
		Longitude:               command.Longitude,
		Latitude:                command.Latitude,
		OnlineMode:              command.OnlineParticipationMode,
		OnlineCompliant:         command.OnlineCompliant,
		SortOrder:               command.SortOrder,
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

	if receipt, replay, readErr := readOperation[operationResult[activity.Session]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"session.update", digest,
	); readErr != nil {
		return activity.Session{}, readErr
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.ID != command.SessionID ||
			receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.InstanceID != command.InstanceID || receipt.Value.Version < 1 {
			return activity.Session{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Session{}, fmt.Errorf("commit admin Session update replay: %w", err)
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
		return activity.Session{}, fmt.Errorf("lock admin Instance for Session update: %w", err)
	}
	if instanceStatus != activity.InstanceStatusDraft ||
		instanceVersion != command.ExpectedInstanceVersion || !activityType.Valid {
		return activity.Session{}, xiangwanadmin.ErrVersionConflict
	}

	repository := activitypostgres.NewRepository(tx)
	current, err := repository.LockSession(ctx, catalog.tenantID, command.SessionID)
	if errors.Is(err, activitypostgres.ErrSessionNotFound) {
		return activity.Session{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.Session{}, err
	}
	if current.InstanceID != command.InstanceID {
		return activity.Session{}, xiangwanadmin.ErrTargetNotFound
	}
	if current.Status != activity.SessionStatusDraft ||
		current.Version != command.ExpectedSessionVersion {
		return activity.Session{}, xiangwanadmin.ErrVersionConflict
	}

	next := current
	next.Title = command.Title
	next.RegistrationStartAt = updateSessionTimePointer(command.RegistrationStartAt)
	next.RegistrationEndAt = updateSessionTimePointer(command.RegistrationEndAt)
	next.SessionStartAt = updateSessionTimePointer(command.SessionStartAt)
	next.SessionEndAt = updateSessionTimePointer(command.SessionEndAt)
	next.Capacity = updateSessionIntPointer(command.Capacity)
	next.GroupMinimum = updateSessionIntPointer(command.GroupMinimum)
	next.LowStockThreshold = updateSessionIntPointer(command.LowStockThreshold)
	next.PriceCents = updateSessionInt64Pointer(command.PriceCents)
	next.DeliveryMode = updateSessionDeliveryModePointer(command.DeliveryMode)
	next.Area = updateSessionAreaPointer(command.Area)
	next.VenueName = nil
	next.Address = nil
	next.Longitude = nil
	next.Latitude = nil
	next.OnlineParticipationMode = nil
	next.OnlineParticipationCompliant = nil
	if command.DeliveryMode == activity.DeliveryModeOffline {
		next.VenueName = updateSessionStringPointer(command.VenueName)
		next.Address = updateSessionStringPointer(command.Address)
		next.Longitude = command.Longitude
		next.Latitude = command.Latitude
	} else if command.DeliveryMode == activity.DeliveryModeOnline {
		next.OnlineParticipationMode = updateSessionStringPointer(command.OnlineParticipationMode)
		next.OnlineParticipationCompliant = updateSessionBoolPointer(command.OnlineCompliant)
	}
	next.SortOrder = command.SortOrder

	readiness := activity.PublicationReferenceReadiness{
		QuestionnaireReady: true,
		PeopleReady:        true,
		ContentReady:       true,
		ResourcesReady:     true,
		QuickTagsReady:     true,
	}
	candidate, err := publicationCandidateFromSession(next, readiness)
	if err != nil {
		return activity.Session{}, fmt.Errorf("%w: %w", xiangwanadmin.ErrPublicationInvalid, err)
	}
	if err := activity.CheckInstancePublication(activity.InstancePublicationCandidate{
		SeriesID: seriesID, InstanceID: command.InstanceID,
		ActivityType:  activity.ActivityType(activityType.String),
		QuickTagCodes: []string(quickTags),
		Sessions:      []activity.SessionPublicationCandidate{candidate},
	}); err != nil {
		return activity.Session{}, fmt.Errorf("%w: %w", xiangwanadmin.ErrPublicationInvalid, err)
	}

	var venueName, address, onlineMode, onlineCompliant any
	var longitude, latitude any
	if command.DeliveryMode == activity.DeliveryModeOffline {
		venueName = command.VenueName
		address = command.Address
		longitude = nullableSessionFloat64(command.Longitude)
		latitude = nullableSessionFloat64(command.Latitude)
	} else if command.DeliveryMode == activity.DeliveryModeOnline {
		onlineMode = command.OnlineParticipationMode
		onlineCompliant = command.OnlineCompliant
	}
	updatedAt := catalog.now().UTC()
	updateResult, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_sessions
SET title = $4,
    registration_start_at = $5,
    registration_end_at = $6,
    session_start_at = $7,
    session_end_at = $8,
    capacity = $9,
    group_minimum = $10,
    low_stock_threshold = $11,
    price_cents = $12,
    delivery_mode = $13,
    area_code = $14,
    venue_name = $15,
    address = $16,
    longitude = $17,
    latitude = $18,
    online_participation_mode = $19,
    online_participation_compliant = $20,
    sort_order = $21,
    version = version + 1,
    updated_at = $22
WHERE tenant_id = $1
  AND instance_id = $2
  AND id = $3
  AND version = $23
`, catalog.tenantID, command.InstanceID, command.SessionID, command.Title,
		command.RegistrationStartAt, command.RegistrationEndAt,
		command.SessionStartAt, command.SessionEndAt, command.Capacity,
		command.GroupMinimum, command.LowStockThreshold, command.PriceCents,
		command.DeliveryMode, command.Area, venueName, address, longitude, latitude,
		onlineMode, onlineCompliant, command.SortOrder, updatedAt,
		command.ExpectedSessionVersion)
	if err != nil {
		return activity.Session{}, fmt.Errorf("update admin Session: %w", err)
	}
	if rows, rowsErr := updateResult.RowsAffected(); rowsErr != nil {
		return activity.Session{}, fmt.Errorf("read updated admin Session count: %w", rowsErr)
	} else if rows != 1 {
		return activity.Session{}, xiangwanadmin.ErrVersionConflict
	}

	advanceResult, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_instances
SET version = version + 1, updated_at = $3
WHERE tenant_id = $1 AND id = $2 AND version = $4
`, catalog.tenantID, command.InstanceID, updatedAt, command.ExpectedInstanceVersion)
	if err != nil {
		return activity.Session{}, fmt.Errorf("advance admin Instance after Session update: %w", err)
	}
	if rows, rowsErr := advanceResult.RowsAffected(); rowsErr != nil {
		return activity.Session{}, fmt.Errorf("read advanced admin Instance count: %w", rowsErr)
	} else if rows != 1 {
		return activity.Session{}, xiangwanadmin.ErrVersionConflict
	}

	result, err = repository.GetSession(ctx, catalog.tenantID, command.SessionID)
	if err != nil {
		return activity.Session{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "session.update", digest,
		operationResult[activity.Session]{Value: result}, result.ID, result.Version,
		command.RequestID, updatedAt,
	); err != nil {
		return activity.Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Session{}, fmt.Errorf("commit admin Session update: %w", err)
	}
	return result, nil
}

func updateSessionTimePointer(value time.Time) *time.Time { return &value }

func updateSessionIntPointer(value int) *int { return &value }

func updateSessionInt64Pointer(value int64) *int64 { return &value }

func updateSessionDeliveryModePointer(value activity.DeliveryMode) *activity.DeliveryMode {
	return &value
}

func updateSessionAreaPointer(value activity.AreaCode) *activity.AreaCode { return &value }

func updateSessionStringPointer(value string) *string { return &value }

func updateSessionBoolPointer(value bool) *bool { return &value }

func nullableSessionFloat64(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
