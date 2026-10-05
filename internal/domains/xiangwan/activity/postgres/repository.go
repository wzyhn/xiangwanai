// Package activitypostgres persists the Xiangwan Activity hierarchy in the
// customer PostgreSQL database. It uses database/sql contracts so both *sql.DB
// and *sql.Tx can provide the same tenant-scoped repository.
package activitypostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var (
	ErrSeriesNotFound           = errors.New("xiangwan activity series not found")
	ErrInstanceNotFound         = errors.New("xiangwan activity instance not found")
	ErrSessionNotFound          = errors.New("xiangwan activity session not found")
	ErrPublicationEventNotFound = errors.New("xiangwan publication event not found")
)

// DBTX is implemented by *sql.DB and *sql.Tx. Capacity-changing callers can
// start a transaction and construct a repository over that transaction.
type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Repository struct {
	db queryExecutor
}

func NewRepository(db DBTX) *Repository {
	return &Repository{db: sqlQueryExecutor{db: db}}
}

type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
	Close() error
}

type queryExecutor interface {
	queryContext(context.Context, string, ...any) (rowsScanner, error)
	queryRowContext(context.Context, string, ...any) rowScanner
}

type sqlQueryExecutor struct {
	db DBTX
}

func (executor sqlQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.db.QueryContext(ctx, query, args...)
}

func (executor sqlQueryExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.db.QueryRowContext(ctx, query, args...)
}

func (repository *Repository) CreateSeries(ctx context.Context, series activity.Series) (activity.Series, error) {
	if series.ID == uuid.Nil {
		series.ID = uuid.New()
	}
	if series.Status == "" {
		series.Status = activity.SeriesStatusDraft
	}
	if series.Version == 0 {
		series.Version = 1
	}

	created, err := scanSeries(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_activity_series (
    id, tenant_id, title, status, is_recurring,
    successful_published_instance_count, current_public_instance_id, version
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING
    id, tenant_id, title, status, is_recurring,
    home_visible, successful_published_instance_count,
    favorite_count, historical_registration_count,
    current_public_instance_id, version,
    created_at, updated_at
`,
		series.ID,
		series.TenantID,
		series.Title,
		series.Status,
		series.IsRecurring,
		series.SuccessfulPublishedInstanceCount,
		series.CurrentPublicInstanceID,
		series.Version,
	))
	if err != nil {
		return activity.Series{}, fmt.Errorf("create xiangwan activity series: %w", err)
	}
	return created, nil
}

func (repository *Repository) GetSeries(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (activity.Series, error) {
	series, err := scanSeries(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, title, status, is_recurring,
    home_visible, successful_published_instance_count,
    favorite_count, historical_registration_count,
    current_public_instance_id, version,
    created_at, updated_at
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
`, tenantID, seriesID))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Series{}, ErrSeriesNotFound
	}
	if err != nil {
		return activity.Series{}, fmt.Errorf("get xiangwan activity series: %w", err)
	}
	return series, nil
}

