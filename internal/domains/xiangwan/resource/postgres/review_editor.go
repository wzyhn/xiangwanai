package resourcepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contentstandalonepg "github.com/wzyhn/xiangwanai/internal/capabilities/content/standalonepg"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func (writer *ReviewResourceWriter) ListReviewResources(ctx context.Context, command resource.ReviewResourceListCommand) ([]resource.ReviewResourceEditView, error) {
	if writer == nil || writer.db == nil || writer.authorize == nil || ctx == nil || command.ActorID == uuid.Nil || command.IdentityLinkID == uuid.Nil || command.InstanceID == uuid.Nil || command.RequestID == "" {
		return nil, resource.ErrInvalidReviewResourceCommand
	}
	tx, err := writer.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := writer.authorize(ctx, tx, command.ActorID, command.IdentityLinkID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
SELECT relation.id FROM xiangwan_resource_relations AS relation
JOIN xiangwan_resource_publications AS publication
  ON publication.tenant_id = relation.tenant_id AND publication.relation_id = relation.id
WHERE relation.tenant_id = $1 AND relation.instance_id = $2
  AND NOT EXISTS (SELECT 1 FROM xiangwan_review_resource_replacements AS replacement
                  WHERE replacement.tenant_id = relation.tenant_id AND replacement.previous_relation_id = relation.id)
ORDER BY relation.sort_order, relation.id LIMIT 51
`, writer.tenantID, command.InstanceID)
	if err != nil {
		return nil, err
	}
	ids := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > 50 {
		return nil, resource.ErrReviewResourceConflict
	}
	views := []resource.ReviewResourceEditView{}
	curator := &PhotoCurationService{tenantID: writer.tenantID}
	for _, id := range ids {
		photoView, err := curator.readCurrent(ctx, tx, id)
		if errors.Is(err, resource.ErrPhotoCurationNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		relation, err := NewRepository(tx).GetByID(ctx, writer.tenantID, id)
		if err != nil {
			return nil, err
		}
		reader, err := contentstandalonepg.NewReader(tx)
		if err != nil {
			return nil, err
		}
		document, err := reader.ReadReviewAt(ctx, writer.tenantID, relation.CreatedBy, relation.ContentID, relation.ContentRevision)
		if err != nil {
			return nil, err
		}
		view := reviewEditView(relation, document)
		view.PhotoCurationVersion = photoView.Version
		views = append(views, view)
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (id, tenant_id, actor_id, action_code, target_type, target_id, request_id, details, occurred_at, created_at)
VALUES ($1, $2, $3, 'review.resource_edit_read', 'instance', $4, $5,
        jsonb_build_object('identity_link_id', $6::TEXT, 'returned', $7::INTEGER), $8, $8)
`, uuid.New(), writer.tenantID, command.ActorID, command.InstanceID, command.RequestID, command.IdentityLinkID.String(), len(views), time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return views, nil
}

func reviewEditView(relation resource.Relation, document contentstandalonepg.ReviewDocument) resource.ReviewResourceEditView {
	view := resource.ReviewResourceEditView{RelationID: relation.ID, SessionID: relation.SessionID, ExpectedTargetVersion: relation.ExpectedTargetVersion, Title: document.Title, SortOrder: relation.SortOrder, Photos: []string{}, Links: []resource.ReviewResourceLink{}, Editable: true}
	for _, block := range document.Blocks {
		var fields map[string]string
		if json.Unmarshal(block.Data, &fields) != nil {
			view.Editable = false
			continue
		}
		switch {
		case block.Type == "text":
			view.Description = fields["text"]
		case block.Type == "image" && fields["url"] != "":
			view.Photos = append(view.Photos, fields["url"])
		case block.Type == "link" && fields["kind"] == "video_channel_native":
			view.VideoChannel = &resource.ReviewVideoChannel{FinderUserName: fields["finder_user_name"], FeedID: fields["feed_id"]}
			if !resource.ValidReviewVideoChannel(*view.VideoChannel) {
				view.Editable = false
			}
		case block.Type == "link" && fields["kind"] == "video_channel":
			view.VideoURL = fields["url"]
		case block.Type == "link" && (fields["kind"] == resource.ReviewResourceLinkRecording || fields["kind"] == resource.ReviewResourceLinkMaterials):
			view.Links = append(view.Links, resource.ReviewResourceLink{Kind: fields["kind"], Title: fields["label"], Subtitle: fields["subtitle"], URL: fields["url"], Hidden: fields["enabled"] == "false"})
		default:
			view.Editable = false
		}
	}
	return view
}

// Lock the previous relation before the activity target, matching photo curation.
// The command must retain every original photo; visibility and order are copied
// by Block identity, so editing links cannot silently restore hidden images.
func (writer *ReviewResourceWriter) lockReviewReplacement(ctx context.Context, tx *sql.Tx, command resource.CreateReviewResourceCommand, photos []string) (*resource.PhotoCurationView, error) {
	if command.ReplacesRelationID == nil {
		return nil, nil
	}
	var id uuid.UUID
	if err := tx.QueryRowContext(ctx, `SELECT id FROM xiangwan_resource_relations WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, writer.tenantID, *command.ReplacesRelationID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, resource.ErrReviewResourceConflict
		}
		return nil, err
	}
	curator := &PhotoCurationService{tenantID: writer.tenantID}
	view, err := curator.readCurrent(ctx, tx, id)
	if errors.Is(err, resource.ErrPhotoCurationNotFound) {
		return nil, resource.ErrReviewResourceConflict
	}
	if err != nil {
		return nil, err
	}
	relation, err := NewRepository(tx).GetByID(ctx, writer.tenantID, id)
	if err != nil {
		return nil, err
	}
	if !reviewRelationMatchesCommand(relation, command) || view.Version != *command.ExpectedPhotoCurationVersion || len(command.Files) != 0 {
		return nil, resource.ErrReviewResourceConflict
	}
	reader, err := contentstandalonepg.NewReader(tx)
	if err != nil {
		return nil, err
	}
	document, err := reader.ReadReviewAt(ctx, writer.tenantID, relation.CreatedBy, relation.ContentID, relation.ContentRevision)
	if err != nil {
		return nil, err
	}
	old := reviewEditView(relation, document)
	if !old.Editable || len(old.Photos) != len(photos) {
		return nil, resource.ErrReviewResourceConflict
	}
	for i := range photos {
		if photos[i] != old.Photos[i] {
			return nil, resource.ErrReviewResourceConflict
		}
	}
	return &view, nil
}

func (writer *ReviewResourceWriter) recordReviewReplacement(ctx context.Context, tx *sql.Tx, command resource.CreateReviewResourceCommand, relation resource.Relation, photos *resource.PhotoCurationView, now time.Time) error {
	if photos == nil {
		return nil
	}
	if photos.Version > 0 {
		mapped := make(map[uuid.UUID]uuid.UUID, len(photos.OriginalPhotos))
		for index, photo := range photos.OriginalPhotos {
			mapped[photo.BlockID] = uuid.NewSHA1(relation.ContentID, []byte(fmt.Sprintf("photo-%d", index)))
		}
		ids := make([]uuid.UUID, 0, len(photos.OrderedBlockIDs))
		for _, id := range photos.OrderedBlockIDs {
			ids = append(ids, mapped[id])
		}
		encoded, err := encodePhotoIDs(ids)
		if err != nil {
			return err
		}
		var cover *uuid.UUID
		if photos.CoverBlockID != nil {
			id, exists := mapped[*photos.CoverBlockID]
			if !exists || !containsPhotoID(ids, id) {
				return resource.ErrReviewResourceConflict
			}
			cover = &id
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO xiangwan_review_photo_curations (id, tenant_id, relation_id, version, expected_version, operation_id, actor_id, ordered_block_ids, created_at, cover_block_id, cover_specified) VALUES ($1,$2,$3,1,0,$4,$5,$6::jsonb,$7,$8,true)`, uuid.New(), writer.tenantID, relation.ID, command.OperationID, command.ActorID, string(encoded), now, cover)
		if err != nil {
			return classifyPhotoCurationError(err)
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO xiangwan_review_resource_replacements (id, tenant_id, previous_relation_id, replacement_relation_id, photo_curation_version, actor_id, operation_id, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.New(), writer.tenantID, *command.ReplacesRelationID, relation.ID, *command.ExpectedPhotoCurationVersion, command.ActorID, command.OperationID, now)
	if err != nil {
		return errors.Join(resource.ErrReviewResourceConflict, err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO xiangwan_admin_audit_events (id,tenant_id,actor_id,action_code,target_type,target_id,request_id,details,occurred_at,created_at) VALUES ($1,$2,$3,'review.resource_replace','instance',$4,$5,jsonb_build_object('previous_relation_id',$6::TEXT,'replacement_relation_id',$7::TEXT),$8,$8)`, uuid.New(), writer.tenantID, command.ActorID, command.InstanceID, command.RequestID, command.ReplacesRelationID.String(), relation.ID.String(), now)
	return err
}

func (writer *ReviewResourceWriter) reviewReplacementReplayMatches(ctx context.Context, tx *sql.Tx, command resource.CreateReviewResourceCommand, relationID uuid.UUID) (bool, error) {
	var previous uuid.UUID
	var version int64
	err := tx.QueryRowContext(ctx, `SELECT previous_relation_id, photo_curation_version FROM xiangwan_review_resource_replacements WHERE tenant_id = $1 AND replacement_relation_id = $2`, writer.tenantID, relationID).Scan(&previous, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return command.ReplacesRelationID == nil, nil
	}
	if err != nil {
		return false, err
	}
	return command.ReplacesRelationID != nil && command.ExpectedPhotoCurationVersion != nil && *command.ReplacesRelationID == previous && *command.ExpectedPhotoCurationVersion == version, nil
}
