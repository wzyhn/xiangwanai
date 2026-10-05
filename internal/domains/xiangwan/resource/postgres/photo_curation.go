package resourcepostgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// PhotoCurationService appends presentation revisions over immutable, approved
// review Content. It shares the runtime's transaction-bound activity Grant
// authorizer with the original review writer.
type PhotoCurationService struct {
	db        *sql.DB
	tenantID  uuid.UUID
	authorize ReviewResourceAuthorizer
	now       func() time.Time
}

func NewPhotoCurationService(
	db *sql.DB, tenantID uuid.UUID, authorize ReviewResourceAuthorizer,
) (*PhotoCurationService, error) {
	if db == nil || tenantID == uuid.Nil || authorize == nil {
		return nil, resource.ErrInvalidPhotoCuration
	}
	return &PhotoCurationService{db: db, tenantID: tenantID, authorize: authorize, now: time.Now}, nil
}

func (service *PhotoCurationService) ListPhotoCurations(
	ctx context.Context, command resource.PhotoCurationListCommand,
) ([]resource.PhotoCurationView, error) {
	if service == nil || service.db == nil || service.authorize == nil || ctx == nil ||
		command.ActorID == uuid.Nil || command.IdentityLinkID == uuid.Nil || command.InstanceID == uuid.Nil {
		return nil, resource.ErrInvalidPhotoCuration
	}
	tx, err := service.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := service.authorize(ctx, tx, command.ActorID, command.IdentityLinkID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT relation.id
FROM xiangwan_resource_relations AS relation
JOIN xiangwan_resource_publications AS publication
  ON publication.tenant_id = relation.tenant_id
 AND publication.relation_id = relation.id
 AND NOT EXISTS (SELECT 1 FROM xiangwan_review_resource_replacements AS replacement
                 WHERE replacement.tenant_id = relation.tenant_id
                   AND replacement.previous_relation_id = relation.id)
WHERE relation.tenant_id = $1 AND relation.instance_id = $2
  AND ((relation.relation_kind = 'instance_review' AND relation.session_id IS NULL)
       OR (relation.relation_kind = 'session_resources' AND relation.session_id IS NOT NULL))
ORDER BY CASE relation.relation_kind WHEN 'instance_review' THEN 0 ELSE 1 END,
         relation.sort_order ASC, relation.id ASC LIMIT 51
`, service.tenantID, command.InstanceID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if len(ids) > 50 {
		return nil, resource.ErrPhotoCurationConflict
	}
	views := make([]resource.PhotoCurationView, 0, len(ids))
	for _, id := range ids {
		view, err := service.readCurrent(ctx, tx, id)
		if errors.Is(err, resource.ErrPhotoCurationNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	requestID := command.RequestID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'review.photo_curation_list', 'instance', $4, $5,
    jsonb_build_object('identity_link_id', $6::TEXT, 'returned', $7::INTEGER), $8, $8)
`, uuid.New(), service.tenantID, command.ActorID, command.InstanceID,
		requestID, command.IdentityLinkID.String(), len(views), now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return views, nil
}

func (service *PhotoCurationService) ReadPhotoCuration(
	ctx context.Context, command resource.PhotoCurationReadCommand,
) (resource.PhotoCurationView, error) {
	if service == nil || service.db == nil || service.authorize == nil || ctx == nil ||
		command.ActorID == uuid.Nil || command.IdentityLinkID == uuid.Nil || command.RelationID == uuid.Nil {
		return resource.PhotoCurationView{}, resource.ErrInvalidPhotoCuration
	}
	tx, err := service.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return resource.PhotoCurationView{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := service.authorize(ctx, tx, command.ActorID, command.IdentityLinkID); err != nil {
		return resource.PhotoCurationView{}, err
	}
	view, err := service.readCurrent(ctx, tx, command.RelationID)
	if err != nil {
		return resource.PhotoCurationView{}, err
	}
	if err := service.audit(ctx, tx, command, "review.photo_curation_read", view.Version, len(view.OriginalPhotos)); err != nil {
		return resource.PhotoCurationView{}, err
	}
	if err := tx.Commit(); err != nil {
		return resource.PhotoCurationView{}, err
	}
	return view, nil
}

func (service *PhotoCurationService) WritePhotoCuration(
	ctx context.Context, command resource.PhotoCurationWriteCommand,
) (resource.PhotoCurationReceipt, error) {
	if service == nil || service.db == nil || service.authorize == nil || ctx == nil ||
		command.ActorID == uuid.Nil || command.IdentityLinkID == uuid.Nil ||
		command.RelationID == uuid.Nil || command.OperationID == uuid.Nil ||
		command.ExpectedVersion < 0 || len(command.OrderedBlockIDs) > 30 ||
		(!command.CoverSpecified && command.CoverBlockID != nil) {
		return resource.PhotoCurationReceipt{}, resource.ErrInvalidPhotoCuration
	}
	ordered, err := encodePhotoIDs(command.OrderedBlockIDs)
	if err != nil {
		return resource.PhotoCurationReceipt{}, err
	}
	tx, err := service.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return resource.PhotoCurationReceipt{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := service.authorize(ctx, tx, command.ActorID, command.IdentityLinkID); err != nil {
		return resource.PhotoCurationReceipt{}, err
	}
	// Read the exact operation before current target facts. A successful replay
	// remains available when a later lifecycle transition changes eligibility.
	var replayRelation uuid.UUID
	var replayExpected, replayVersion int64
	var replayIDs []byte
	var replayCover uuid.NullUUID
	var replayCoverSpecified bool
	err = tx.QueryRowContext(ctx, `
SELECT relation_id, expected_version, version, ordered_block_ids, cover_block_id, cover_specified
FROM xiangwan_review_photo_curations
WHERE tenant_id = $1 AND actor_id = $2 AND operation_id = $3
`, service.tenantID, command.ActorID, command.OperationID).Scan(
		&replayRelation, &replayExpected, &replayVersion, &replayIDs, &replayCover, &replayCoverSpecified,
	)
	if err == nil {
		savedIDs, decodeErr := decodePhotoIDs(replayIDs)
		if replayRelation != command.RelationID || replayExpected != command.ExpectedVersion ||
			decodeErr != nil || !equalPhotoIDs(savedIDs, command.OrderedBlockIDs) ||
			replayCoverSpecified != command.CoverSpecified ||
			(command.CoverSpecified && !samePhotoCover(nullablePhotoCover(replayCover), command.CoverBlockID)) {
			return resource.PhotoCurationReceipt{}, resource.ErrPhotoCurationConflict
		}
		if err := tx.Commit(); err != nil {
			return resource.PhotoCurationReceipt{}, classifyPhotoCurationError(err)
		}
		return resource.PhotoCurationReceipt{RelationID: replayRelation, Version: replayVersion,
			OrderedBlockIDs: append([]uuid.UUID(nil), command.OrderedBlockIDs...), CoverBlockID: nullablePhotoCover(replayCover)}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return resource.PhotoCurationReceipt{}, err
	}

	var lockedRelationID uuid.UUID
	err = tx.QueryRowContext(ctx, `
SELECT id FROM xiangwan_resource_relations
WHERE tenant_id = $1 AND id = $2 FOR UPDATE
`, service.tenantID, command.RelationID).Scan(&lockedRelationID)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.PhotoCurationReceipt{}, resource.ErrPhotoCurationNotFound
	}
	if err != nil {
		return resource.PhotoCurationReceipt{}, err
	}
	view, err := service.readCurrent(ctx, tx, command.RelationID)
	if err != nil {
		return resource.PhotoCurationReceipt{}, err
	}
	cover := command.CoverBlockID
	if !command.CoverSpecified {
		cover = view.CoverBlockID
		if cover != nil && !containsPhotoID(command.OrderedBlockIDs, *cover) {
			cover = nil
		}
	}
	if cover != nil && (*cover == uuid.Nil || !containsPhotoID(command.OrderedBlockIDs, *cover)) {
		return resource.PhotoCurationReceipt{}, resource.ErrInvalidPhotoCuration
	}
	if command.ExpectedVersion != view.Version ||
		(equalPhotoIDs(command.OrderedBlockIDs, view.OrderedBlockIDs) && samePhotoCover(cover, view.CoverBlockID)) {
		return resource.PhotoCurationReceipt{}, resource.ErrPhotoCurationConflict
	}
	known := make(map[uuid.UUID]bool, len(view.OriginalPhotos))
	for _, photo := range view.OriginalPhotos {
		known[photo.BlockID] = true
	}
	for _, id := range command.OrderedBlockIDs {
		if !known[id] {
			return resource.PhotoCurationReceipt{}, resource.ErrInvalidPhotoCuration
		}
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_review_photo_curations (
    id, tenant_id, relation_id, version, expected_version, operation_id,
    actor_id, ordered_block_ids, created_at, cover_block_id, cover_specified
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9, $10, $11)
`, uuid.New(), service.tenantID, command.RelationID, view.Version+1,
		command.ExpectedVersion, command.OperationID, command.ActorID, string(ordered), now, cover, command.CoverSpecified)
	if err != nil {
		return resource.PhotoCurationReceipt{}, classifyPhotoCurationError(err)
	}
	if err := service.audit(ctx, tx, command.PhotoCurationReadCommand,
		"review.photo_curation_write", view.Version+1, len(command.OrderedBlockIDs)); err != nil {
		return resource.PhotoCurationReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return resource.PhotoCurationReceipt{}, classifyPhotoCurationError(err)
	}
	return resource.PhotoCurationReceipt{RelationID: command.RelationID,
		Version: view.Version + 1, OrderedBlockIDs: append([]uuid.UUID(nil), command.OrderedBlockIDs...), CoverBlockID: cover}, nil
}

const photoCurationRelationSelect = `
SELECT relation.content_id, relation.instance_id, relation.session_id,
       relation.expected_target_version, content.title
FROM xiangwan_resource_relations AS relation
JOIN xiangwan_resource_publications AS publication
  ON publication.tenant_id = relation.tenant_id
 AND publication.relation_id = relation.id
 AND NOT EXISTS (SELECT 1 FROM xiangwan_review_resource_replacements AS replacement
                 WHERE replacement.tenant_id = relation.tenant_id
                   AND replacement.previous_relation_id = relation.id)
 AND publication.content_id = relation.content_id
 AND publication.content_revision_at = relation.content_revision_at
 AND publication.access_policy = relation.access_policy
 AND publication.expected_target_version = relation.expected_target_version
JOIN xiangwan_resource_content_snapshots AS snapshot
  ON snapshot.tenant_id = relation.tenant_id
 AND snapshot.relation_id = relation.id
 AND snapshot.content_id = relation.content_id
 AND snapshot.content_revision_at = relation.content_revision_at
JOIN xiangwan_resource_moderation_observations AS approval
  ON approval.tenant_id = publication.tenant_id
 AND approval.id = publication.approval_observation_id
 AND approval.relation_id = relation.id
 AND approval.content_id = relation.content_id
 AND approval.content_revision_at = relation.content_revision_at
 AND approval.subject_digest = snapshot.subject_digest
 AND approval.decision = 'approved'
JOIN contents AS content
  ON content.id = relation.content_id
 AND content.tenant_id = relation.tenant_id
 AND content.principal_id = relation.created_by
 AND content.type = 'review'
 AND content.status IN ('active', 'reviewing')
 AND content.visibility = 'private'
 AND content.updated_at = relation.content_revision_at
 AND content.deleted_at IS NULL
WHERE relation.tenant_id = $1 AND relation.id = $2
  AND relation.access_policy = 'public' AND publication.access_policy = 'public'
  AND ((relation.relation_kind = 'instance_review' AND relation.session_id IS NULL)
       OR (relation.relation_kind = 'session_resources' AND relation.session_id IS NOT NULL))
  AND NOT EXISTS (
      SELECT 1 FROM content_governance AS governance
      WHERE governance.content_id = relation.content_id
  )
FOR SHARE OF relation, publication, content
`

func (service *PhotoCurationService) readCurrent(
	ctx context.Context, tx *sql.Tx, relationID uuid.UUID,
) (resource.PhotoCurationView, error) {
	view := resource.PhotoCurationView{RelationID: relationID, OriginalPhotos: []resource.ReviewPhoto{}}
	var contentID, instanceID uuid.UUID
	var sessionID uuid.NullUUID
	var targetVersion int64
	err := tx.QueryRowContext(ctx, photoCurationRelationSelect,
		service.tenantID, relationID).Scan(&contentID, &instanceID, &sessionID, &targetVersion, &view.Title)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.PhotoCurationView{}, resource.ErrPhotoCurationNotFound
	}
	if err != nil {
		return resource.PhotoCurationView{}, err
	}
	view.InstanceID = instanceID
	if sessionID.Valid {
		view.SessionID = &sessionID.UUID
	}
	var currentVersion int64
	if sessionID.Valid {
		var parentStatus string
		err = tx.QueryRowContext(ctx, `
SELECT status FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2 AND status IN ('published', 'completed', 'archived') FOR SHARE
`, service.tenantID, instanceID).Scan(&parentStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return resource.PhotoCurationView{}, resource.ErrPhotoCurationNotFound
		}
		if err != nil {
			return resource.PhotoCurationView{}, err
		}
		err = tx.QueryRowContext(ctx, `
SELECT version FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3
  AND status IN ('published', 'ended', 'archived') FOR SHARE
`, service.tenantID, instanceID, sessionID.UUID).Scan(&currentVersion)
	} else {
		err = tx.QueryRowContext(ctx, `
SELECT version FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2 AND status IN ('completed', 'archived') FOR SHARE
`, service.tenantID, instanceID).Scan(&currentVersion)
	}
	if errors.Is(err, sql.ErrNoRows) || (err == nil && currentVersion != targetVersion) {
		return resource.PhotoCurationView{}, resource.ErrPhotoCurationNotFound
	}
	if err != nil {
		return resource.PhotoCurationView{}, err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT content_block.id, COALESCE(content_block.data->>'url', ''),
       COALESCE(content_block.data->>'file_id', '')
FROM xiangwan_resource_relations AS relation
JOIN xiangwan_resource_publications AS publication
  ON publication.tenant_id = relation.tenant_id
 AND publication.relation_id = relation.id
 AND NOT EXISTS (SELECT 1 FROM xiangwan_review_resource_replacements AS replacement
                 WHERE replacement.tenant_id = relation.tenant_id
                   AND replacement.previous_relation_id = relation.id)
 AND publication.content_id = relation.content_id
 AND publication.content_revision_at = relation.content_revision_at
 AND publication.access_policy = relation.access_policy
 AND publication.expected_target_version = relation.expected_target_version
JOIN contents AS content
  ON content.id = relation.content_id
 AND content.tenant_id = relation.tenant_id
 AND content.principal_id = relation.created_by
 AND content.type = 'review'
 AND content.status IN ('active', 'reviewing')
 AND content.visibility = 'private'
 AND content.updated_at = relation.content_revision_at
 AND content.deleted_at IS NULL
JOIN blocks AS content_block
  ON content_block.content_id = content.id
 AND content_block.type = 'image'
WHERE relation.tenant_id = $1 AND relation.id = $2 AND relation.content_id = $3
  AND relation.access_policy = 'public' AND publication.access_policy = 'public'
  AND NOT EXISTS (
      SELECT 1 FROM content_governance AS governance
      WHERE governance.content_id = relation.content_id
  )
ORDER BY content_block.sort_order ASC, content_block.id ASC LIMIT 31
`, service.tenantID, relationID, contentID)
	if err != nil {
		return resource.PhotoCurationView{}, err
	}
	for rows.Next() {
		var photo resource.ReviewPhoto
		var fileID string
		if err := rows.Scan(&photo.BlockID, &photo.URL, &fileID); err != nil {
			_ = rows.Close()
			return resource.PhotoCurationView{}, err
		}
		if fileID != "" {
			parsed, parseErr := uuid.Parse(fileID)
			if parseErr != nil || parsed == uuid.Nil {
				_ = rows.Close()
				return resource.PhotoCurationView{}, resource.ErrPhotoCurationConflict
			}
			photo.FileID = &parsed
		}
		view.OriginalPhotos = append(view.OriginalPhotos, photo)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return resource.PhotoCurationView{}, err
	}
	_ = rows.Close()
	if len(view.OriginalPhotos) > 30 {
		return resource.PhotoCurationView{}, resource.ErrPhotoCurationConflict
	}
	var encoded []byte
	var storedCover uuid.NullUUID
	err = tx.QueryRowContext(ctx, `
SELECT version, ordered_block_ids, cover_block_id FROM xiangwan_review_photo_curations
WHERE tenant_id = $1 AND relation_id = $2 ORDER BY version DESC LIMIT 1
`, service.tenantID, relationID).Scan(&view.Version, &encoded, &storedCover)
	if errors.Is(err, sql.ErrNoRows) {
		view.Version = 0
		view.OrderedBlockIDs = make([]uuid.UUID, 0, len(view.OriginalPhotos))
		for _, photo := range view.OriginalPhotos {
			view.OrderedBlockIDs = append(view.OrderedBlockIDs, photo.BlockID)
		}
		return view, nil
	}
	if err != nil {
		return resource.PhotoCurationView{}, err
	}
	view.OrderedBlockIDs, err = decodePhotoIDs(encoded)
	if err != nil {
		return resource.PhotoCurationView{}, resource.ErrPhotoCurationConflict
	}
	view.CoverBlockID = nullablePhotoCover(storedCover)
	if view.CoverBlockID != nil && !containsPhotoID(view.OrderedBlockIDs, *view.CoverBlockID) {
		return resource.PhotoCurationView{}, resource.ErrPhotoCurationConflict
	}
	known := make(map[uuid.UUID]bool, len(view.OriginalPhotos))
	for _, photo := range view.OriginalPhotos {
		known[photo.BlockID] = true
	}
	for _, id := range view.OrderedBlockIDs {
		if !known[id] {
			return resource.PhotoCurationView{}, resource.ErrPhotoCurationConflict
		}
	}
	return view, nil
}

func nullablePhotoCover(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	id := value.UUID
	return &id
}

func samePhotoCover(left, right *uuid.UUID) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func containsPhotoID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, value := range ids {
		if value == id {
			return true
		}
	}
	return false
}

func encodePhotoIDs(ids []uuid.UUID) ([]byte, error) {
	if len(ids) > 30 {
		return nil, resource.ErrInvalidPhotoCuration
	}
	seen := make(map[uuid.UUID]bool, len(ids))
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			return nil, resource.ErrInvalidPhotoCuration
		}
		seen[id] = true
		values = append(values, id.String())
	}
	return json.Marshal(values)
}