func (repository *Repository) ListSeries(
	ctx context.Context,
	tenantID uuid.UUID,
	offset int,
	limit int,
) ([]activity.Series, int64, error) {
	if tenantID == uuid.Nil || offset < 0 || limit < 1 {
		return nil, 0, ErrSeriesNotFound
	}
	var total int64
	if err := repository.db.queryRowContext(ctx, `
SELECT COUNT(*)
FROM xiangwan_activity_series
WHERE tenant_id = $1
`, tenantID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count xiangwan activity series: %w", err)
	}
	rows, err := repository.db.queryContext(ctx, `
SELECT
    id, tenant_id, title, status, is_recurring,
    home_visible, successful_published_instance_count,
    favorite_count, historical_registration_count,
    current_public_instance_id, version,
    created_at, updated_at
FROM xiangwan_activity_series
WHERE tenant_id = $1
ORDER BY updated_at DESC, id DESC
OFFSET $2 LIMIT $3
`, tenantID, offset, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list xiangwan activity series: %w", err)
	}
	defer rows.Close()
	series := make([]activity.Series, 0)
	for rows.Next() {
		value, scanErr := scanSeries(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("scan xiangwan activity series: %w", scanErr)
		}
		series = append(series, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate xiangwan activity series: %w", err)
	}
	return series, total, nil
}

func (repository *Repository) CreateInstance(
	ctx context.Context,
	instance activity.Instance,
) (activity.Instance, error) {
	if instance.ID == uuid.Nil {
		instance.ID = uuid.New()
	}
	if instance.Status == "" {
		instance.Status = activity.InstanceStatusDraft
	}
	if instance.Version == 0 {
		instance.Version = 1
	}
	// Catalog writes allocate the Series-scoped number before reaching the
	// repository. Keep a deterministic default for lower-level fixtures and
	// legacy callers; the database trigger/migration remains the final guard.
	if instance.IssueNo < 1 {
		instance.IssueNo = 1
	}
	if instance.QuickTagCodes == nil {
		instance.QuickTagCodes = []string{}
	}
	detailBlocks, err := encodeInstanceDetailBlocks(instance.DetailBlocks)
	if err != nil {
		return activity.Instance{}, fmt.Errorf("encode xiangwan Instance detail blocks: %w", err)
	}

	created, err := scanInstance(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_activity_instances (
    id, tenant_id, series_id, issue_no, title, status,
    activity_type, quick_tag_codes, cover_image_url, detail_blocks,
    publication_version,
    scheduled_at, published_at, completed_at, version
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::JSONB, $11, $12, $13, $14, $15)
RETURNING
    id, tenant_id, series_id, issue_no, title, status,
    activity_type, TO_JSON(quick_tag_codes), cover_image_url, detail_blocks,
    publication_version, presentation_revision,
    scheduled_at, published_at, completed_at, version, created_at, updated_at
`,
		instance.ID,
		instance.TenantID,
		instance.SeriesID,
		instance.IssueNo,
		instance.Title,
		instance.Status,
		instance.ActivityType,
		instance.QuickTagCodes,
		instance.CoverImageURL,
		detailBlocks,
		instance.PublicationVersion,
		instance.ScheduledAt,
		instance.PublishedAt,
		instance.CompletedAt,
		instance.Version,
	))
	if err != nil {
		return activity.Instance{}, fmt.Errorf("create xiangwan activity instance: %w", err)
	}
	return created, nil
}

func (repository *Repository) GetInstance(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) (activity.Instance, error) {
	instance, err := scanInstance(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, issue_no, title, status,
    activity_type, TO_JSON(quick_tag_codes), cover_image_url, detail_blocks,
    publication_version, presentation_revision,
    scheduled_at, published_at, completed_at, version, created_at, updated_at
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
`, tenantID, instanceID))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Instance{}, ErrInstanceNotFound
	}
	if err != nil {
		return activity.Instance{}, fmt.Errorf("get xiangwan activity instance: %w", err)
	}
	return instance, nil
}

