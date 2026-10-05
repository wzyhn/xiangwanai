package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	storagestand "github.com/wzyhn/xiangwanai/internal/capabilities/storage/standalonepg"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

const reviewMediaIntentLifetime = 30 * time.Minute

// ReviewMediaUploads composes Storage's verified local bytes and File metadata
// with a target-bound Xiangwan intent. It does not publish unreviewed media.
type ReviewMediaUploads struct {
	catalog *Catalog
	stager  *storagestand.ReviewLocalStager
}

func NewReviewMediaUploads(catalog *Catalog, stager *storagestand.ReviewLocalStager) (*ReviewMediaUploads, error) {
	if catalog == nil || stager == nil {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	return &ReviewMediaUploads{catalog: catalog, stager: stager}, nil
}

func (service *ReviewMediaUploads) valid(ctx context.Context, actor xiangwanadmin.Principal) bool {
	return service != nil && service.stager != nil && service.catalog.valid(ctx) &&
		actor.PrincipalID != uuid.Nil && actor.IdentityLinkID != uuid.Nil
}

func reviewMediaLimit(kind xiangwanadmin.ReviewMediaKind, mime string) int64 {
	switch kind {
	case xiangwanadmin.ReviewMediaPhoto:
		if mime == "image/jpeg" || mime == "image/png" || mime == "image/webp" {
			return 10 << 20
		}
	case xiangwanadmin.ReviewMediaVideo:
		if mime == "video/mp4" {
			return storagestand.MaxReviewObjectBytes
		}
	case xiangwanadmin.ReviewMediaAudio:
		if mime == "audio/mpeg" {
			return 50 << 20
		}
	case xiangwanadmin.ReviewMediaMaterial:
		if mime == "application/pdf" {
			return 20 << 20
		}
	}
	return 0
}

func validReviewMediaCommand(command xiangwanadmin.IssueReviewMediaUploadCommand) bool {
	canonicalMIME, err := storagestand.CanonicalMIME(command.MIME)
	return err == nil && command.MIME == canonicalMIME &&
		command.OperationID != uuid.Nil && command.OperationID.Version() == 4 &&
		command.OperationID.Variant() == uuid.RFC4122 && command.InstanceID != uuid.Nil &&
		(command.SessionID == nil || *command.SessionID != uuid.Nil) &&
		command.Size > 0 && command.Size <= reviewMediaLimit(command.Kind, command.MIME) &&
		validReviewMediaChecksum(command.SHA256)
}

// The original client filename is neither needed for the public review nor
// safe to store before text moderation. Use a stable server-only basename.
func reviewMediaFilename(fileID uuid.UUID, mime string) string {
	extension := map[string]string{
		"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp",
		"video/mp4": "mp4", "audio/mpeg": "mp3", "application/pdf": "pdf",
	}[mime]
	if fileID == uuid.Nil || extension == "" {
		return ""
	}
	return "review-" + fileID.String() + "." + extension
}

func validReviewMediaChecksum(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}

func (service *ReviewMediaUploads) IssueReviewMediaUpload(
	ctx context.Context, actor xiangwanadmin.Principal,
	command xiangwanadmin.IssueReviewMediaUploadCommand,
) (xiangwanadmin.ReviewMediaUpload, error) {
	if !service.valid(ctx, actor) || !validReviewMediaCommand(command) {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrInvalidMediaUpload
	}
	write, err := service.catalog.beginActivityWrite(ctx, actor.PrincipalID,
		actor.IdentityLinkID, command.OperationID)
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	defer func() { _ = write.Rollback() }()
	if err := lockReviewMediaTarget(ctx, write.Tx, service.catalog.tenantID,
		command.InstanceID, command.SessionID); err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	now := service.catalog.now().UTC().Truncate(time.Microsecond)
	storageWriter, err := storagestand.NewWriter(write.Tx)
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	_, err = storageWriter.CreatePending(ctx, storagestand.PendingFileInput{
		ID: command.OperationID, PrincipalID: actor.PrincipalID,
		Filename: reviewMediaFilename(command.OperationID, command.MIME),
		MIME:     command.MIME, Size: command.Size,
		ProviderObjectKey: storagestand.ReviewObjectKey(command.OperationID),
		CreatedAt:         now,
	})
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, mapReviewMediaStorageError(err)
	}
	result, err := write.ExecContext(ctx, `
INSERT INTO xiangwan_review_media_uploads (
    file_id, tenant_id, actor_id, identity_link_id, instance_id, session_id,
    media_kind, mime, expected_size, expected_sha256, created_at, expires_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (file_id) DO NOTHING
`, command.OperationID, service.catalog.tenantID, actor.PrincipalID,
		actor.IdentityLinkID, command.InstanceID, nullableUUID(command.SessionID),
		command.Kind, command.MIME, command.Size, command.SHA256,
		now, now.Add(reviewMediaIntentLifetime))
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, fmt.Errorf("record review media intent: %w", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	stored, err := readReviewMediaIntent(ctx, write.Tx, service.catalog.tenantID,
		command.OperationID)
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	if stored.ActorID != actor.PrincipalID || stored.IdentityLinkID != actor.IdentityLinkID ||
		stored.InstanceID != command.InstanceID || !sameOptionalUUID(stored.SessionID, command.SessionID) ||
		stored.Kind != command.Kind || stored.MIME != command.MIME ||
		stored.Size != command.Size || stored.SHA256 != command.SHA256 {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrMediaUploadConflict
	}
	if stored.ConfirmedAt == nil && now.After(stored.ExpiresAt) {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrMediaUploadExpired
	}
	if created == 1 {
		if err := auditReviewMediaWrite(ctx, write.Tx, service.catalog.tenantID,
			actor.PrincipalID, actor.IdentityLinkID, "media.intent_created",
			command.OperationID, now); err != nil {
			return xiangwanadmin.ReviewMediaUpload{}, err
		}
	}
	if err := write.Commit(); err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	return stored.ReviewMediaUpload, nil
}

func (service *ReviewMediaUploads) StageReviewMediaBytes(
	ctx context.Context, actor xiangwanadmin.Principal, fileID uuid.UUID,
	data io.Reader,
) (xiangwanadmin.ReviewMediaUpload, error) {
	if !service.valid(ctx, actor) || fileID == uuid.Nil || data == nil {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrInvalidMediaUpload
	}
	intent, err := service.readAuthorizedIntent(ctx, actor, fileID)
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	// A confirmed receipt may be replayed after its intent expires, but bytes
	// must never be staged again. Otherwise a late retry could recreate a
	// selected object after the File cleanup worker has deleted it.
	if !reviewMediaStageWindowOpen(service.catalog.now().UTC(), intent.ExpiresAt) {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrMediaUploadExpired
	}
	stageContext, cancelStage := context.WithDeadline(ctx, intent.ExpiresAt)
	defer cancelStage()
	if _, err := service.stager.Stage(stageContext, fileID, data, intent.MIME,
		intent.Size, intent.SHA256); err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, mapReviewMediaStorageError(err)
	}
	// Recheck after IO as well. An early lifecycle closure must not leave bytes
	// recreated by an in-flight request after the cleanup permit was committed.
	if _, err := service.readAuthorizedIntent(ctx, actor, fileID); err != nil {
		if errors.Is(err, xiangwanadmin.ErrMediaUploadExpired) {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cleanupCancel()
			err = errors.Join(err, service.stager.DeleteSelected(cleanupCtx, fileID))
		}
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	return intent.ReviewMediaUpload, nil
}

func reviewMediaStageWindowOpen(now, expiresAt time.Time) bool {
	return !now.IsZero() && !expiresAt.IsZero() && !now.After(expiresAt)
}

func (service *ReviewMediaUploads) ConfirmReviewMediaUpload(
	ctx context.Context, actor xiangwanadmin.Principal,
	fileID uuid.UUID, checksum string,
) (xiangwanadmin.ReviewMediaUpload, error) {
	if !service.valid(ctx, actor) || fileID == uuid.Nil || !validReviewMediaChecksum(checksum) {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrInvalidMediaUpload
	}
	first, err := service.readAuthorizedIntent(ctx, actor, fileID)
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	if checksum != first.SHA256 {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrMediaUploadConflict
	}
	facts, err := service.stager.Inspect(ctx, fileID, first.MIME, first.Size, checksum)
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, mapReviewMediaStorageError(err)
	}
	write, err := service.catalog.beginActivityWrite(ctx, actor.PrincipalID,
		actor.IdentityLinkID, fileID)
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	defer func() { _ = write.Rollback() }()
	stored, err := readReviewMediaIntent(ctx, write.Tx, service.catalog.tenantID, fileID)
	if err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	if stored.ActorID != actor.PrincipalID || stored.IdentityLinkID != actor.IdentityLinkID ||
		stored.MIME != facts.MIME || stored.Size != facts.Size || stored.SHA256 != facts.SHA256 {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrMediaUploadConflict
	}
	now := service.catalog.now().UTC().Truncate(time.Microsecond)
	if stored.ConfirmedAt == nil && now.After(stored.ExpiresAt) {
		return xiangwanadmin.ReviewMediaUpload{}, xiangwanadmin.ErrMediaUploadExpired
	}
	if stored.ConfirmedAt == nil {
		if err := lockReviewMediaTarget(ctx, write.Tx, service.catalog.tenantID,
			stored.InstanceID, stored.SessionID); err != nil {
			return xiangwanadmin.ReviewMediaUpload{}, err
		}
		storageWriter, err := storagestand.NewWriter(write.Tx)
		if err != nil {
			return xiangwanadmin.ReviewMediaUpload{}, err
		}
		if _, err := storageWriter.Confirm(ctx, actor.PrincipalID, fileID,
			storagestand.ProviderObjectFacts{
				ProviderObjectKey: facts.ProviderObjectKey,
				MIME:              facts.MIME, Size: facts.Size,
			}); err != nil {
			return xiangwanadmin.ReviewMediaUpload{}, mapReviewMediaStorageError(err)
		}
		if _, err := write.ExecContext(ctx, `
UPDATE xiangwan_review_media_uploads SET confirmed_at = $3
WHERE tenant_id = $1 AND file_id = $2 AND confirmed_at IS NULL
`, service.catalog.tenantID, fileID, now); err != nil {
			return xiangwanadmin.ReviewMediaUpload{}, fmt.Errorf("confirm review media intent: %w", err)
		}
		if err := auditReviewMediaWrite(ctx, write.Tx, service.catalog.tenantID,
			actor.PrincipalID, actor.IdentityLinkID, "media.bytes_confirmed", fileID, now); err != nil {
			return xiangwanadmin.ReviewMediaUpload{}, err
		}
		stored.ConfirmedAt = &now
	}
	if err := write.Commit(); err != nil {
		return xiangwanadmin.ReviewMediaUpload{}, err
	}
	return stored.ReviewMediaUpload, nil
}