func decodePhotoIDs(encoded []byte) ([]uuid.UUID, error) {
	if trimmed := bytes.TrimSpace(encoded); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, resource.ErrInvalidPhotoCuration
	}
	var values []string
	if err := json.Unmarshal(encoded, &values); err != nil || len(values) > 30 {
		return nil, resource.ErrInvalidPhotoCuration
	}
	ids := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		id, err := uuid.Parse(value)
		if err != nil || id.String() != value {
			return nil, resource.ErrInvalidPhotoCuration
		}
		ids = append(ids, id)
	}
	if _, err := encodePhotoIDs(ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func equalPhotoIDs(left, right []uuid.UUID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (service *PhotoCurationService) audit(
	ctx context.Context, tx *sql.Tx, command resource.PhotoCurationReadCommand,
	action string, version int64, count int,
) error {
	requestID := command.RequestID
	if requestID == "" {
		requestID = uuid.NewString()
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, $4, 'review_relation', $5, $6,
    jsonb_build_object('identity_link_id', $7::TEXT,
    'curation_version', $8::BIGINT, 'photo_count', $9::INTEGER), $10, $10)
`, uuid.New(), service.tenantID, command.ActorID, action,
		command.RelationID, requestID, command.IdentityLinkID.String(),
		version, count, service.now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return fmt.Errorf("audit review photo curation: %w", err)
	}
	return nil
}

func classifyPhotoCurationError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "40001", "40P01":
			return resource.ErrPhotoCurationConflict
		}
	}
	return err
}