func (repository *Repository) CreateSession(
	ctx context.Context,
	session activity.Session,
) (activity.Session, error) {
	if session.ID == uuid.Nil {
		session.ID = uuid.New()
	}
	if session.Status == "" {
		session.Status = activity.SessionStatusDraft
	}
	if session.Version == 0 {
		session.Version = 1
	}

	created, err := scanSession(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_activity_sessions (
    id, tenant_id, instance_id, title, status,
    registration_start_at, registration_end_at, session_start_at, session_end_at,
    capacity, group_minimum, low_stock_threshold, price_cents,
    delivery_mode, area_code, venue_name, address, longitude, latitude,
    online_participation_mode, online_participation_compliant,
    confirmed_registration_count, active_hold_count,
    sort_order, published_at, version
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16, $17, $18, $19,
    $20, $21,
    $22, $23,
    $24, $25, $26
)
RETURNING
    id, tenant_id, instance_id, title, status,
    registration_start_at, registration_end_at, session_start_at, session_end_at,
    capacity, group_minimum, low_stock_threshold, price_cents,
    delivery_mode, area_code, venue_name, address, longitude, latitude,
    online_participation_mode, online_participation_compliant,
    confirmed_registration_count, active_hold_count,
    sort_order, published_at, version, created_at, updated_at
`,
		session.ID,
		session.TenantID,
		session.InstanceID,
		session.Title,
		session.Status,
		session.RegistrationStartAt,
		session.RegistrationEndAt,
		session.SessionStartAt,
		session.SessionEndAt,
		session.Capacity,
		session.GroupMinimum,
		session.LowStockThreshold,
		session.PriceCents,
		session.DeliveryMode,
		session.Area,
		session.VenueName,
		session.Address,
		session.Longitude,
		session.Latitude,
		session.OnlineParticipationMode,
		session.OnlineParticipationCompliant,
		session.ConfirmedRegistrationCount,
		session.ActiveHoldCount,
		session.SortOrder,
		session.PublishedAt,
		session.Version,
	))
	if err != nil {
		return activity.Session{}, fmt.Errorf("create xiangwan activity session: %w", err)
	}
	return created, nil
}

func (repository *Repository) GetSession(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
) (activity.Session, error) {
	return repository.getSession(ctx, tenantID, sessionID, false)
}

// LockSession obtains the row lock used by registration, capacity, payment,
// cancellation, and terminal aggregation transactions.
func (repository *Repository) LockSession(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
) (activity.Session, error) {
	return repository.getSession(ctx, tenantID, sessionID, true)
}

func (repository *Repository) getSession(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	forUpdate bool,
) (activity.Session, error) {
	query := `
SELECT
    id, tenant_id, instance_id, title, status,
    registration_start_at, registration_end_at, session_start_at, session_end_at,
    capacity, group_minimum, low_stock_threshold, price_cents,
    delivery_mode, area_code, venue_name, address, longitude, latitude,
    online_participation_mode, online_participation_compliant,
    confirmed_registration_count, active_hold_count,
    sort_order, published_at, version, created_at, updated_at
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND id = $2
`
	if forUpdate {
		query += "FOR UPDATE\n"
	}

	session, err := scanSession(repository.db.queryRowContext(ctx, query, tenantID, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Session{}, ErrSessionNotFound
	}
	if err != nil {
		return activity.Session{}, fmt.Errorf("get xiangwan activity session: %w", err)
	}
	return session, nil
}

func (repository *Repository) ListInstanceSessions(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) ([]activity.Session, error) {
	rows, err := repository.db.queryContext(ctx, `
SELECT
    id, tenant_id, instance_id, title, status,
    registration_start_at, registration_end_at, session_start_at, session_end_at,
    capacity, group_minimum, low_stock_threshold, price_cents,
    delivery_mode, area_code, venue_name, address, longitude, latitude,
    online_participation_mode, online_participation_compliant,
    confirmed_registration_count, active_hold_count,
    sort_order, published_at, version, created_at, updated_at
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2
ORDER BY sort_order ASC, session_start_at ASC NULLS LAST, id ASC
`, tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan activity sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]activity.Session, 0)
	for rows.Next() {
		session, scanErr := scanSession(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan xiangwan activity session: %w", scanErr)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan activity sessions: %w", err)
	}
	return sessions, nil
}

// CreatePublicationEvent persists the immutable receipt after an Instance and
// all of its Sessions have been atomically published.
func (repository *Repository) CreatePublicationEvent(
	ctx context.Context,
	event activity.PublicationEvent,
) (activity.PublicationEvent, error) {
	if event.ID == uuid.Nil {
		event.ID = uuid.New()
	}

	created, err := scanPublicationEvent(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_publication_events (
    id, tenant_id, series_id, instance_id, publication_version,
    session_count, candidate_digest, published_by, published_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING
    id, tenant_id, series_id, instance_id, publication_version,
    session_count, candidate_digest, published_by, published_at, created_at
`,
		event.ID,
		event.TenantID,
		event.SeriesID,
		event.InstanceID,
		event.PublicationVersion,
		event.SessionCount,
		event.CandidateDigest,
		event.PublishedBy,
		event.PublishedAt,
	))
	if err != nil {
		return activity.PublicationEvent{}, fmt.Errorf("create xiangwan publication event: %w", err)
	}
	return created, nil
}