// OpenReviewMediaPreview permits a current activity operator to inspect only
// their own target-bound, confirmed File. Authorization, active generation,
// File lifecycle and the audit attempt are checked in one short transaction;
// potentially large checksum IO runs after that transaction commits. A second
// authorized transaction records a successful open only after byte integrity
// verification. Publication can require this evidence independently of a
// caller's manual-review assertion.
func (service *ReviewMediaUploads) OpenReviewMediaPreview(
	ctx context.Context, actor xiangwanadmin.Principal, fileID uuid.UUID,
) (xiangwanadmin.ReviewMediaPreview, error) {
	if !service.valid(ctx, actor) || storagestand.ReviewObjectKey(fileID) == "" {
		return xiangwanadmin.ReviewMediaPreview{}, xiangwanadmin.ErrInvalidMediaUpload
	}
	write, err := service.catalog.beginActivityWrite(ctx, actor.PrincipalID,
		actor.IdentityLinkID, fileID)
	if err != nil {
		return xiangwanadmin.ReviewMediaPreview{}, err
	}
	defer func() { _ = write.Rollback() }()
	intent, err := readReviewMediaIntent(ctx, write.Tx, service.catalog.tenantID, fileID)
	if err != nil {
		return xiangwanadmin.ReviewMediaPreview{}, err
	}
	if intent.ActorID != actor.PrincipalID ||
		intent.IdentityLinkID != actor.IdentityLinkID {
		return xiangwanadmin.ReviewMediaPreview{}, xiangwanadmin.ErrScopeForbidden
	}
	if intent.ConfirmedAt == nil {
		return xiangwanadmin.ReviewMediaPreview{}, xiangwanadmin.ErrMediaUploadConflict
	}
	if err := lockReviewMediaTarget(ctx, write.Tx, service.catalog.tenantID,
		intent.InstanceID, intent.SessionID); err != nil {
		return xiangwanadmin.ReviewMediaPreview{}, err
	}
	var liveFileID uuid.UUID
	err = write.QueryRowContext(ctx, `
SELECT id FROM files
WHERE id = $1 AND principal_id = $2
  AND file_key = 'xiangwan-review/' || id::text
  AND mime = $3 AND size = $4 AND status = 'confirmed'
  AND deleting_at IS NULL AND expired_at IS NULL
  AND (delete_after IS NULL OR delete_after > clock_timestamp())
FOR SHARE
`, fileID, actor.PrincipalID, intent.MIME, intent.Size).Scan(&liveFileID)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.ReviewMediaPreview{}, xiangwanadmin.ErrMediaUploadExpired
	}
	if err != nil {
		return xiangwanadmin.ReviewMediaPreview{},
			fmt.Errorf("lock review media preview File: %w", err)
	}
	if liveFileID != fileID {
		return xiangwanadmin.ReviewMediaPreview{}, xiangwanadmin.ErrMediaUploadConflict
	}
	if err := auditReviewMediaWrite(ctx, write.Tx, service.catalog.tenantID,
		actor.PrincipalID, actor.IdentityLinkID,
		"media.private_preview_attempt", fileID, service.catalog.now().UTC()); err != nil {
		return xiangwanadmin.ReviewMediaPreview{}, err
	}
	if err := write.Commit(); err != nil {
		return xiangwanadmin.ReviewMediaPreview{}, err
	}
	reader, err := service.stager.OpenSelected(ctx, fileID,
		intent.MIME, intent.Size, intent.SHA256)
	if err != nil {
		return xiangwanadmin.ReviewMediaPreview{}, mapReviewMediaStorageError(err)
	}
	opened, err := service.catalog.beginActivityWrite(ctx, actor.PrincipalID,
		actor.IdentityLinkID, fileID)
	if err != nil {
		_ = reader.Close()
		return xiangwanadmin.ReviewMediaPreview{}, err
	}
	defer func() { _ = opened.Rollback() }()
	if err := auditReviewMediaWrite(ctx, opened.Tx, service.catalog.tenantID,
		actor.PrincipalID, actor.IdentityLinkID,
		"media.private_preview_opened", fileID, service.catalog.now().UTC()); err != nil {
		_ = reader.Close()
		return xiangwanadmin.ReviewMediaPreview{}, err
	}
	if err := opened.Commit(); err != nil {
		_ = reader.Close()
		return xiangwanadmin.ReviewMediaPreview{}, err
	}
	return xiangwanadmin.ReviewMediaPreview{
		Reader: reader, MIME: intent.MIME, Size: intent.Size,
	}, nil
}

