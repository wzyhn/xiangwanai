package peoplepostgres

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicProfilesFilter = errors.New(
		"invalid xiangwan public PeopleProfile filter",
	)
	ErrInvalidPublicProfilesCursor = errors.New(
		"invalid xiangwan public PeopleProfile cursor",
	)
	ErrStalePublicProfilesCursor = errors.New(
		"stale xiangwan public PeopleProfile cursor",
	)
	ErrPublicProfilesProjection = errors.New(
		"xiangwan public PeopleProfile projection mismatch",
	)
)

// ListPublishedProfiles reads one stable page from the moderated public set.
// It never joins or infers a trusted Principal binding.
func (repository *Repository) ListPublishedProfiles(
	ctx context.Context,
	filter people.PublicProfileFilter,
) (people.PublicProfilesPage, error) {
	if repository == nil || repository.db == nil {
		return people.PublicProfilesPage{}, ErrInvalidPublicProfilesFilter
	}
	normalized, cursor, err := normalizePublicProfilesFilter(filter)
	if err != nil {
		return people.PublicProfilesPage{}, err
	}

	var query strings.Builder
	query.WriteString(`
SELECT` + profileProjection + `
FROM xiangwan_people_profiles
WHERE tenant_id = $1
  AND profile_status = 'published'
  AND moderation_status = 'approved'
  AND updated_at <= $2
`)
	args := []any{normalized.TenantID, normalized.At}
	if cursor != nil {
		query.WriteString(`  AND (updated_at, id) < ($3, $4)
`)
		args = append(args, cursor.UpdatedAt, cursor.PeopleID)
	}
	query.WriteString(`ORDER BY updated_at DESC, id DESC
`)
	query.WriteString(fmt.Sprintf("LIMIT $%d\n", len(args)+1))
	args = append(args, normalized.Limit+1)

	rows, err := repository.db.queryContext(ctx, query.String(), args...)
	if err != nil {
		return people.PublicProfilesPage{}, fmt.Errorf(
			"list xiangwan public PeopleProfiles: %w",
			err,
		)
	}
	defer func() { _ = rows.Close() }()

	items := make([]people.Profile, 0, normalized.Limit+1)
	for rows.Next() {
		value, scanErr := scanProfile(rows)
		if scanErr != nil {
			return people.PublicProfilesPage{}, fmt.Errorf(
				"scan xiangwan public PeopleProfile: %w",
				scanErr,
			)
		}
		if value.TenantID != normalized.TenantID ||
			value.ProfileStatus != people.ProfileStatusPublished ||
			value.ModerationStatus != people.ModerationStatusApproved ||
			value.UpdatedAt.After(normalized.At) {
			return people.PublicProfilesPage{}, ErrPublicProfilesProjection
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return people.PublicProfilesPage{}, fmt.Errorf(
			"iterate xiangwan public PeopleProfiles: %w",
			err,
		)
	}

	nextCursor := ""
	if len(items) > normalized.Limit {
		items = items[:normalized.Limit]
		nextCursor, err = encodePublicProfilesCursor(
			normalized,
			items[len(items)-1],
		)
		if err != nil {
			return people.PublicProfilesPage{}, err
		}
	}
	return people.PublicProfilesPage{
		Items:      items,
		AsOf:       normalized.At,
		NextCursor: nextCursor,
	}, nil
}

type decodedPublicProfilesCursor struct {
	Version   int       `json:"v"`
	TenantID  uuid.UUID `json:"tenant_id"`
	AsOf      time.Time `json:"as_of"`
	UpdatedAt time.Time `json:"updated_at"`
	PeopleID  uuid.UUID `json:"people_id"`
}

func normalizePublicProfilesFilter(
	filter people.PublicProfileFilter,
) (people.PublicProfileFilter, *decodedPublicProfilesCursor, error) {
	if filter.TenantID == uuid.Nil {
		return people.PublicProfileFilter{}, nil, ErrInvalidPublicProfilesFilter
	}
	switch {
	case filter.Limit == 0:
		filter.Limit = people.DefaultPublicProfilesLimit
	case filter.Limit < 1 || filter.Limit > people.MaxPublicProfilesLimit:
		return people.PublicProfileFilter{}, nil, ErrInvalidPublicProfilesFilter
	}
	requestedAt := filter.At
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}
	filter.At = requestedAt.UTC()
	if filter.Cursor == "" {
		return filter, nil, nil
	}
	cursor, err := decodePublicProfilesCursor(filter.Cursor)
	if err != nil {
		return people.PublicProfileFilter{}, nil, err
	}
	if cursor.TenantID != filter.TenantID ||
		filter.At.Sub(cursor.AsOf) > people.MaxPublicProfilesCursorAge ||
		cursor.AsOf.After(filter.At.Add(people.PublicProfilesFutureSkew)) {
		return people.PublicProfileFilter{}, nil, ErrStalePublicProfilesCursor
	}
	filter.At = cursor.AsOf.UTC()
	return filter, &cursor, nil
}

func encodePublicProfilesCursor(
	filter people.PublicProfileFilter,
	last people.Profile,
) (string, error) {
	encoded, err := json.Marshal(decodedPublicProfilesCursor{
		Version:   1,
		TenantID:  filter.TenantID,
		AsOf:      filter.At.UTC(),
		UpdatedAt: last.UpdatedAt.UTC(),
		PeopleID:  last.ID,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode", ErrInvalidPublicProfilesCursor)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePublicProfilesCursor(
	value string,
) (decodedPublicProfilesCursor, error) {
	if len(value) > 2048 {
		return decodedPublicProfilesCursor{}, fmt.Errorf(
			"%w: payload is too large",
			ErrInvalidPublicProfilesCursor,
		)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return decodedPublicProfilesCursor{}, fmt.Errorf(
			"%w: malformed base64",
			ErrInvalidPublicProfilesCursor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var cursor decodedPublicProfilesCursor
	if err := decoder.Decode(&cursor); err != nil {
		return decodedPublicProfilesCursor{}, fmt.Errorf(
			"%w: malformed payload",
			ErrInvalidPublicProfilesCursor,
		)
	}
	if err := ensurePublicProfilesCursorEOF(decoder); err != nil {
		return decodedPublicProfilesCursor{}, err
	}
	if cursor.Version != 1 || cursor.TenantID == uuid.Nil ||
		cursor.AsOf.IsZero() || cursor.UpdatedAt.IsZero() ||
		cursor.UpdatedAt.After(cursor.AsOf) || cursor.PeopleID == uuid.Nil {
		return decodedPublicProfilesCursor{}, fmt.Errorf(
			"%w: invalid fields",
			ErrInvalidPublicProfilesCursor,
		)
	}
	cursor.AsOf = cursor.AsOf.UTC()
	cursor.UpdatedAt = cursor.UpdatedAt.UTC()
	return cursor, nil
}

func ensurePublicProfilesCursorEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf(
			"%w: trailing payload",
			ErrInvalidPublicProfilesCursor,
		)
	}
	return nil
}