func (repository *Repository) GetPublicationEvent(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	publicationVersion int64,
) (activity.PublicationEvent, error) {
	event, err := scanPublicationEvent(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, publication_version,
    session_count, candidate_digest, published_by, published_at, created_at
FROM xiangwan_publication_events
WHERE tenant_id = $1 AND instance_id = $2 AND publication_version = $3
`, tenantID, instanceID, publicationVersion))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.PublicationEvent{}, ErrPublicationEventNotFound
	}
	if err != nil {
		return activity.PublicationEvent{}, fmt.Errorf("get xiangwan publication event: %w", err)
	}
	return event, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanSeries(row rowScanner) (activity.Series, error) {
	var series activity.Series
	var currentPublicInstanceID uuid.NullUUID
	err := row.Scan(
		&series.ID,
		&series.TenantID,
		&series.Title,
		&series.Status,
		&series.IsRecurring,
		&series.HomeVisible,
		&series.SuccessfulPublishedInstanceCount,
		&series.FavoriteCount,
		&series.HistoricalRegistrationCount,
		&currentPublicInstanceID,
		&series.Version,
		&series.CreatedAt,
		&series.UpdatedAt,
	)
	if err != nil {
		return activity.Series{}, err
	}
	if currentPublicInstanceID.Valid {
		series.CurrentPublicInstanceID = &currentPublicInstanceID.UUID
	}
	return series, nil
}

func scanInstance(row rowScanner) (activity.Instance, error) {
	var instance activity.Instance
	var activityType sql.NullString
	var quickTagCodes StringArrayJSON
	var detailBlocks []byte
	var scheduledAt sql.NullTime
	var publishedAt sql.NullTime
	var completedAt sql.NullTime
	err := row.Scan(
		&instance.ID,
		&instance.TenantID,
		&instance.SeriesID,
		&instance.IssueNo,
		&instance.Title,
		&instance.Status,
		&activityType,
		&quickTagCodes,
		&instance.CoverImageURL,
		&detailBlocks,
		&instance.PublicationVersion,
		&instance.PresentationRevision,
		&scheduledAt,
		&publishedAt,
		&completedAt,
		&instance.Version,
		&instance.CreatedAt,
		&instance.UpdatedAt,
	)
	if err != nil {
		return activity.Instance{}, err
	}
	blocks, err := activity.ProjectDetailBlocks(detailBlocks)
	if err != nil {
		return activity.Instance{}, fmt.Errorf("decode xiangwan Instance detail blocks: %w", err)
	}
	if len(blocks) > 0 {
		instance.DetailBlocks = blocks
	}
	instance.ScheduledAt = nullTimePointer(scheduledAt)
	instance.QuickTagCodes = append([]string(nil), quickTagCodes...)
	instance.ActivityType = nullActivityTypePointer(activityType)
	instance.PublishedAt = nullTimePointer(publishedAt)
	instance.CompletedAt = nullTimePointer(completedAt)
	return instance, nil
}

// encodeInstanceDetailBlocks keeps an empty list a JSON array: json.Marshal
// renders a nil slice as null, which the 786 CHECK rejects.
func encodeInstanceDetailBlocks(blocks []activity.DetailBlock) (string, error) {
	if blocks == nil {
		return "[]", nil
	}
	encoded, err := json.Marshal(blocks)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func scanSession(row rowScanner) (activity.Session, error) {
	var session activity.Session
	var registrationStartAt sql.NullTime
	var registrationEndAt sql.NullTime
	var sessionStartAt sql.NullTime
	var sessionEndAt sql.NullTime
	var capacity sql.NullInt64
	var groupMinimum sql.NullInt64
	var lowStockThreshold sql.NullInt64
	var priceCents sql.NullInt64
	var deliveryMode sql.NullString
	var area sql.NullString
	var venueName sql.NullString
	var address sql.NullString
	var longitude sql.NullFloat64
	var latitude sql.NullFloat64
	var onlineParticipationMode sql.NullString
	var onlineParticipationCompliant sql.NullBool
	var publishedAt sql.NullTime
	err := row.Scan(
		&session.ID,
		&session.TenantID,
		&session.InstanceID,
		&session.Title,
		&session.Status,
		&registrationStartAt,
		&registrationEndAt,
		&sessionStartAt,
		&sessionEndAt,
		&capacity,
		&groupMinimum,
		&lowStockThreshold,
		&priceCents,
		&deliveryMode,
		&area,
		&venueName,
		&address,
		&longitude,
		&latitude,
		&onlineParticipationMode,
		&onlineParticipationCompliant,
		&session.ConfirmedRegistrationCount,
		&session.ActiveHoldCount,
		&session.SortOrder,
		&publishedAt,
		&session.Version,
		&session.CreatedAt,
		&session.UpdatedAt,
	)
	if err != nil {
		return activity.Session{}, err
	}
	session.RegistrationStartAt = nullTimePointer(registrationStartAt)
	session.RegistrationEndAt = nullTimePointer(registrationEndAt)
	session.SessionStartAt = nullTimePointer(sessionStartAt)
	session.SessionEndAt = nullTimePointer(sessionEndAt)
	session.Capacity = nullIntPointer(capacity)
	session.GroupMinimum = nullIntPointer(groupMinimum)
	session.LowStockThreshold = nullIntPointer(lowStockThreshold)
	session.PriceCents = nullInt64Pointer(priceCents)
	session.DeliveryMode = nullDeliveryModePointer(deliveryMode)
	session.Area = nullAreaCodePointer(area)
	session.VenueName = nullStringPointer(venueName)
	session.Address = nullStringPointer(address)
	session.Longitude = nullFloat64Pointer(longitude)
	session.Latitude = nullFloat64Pointer(latitude)
	session.OnlineParticipationMode = nullStringPointer(onlineParticipationMode)
	session.OnlineParticipationCompliant = nullBoolPointer(onlineParticipationCompliant)
	session.PublishedAt = nullTimePointer(publishedAt)
	return session, nil
}

func scanPublicationEvent(row rowScanner) (activity.PublicationEvent, error) {
	var event activity.PublicationEvent
	err := row.Scan(
		&event.ID,
		&event.TenantID,
		&event.SeriesID,
		&event.InstanceID,
		&event.PublicationVersion,
		&event.SessionCount,
		&event.CandidateDigest,
		&event.PublishedBy,
		&event.PublishedAt,
		&event.CreatedAt,
	)
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	return event, nil
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func nullIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

func nullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullDeliveryModePointer(value sql.NullString) *activity.DeliveryMode {
	if !value.Valid {
		return nil
	}
	converted := activity.DeliveryMode(value.String)
	return &converted
}

func nullActivityTypePointer(value sql.NullString) *activity.ActivityType {
	if !value.Valid {
		return nil
	}
	converted := activity.ActivityType(value.String)
	return &converted
}

func nullAreaCodePointer(value sql.NullString) *activity.AreaCode {
	if !value.Valid {
		return nil
	}
	converted := activity.AreaCode(value.String)
	return &converted
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullFloat64Pointer(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func nullBoolPointer(value sql.NullBool) *bool {
	if !value.Valid {
		return nil
	}
	return &value.Bool
}