type storedReviewMediaIntent struct {
	xiangwanadmin.ReviewMediaUpload
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
}

func (service *ReviewMediaUploads) readAuthorizedIntent(
	ctx context.Context, actor xiangwanadmin.Principal,
	fileID uuid.UUID,
) (storedReviewMediaIntent, error) {
	write, err := service.catalog.beginActivityWrite(ctx, actor.PrincipalID,
		actor.IdentityLinkID, fileID)
	if err != nil {
		return storedReviewMediaIntent{}, err
	}
	defer func() { _ = write.Rollback() }()
	intent, err := readReviewMediaIntent(ctx, write.Tx, service.catalog.tenantID, fileID)
	if err != nil {
		return storedReviewMediaIntent{}, err
	}
	if intent.ActorID != actor.PrincipalID || intent.IdentityLinkID != actor.IdentityLinkID {
		return storedReviewMediaIntent{}, xiangwanadmin.ErrScopeForbidden
	}
	if intent.ConfirmedAt == nil && service.catalog.now().UTC().After(intent.ExpiresAt) {
		return storedReviewMediaIntent{}, xiangwanadmin.ErrMediaUploadExpired
	}
	storageWriter, err := storagestand.NewWriter(write.Tx)
	if err != nil {
		return storedReviewMediaIntent{}, err
	}
	file, err := storageWriter.GetForStaging(ctx, actor.PrincipalID, fileID)
	if err != nil {
		return storedReviewMediaIntent{}, mapReviewMediaStorageError(err)
	}
	if file.MIME != intent.MIME || file.Size != intent.Size {
		return storedReviewMediaIntent{}, xiangwanadmin.ErrMediaUploadConflict
	}
	if err := write.Commit(); err != nil {
		return storedReviewMediaIntent{}, err
	}
	return intent, nil
}

