package activitypostgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var ErrPastActivityProjection = errors.New(
	"xiangwan past activity projection mismatch",
)

const pastActivityProjection = `
    activity_series.id,
    activity_series.title,
    activity_series.successful_published_instance_count,
    activity_series.historical_registration_count,
    activity_instance.id,
    activity_instance.title,
    activity_instance.status,
    activity_instance.activity_type,
    activity_instance.cover_image_url,
    activity_instance.publication_version,
    activity_instance.published_at,
    activity_instance.completed_at
`

const pastActivitiesQueryPrefix = `
SELECT
` + pastActivityProjection + `
FROM xiangwan_activity_instances AS activity_instance
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
WHERE activity_instance.tenant_id = $1
  AND activity_series.status IN ('active', 'archived')
  AND activity_instance.status IN ('completed', 'archived')
  AND activity_instance.activity_type IS NOT NULL
  AND activity_instance.publication_version >= 1
  AND activity_instance.published_at IS NOT NULL
  AND activity_instance.completed_at IS NOT NULL
  AND activity_instance.completed_at <= $3
  AND ($2 = 'all' OR activity_instance.activity_type = $2)
`

const pastActivitiesOrder = `
ORDER BY
    activity_instance.completed_at DESC,
    activity_instance.published_at DESC,
    activity_instance.id DESC
`

func (repository *Repository) ListPastActivities(
	ctx context.Context,
	filter activity.PastActivitiesFilter,
) (activity.PastActivitiesPage, error) {
	if repository == nil || repository.db == nil || ctx == nil {
		return activity.PastActivitiesPage{},
			activity.ErrInvalidPastActivitiesFilter
	}
	normalized, cursor, err := normalizePastActivitiesFilter(filter)
	if err != nil {
		return activity.PastActivitiesPage{}, err
	}

	var query strings.Builder
	query.WriteString(pastActivitiesQueryPrefix)
	args := []any{
		normalized.TenantID,
		normalized.ActivityType,
		normalized.At,
	}
	if cursor != nil {
		query.WriteString(`
  AND (
      activity_instance.completed_at,
      activity_instance.published_at,
      activity_instance.id
  ) < ($4, $5, $6)
`)
		args = append(
			args,
			cursor.CompletedAt,
			cursor.PublishedAt,
			cursor.InstanceID,
		)
	}
	query.WriteString(pastActivitiesOrder)
	query.WriteString(fmt.Sprintf("LIMIT $%d\n", len(args)+1))
	args = append(args, normalized.Limit+1)

	rows, err := repository.db.queryContext(ctx, query.String(), args...)
	if err != nil {
		return activity.PastActivitiesPage{}, fmt.Errorf(
			"list xiangwan past activities: %w",
			err,
		)
	}
	defer func() { _ = rows.Close() }()

	items := make([]activity.PastActivityItem, 0, normalized.Limit+1)
	for rows.Next() {
		item, scanErr := scanPastActivityItem(rows)
		if scanErr != nil {
			return activity.PastActivitiesPage{}, fmt.Errorf(
				"scan xiangwan past activity: %w",
				scanErr,
			)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return activity.PastActivitiesPage{}, fmt.Errorf(
			"iterate xiangwan past activities: %w",
			err,
		)
	}

	nextCursor := ""
	if len(items) > normalized.Limit {
		items = items[:normalized.Limit]
		nextCursor, err = encodePastActivitiesCursor(
			normalized,
			items[len(items)-1],
		)
		if err != nil {
			return activity.PastActivitiesPage{}, err
		}
	}
	return activity.PastActivitiesPage{
		Items:              items,
		ActiveActivityType: normalized.ActivityType,
		AsOf:               normalized.At,
		NextCursor:         nextCursor,
	}, nil
}

func (repository *Repository) ReadPastActivity(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	at time.Time,
) (activity.PastActivityItem, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		tenantID == uuid.Nil || instanceID == uuid.Nil || at.IsZero() {
		return activity.PastActivityItem{},
			activity.ErrInvalidPastActivitiesFilter
	}
	asOf := at.UTC()
	item, err := scanPastActivityItem(repository.db.queryRowContext(ctx, `
SELECT
`+pastActivityProjection+`
FROM xiangwan_activity_instances AS activity_instance
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
WHERE activity_instance.tenant_id = $1
  AND activity_instance.id = $2
  AND activity_series.status IN ('active', 'archived')
  AND activity_instance.status IN ('completed', 'archived')
  AND activity_instance.activity_type IS NOT NULL
  AND activity_instance.publication_version >= 1
  AND activity_instance.published_at IS NOT NULL
  AND activity_instance.completed_at IS NOT NULL
  AND activity_instance.completed_at <= $3
LIMIT 1
`, tenantID, instanceID, asOf))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.PastActivityItem{}, activity.ErrPastActivityNotFound
	}
	if err != nil {
		return activity.PastActivityItem{}, fmt.Errorf(
			"read xiangwan past activity: %w",
			err,
		)
	}
	return item, nil
}