func readReviewMediaIntent(
	ctx context.Context, tx *sql.Tx, tenantID, fileID uuid.UUID,
) (storedReviewMediaIntent, error) {
	var intent storedReviewMediaIntent
	var sessionID uuid.NullUUID
	var confirmedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `
SELECT file_id, actor_id, identity_link_id, instance_id, session_id, media_kind,
       mime, expected_size, expected_sha256, created_at, expires_at, confirmed_at
FROM xiangwan_review_media_uploads
WHERE tenant_id = $1 AND file_id = $2
FOR UPDATE
`, tenantID, fileID).Scan(&intent.FileID, &intent.ActorID, &intent.IdentityLinkID,
		&intent.InstanceID, &sessionID, &intent.Kind, &intent.MIME, &intent.Size,
		&intent.SHA256, &intent.CreatedAt, &intent.ExpiresAt, &confirmedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return storedReviewMediaIntent{}, xiangwanadmin.ErrMediaUploadConflict
	}
	if err != nil {
		return storedReviewMediaIntent{}, fmt.Errorf("read review media intent: %w", err)
	}
	if sessionID.Valid {
		intent.SessionID = &sessionID.UUID
	}
	if confirmedAt.Valid {
		intent.ConfirmedAt = &confirmedAt.Time
	}
	return intent, nil
}