// ReadPastActivityDetailBlocks returns the already-published Instance
// presentation blocks for one exact completed/archived Instance.  Review
// pages use a separate read from the card projection so the public list does
// not grow a second copy of the content payload.  The lifecycle/publication
// predicates intentionally mirror ReadPastActivity: a draft, unpublished,
// cancelled, or future-completed Instance can never leak its stored blocks.
func (repository *Repository) ReadPastActivityDetailBlocks(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	at time.Time,
) ([]activity.DetailBlock, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		tenantID == uuid.Nil || instanceID == uuid.Nil || at.IsZero() {
		return nil, activity.ErrInvalidPastActivitiesFilter
	}
	var raw []byte
	err := repository.db.queryRowContext(ctx, `
SELECT activity_instance.detail_blocks
FROM xiangwan_activity_instances AS activity_instance
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
WHERE activity_instance.tenant_id = $1
  AND activity_instance.id = $2
  AND activity_series.status IN ('active', 'archived')
  AND activity_instance.status IN ('completed', 'archived')
  AND activity_instance.publication_version >= 1
  AND activity_instance.published_at IS NOT NULL
  AND activity_instance.completed_at IS NOT NULL
  AND activity_instance.completed_at <= $3
LIMIT 1
`, tenantID, instanceID, at.UTC()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, activity.ErrPastActivityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read xiangwan past activity detail blocks: %w", err)
	}
	blocks, err := activity.ProjectDetailBlocks(raw)
	if err != nil {
		return nil, fmt.Errorf("project xiangwan past activity detail blocks: %w", err)
	}
	return blocks, nil
}

func scanPastActivityItem(row rowScanner) (activity.PastActivityItem, error) {
	var item activity.PastActivityItem
	if err := row.Scan(
		&item.SeriesID,
		&item.SeriesTitle,
		&item.SuccessfulPublishedInstanceCount,
		&item.HistoricalRegistrationCount,
		&item.InstanceID,
		&item.InstanceTitle,
		&item.InstanceStatus,
		&item.ActivityType,
		&item.CoverImageURL,
		&item.PublicationVersion,
		&item.PublishedAt,
		&item.CompletedAt,
	); err != nil {
		return activity.PastActivityItem{}, err
	}
	item.PublishedAt = item.PublishedAt.UTC()
	item.CompletedAt = item.CompletedAt.UTC()
	// Public read projections tolerate legacy surrounding whitespace while the
	// domain invariant remains strict for all newly written facts.
	item.SeriesTitle = strings.TrimSpace(item.SeriesTitle)
	item.InstanceTitle = strings.TrimSpace(item.InstanceTitle)
	if err := activity.ValidatePastActivityItem(item); err != nil {
		return activity.PastActivityItem{}, ErrPastActivityProjection
	}
	return item, nil
}

type decodedPastActivitiesCursor struct {
	Version      int                   `json:"v"`
	TenantID     uuid.UUID             `json:"tenant_id"`
	ActivityType activity.ActivityType `json:"activity_type"`
	AsOf         time.Time             `json:"as_of"`
	CompletedAt  time.Time             `json:"completed_at"`
	PublishedAt  time.Time             `json:"published_at"`
	InstanceID   uuid.UUID             `json:"instance_id"`
}