func lockReviewMediaTarget(
	ctx context.Context, tx *sql.Tx, tenantID, instanceID uuid.UUID,
	sessionID *uuid.UUID,
) error {
	var status string
	err := tx.QueryRowContext(ctx, `
SELECT status FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2 FOR SHARE
`, tenantID, instanceID).Scan(&status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return xiangwanadmin.ErrMediaUploadConflict
		}
		return err
	}
	if sessionID == nil {
		if status != "completed" && status != "archived" {
			return xiangwanadmin.ErrMediaUploadConflict
		}
		return nil
	}
	if status != "published" && status != "completed" && status != "archived" {
		return xiangwanadmin.ErrMediaUploadConflict
	}
	err = tx.QueryRowContext(ctx, `
SELECT status FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3 FOR SHARE
`, tenantID, instanceID, *sessionID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.ErrMediaUploadConflict
	}
	if err != nil {
		return err
	}
	if status != "published" && status != "ended" && status != "archived" {
		return xiangwanadmin.ErrMediaUploadConflict
	}
	return nil
}

func sameOptionalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func mapReviewMediaStorageError(err error) error {
	switch {
	case errors.Is(err, storagestand.ErrInvalidReviewObject),
		errors.Is(err, storagestand.ErrInvalidFile):
		return xiangwanadmin.ErrInvalidMediaUpload
	case errors.Is(err, storagestand.ErrFileExpired):
		return xiangwanadmin.ErrMediaUploadExpired
	case errors.Is(err, storagestand.ErrReviewObjectConflict),
		errors.Is(err, storagestand.ErrReviewObjectMissing),
		errors.Is(err, storagestand.ErrFileConflict),
		errors.Is(err, storagestand.ErrFileNotConfirmed),
		errors.Is(err, storagestand.ErrProviderObjectFacts),
		errors.Is(err, storagestand.ErrFileForbidden),
		errors.Is(err, storagestand.ErrFileNotFound):
		return xiangwanadmin.ErrMediaUploadConflict
	default:
		return err
	}
}

func auditReviewMediaWrite(
	ctx context.Context, tx *sql.Tx, tenantID, actorID,
	identityLinkID uuid.UUID, action string, fileID uuid.UUID,
	now time.Time,
) error {
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	_, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1,$2,$3,$4,'file',$5,$6,
    jsonb_build_object('identity_link_id', $7::TEXT),$8,$8)
`, uuid.New(), tenantID, actorID, action, fileID, requestID,
		identityLinkID.String(), now)
	if err != nil {
		return fmt.Errorf("audit review media write: %w", err)
	}
	return nil
}