func normalizePastActivitiesFilter(
	filter activity.PastActivitiesFilter,
) (activity.PastActivitiesFilter, *decodedPastActivitiesCursor, error) {
	if filter.TenantID == uuid.Nil {
		return activity.PastActivitiesFilter{}, nil,
			activity.ErrInvalidPastActivitiesFilter
	}
	if filter.ActivityType == "" {
		filter.ActivityType = activity.ActivityTypeAll
	}
	if !activity.ValidPastActivityType(filter.ActivityType) {
		return activity.PastActivitiesFilter{}, nil,
			activity.ErrInvalidPastActivitiesFilter
	}
	switch {
	case filter.Limit == 0:
		filter.Limit = activity.DefaultPastActivitiesLimit
	case filter.Limit < 1 || filter.Limit > activity.MaxPastActivitiesLimit:
		return activity.PastActivitiesFilter{}, nil,
			activity.ErrInvalidPastActivitiesFilter
	}
	requestedAt := filter.At
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}
	filter.At = requestedAt.UTC()
	if filter.Cursor == "" {
		return filter, nil, nil
	}

	cursor, err := decodePastActivitiesCursor(filter.Cursor)
	if err != nil {
		return activity.PastActivitiesFilter{}, nil, err
	}
	if cursor.TenantID != filter.TenantID ||
		cursor.ActivityType != filter.ActivityType {
		return activity.PastActivitiesFilter{}, nil,
			activity.ErrStalePastActivitiesCursor
	}
	if filter.At.Sub(cursor.AsOf) > activity.MaxPastActivitiesCursorAge ||
		cursor.AsOf.After(filter.At.Add(activity.PastActivitiesFutureSkew)) {
		return activity.PastActivitiesFilter{}, nil,
			activity.ErrStalePastActivitiesCursor
	}
	filter.At = cursor.AsOf.UTC()
	return filter, &cursor, nil
}

func encodePastActivitiesCursor(
	filter activity.PastActivitiesFilter,
	item activity.PastActivityItem,
) (string, error) {
	encoded, err := json.Marshal(decodedPastActivitiesCursor{
		Version:      1,
		TenantID:     filter.TenantID,
		ActivityType: filter.ActivityType,
		AsOf:         filter.At.UTC(),
		CompletedAt:  item.CompletedAt.UTC(),
		PublishedAt:  item.PublishedAt.UTC(),
		InstanceID:   item.InstanceID,
	})
	if err != nil {
		return "", fmt.Errorf(
			"%w: encode",
			activity.ErrInvalidPastActivitiesCursor,
		)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePastActivitiesCursor(
	value string,
) (decodedPastActivitiesCursor, error) {
	if len(value) > 2048 {
		return decodedPastActivitiesCursor{}, fmt.Errorf(
			"%w: payload is too large",
			activity.ErrInvalidPastActivitiesCursor,
		)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return decodedPastActivitiesCursor{}, fmt.Errorf(
			"%w: malformed base64",
			activity.ErrInvalidPastActivitiesCursor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var cursor decodedPastActivitiesCursor
	if err := decoder.Decode(&cursor); err != nil {
		return decodedPastActivitiesCursor{}, fmt.Errorf(
			"%w: malformed payload",
			activity.ErrInvalidPastActivitiesCursor,
		)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return decodedPastActivitiesCursor{}, fmt.Errorf(
			"%w: trailing payload",
			activity.ErrInvalidPastActivitiesCursor,
		)
	}
	if cursor.Version != 1 || cursor.TenantID == uuid.Nil ||
		!activity.ValidPastActivityType(cursor.ActivityType) ||
		cursor.AsOf.IsZero() || cursor.CompletedAt.IsZero() ||
		cursor.PublishedAt.IsZero() ||
		cursor.PublishedAt.After(cursor.CompletedAt) ||
		cursor.CompletedAt.After(cursor.AsOf) || cursor.InstanceID == uuid.Nil {
		return decodedPastActivitiesCursor{}, fmt.Errorf(
			"%w: invalid fields",
			activity.ErrInvalidPastActivitiesCursor,
		)
	}
	cursor.AsOf = cursor.AsOf.UTC()
	cursor.CompletedAt = cursor.CompletedAt.UTC()
	cursor.PublishedAt = cursor.PublishedAt.UTC()
	return cursor, nil
}
