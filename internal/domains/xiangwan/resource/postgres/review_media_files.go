package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	storagestand "github.com/wzyhn/xiangwanai/internal/capabilities/storage/standalonepg"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

type reviewMediaFileFacts struct {
	MIME   string
	Size   int64
	SHA256 string
}

// Byte verification stays outside the short publication transaction. The
// transaction rechecks every immutable intent, File owner and lifecycle before
// Pin; the local provider never overwrites a selected UUID object.
func (writer *ReviewResourceWriter) verifyReviewFiles(
	ctx context.Context, command resource.CreateReviewResourceCommand,
) error {
	if len(command.Files) == 0 {
		return nil
	}
	if writer.mediaStager == nil {
		return resource.ErrReviewResourceUnavailable
	}
	// Do not let an authenticated but de-granted administrator trigger a large
	// checksum read. This short preliminary authorization is repeated under
	// the final publication transaction to fence a concurrent Grant cutover.
	preflight, err := writer.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin review File preflight: %w", err)
	}
	defer func() { _ = preflight.Rollback() }()
	if err := writer.authorize(ctx, preflight, command.ActorID,
		command.IdentityLinkID); err != nil {
		return err
	}
	if err := preflight.Commit(); err != nil {
		return fmt.Errorf("commit review File preflight: %w", err)
	}
	for _, file := range command.Files {
		facts, err := readReviewMediaFileFacts(ctx, writer.db,
			writer.tenantID, command, file)
		if err != nil {
			return err
		}
		if _, err := writer.mediaStager.Inspect(ctx, file.FileID,
			facts.MIME, facts.Size, facts.SHA256); err != nil {
			return errors.Join(resource.ErrReviewResourceConflict, err)
		}
	}
	return nil
}

func pinReviewFiles(
	ctx context.Context, tx *sql.Tx, tenantID uuid.UUID,
	command resource.CreateReviewResourceCommand,
) error {
	if len(command.Files) == 0 {
		return nil
	}
	storageWriter, err := storagestand.NewWriter(tx)
	if err != nil {
		return resource.ErrReviewResourceUnavailable
	}
	ordered := append([]resource.ReviewResourceFile(nil), command.Files...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].FileID.String() < ordered[j].FileID.String()
	})
	for _, selected := range ordered {
		facts, err := readReviewMediaFileFacts(ctx, tx, tenantID, command, selected)
		if err != nil {
			return err
		}
		file, err := storageWriter.Pin(ctx, command.ActorID, selected.FileID)
		if err != nil {
			return errors.Join(resource.ErrReviewResourceConflict, err)
		}
		if file.MIME != facts.MIME || file.Size != facts.Size {
			return resource.ErrReviewResourceConflict
		}
		var objectKey string
		if err := tx.QueryRowContext(ctx, `
SELECT file_key FROM files WHERE id = $1 AND principal_id = $2
`, selected.FileID, command.ActorID).Scan(&objectKey); err != nil {
			return fmt.Errorf("verify pinned review File key: %w", err)
		}
		if objectKey != storagestand.ReviewObjectKey(selected.FileID) {
			return resource.ErrReviewResourceConflict
		}
	}
	return nil
}

type reviewMediaFactsQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readReviewMediaFileFacts(
	ctx context.Context, query reviewMediaFactsQuery, tenantID uuid.UUID,
	command resource.CreateReviewResourceCommand,
	selected resource.ReviewResourceFile,
) (reviewMediaFileFacts, error) {
	if ctx == nil || query == nil || tenantID == uuid.Nil {
		return reviewMediaFileFacts{}, resource.ErrReviewResourceUnavailable
	}
	var facts reviewMediaFileFacts
	err := query.QueryRowContext(ctx, `
SELECT mime, expected_size, expected_sha256
FROM xiangwan_review_media_uploads AS upload
WHERE upload.tenant_id = $1 AND upload.file_id = $2 AND upload.actor_id = $3
  AND upload.identity_link_id = $4 AND upload.instance_id = $5
  AND upload.session_id IS NOT DISTINCT FROM $6::uuid
  AND upload.media_kind = $7 AND upload.confirmed_at IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM xiangwan_admin_audit_events AS audit
      WHERE audit.tenant_id = upload.tenant_id
        AND audit.actor_id = upload.actor_id
        AND audit.target_type = 'file'
        AND audit.target_id = upload.file_id
        AND audit.action_code = 'media.private_preview_opened'
        AND audit.details->>'identity_link_id' = upload.identity_link_id::text
        AND audit.occurred_at >= upload.confirmed_at
  )
FOR SHARE
`, tenantID, selected.FileID, command.ActorID, command.IdentityLinkID,
		command.InstanceID, command.SessionID, selected.Kind,
	).Scan(&facts.MIME, &facts.Size, &facts.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return reviewMediaFileFacts{}, resource.ErrReviewResourceConflict
	}
	if err != nil {
		return reviewMediaFileFacts{}, fmt.Errorf("read review media File intent: %w", err)
	}
	if facts.SHA256 != selected.SHA256 ||
		!validReviewFileMIME(selected.Kind, facts.MIME, facts.Size) {
		return reviewMediaFileFacts{}, resource.ErrReviewResourceConflict
	}
	return facts, nil
}

func validReviewFileMIME(kind, mime string, size int64) bool {
	if size <= 0 {
		return false
	}
	switch kind {
	case resource.ReviewResourceFilePhoto:
		return size <= 10<<20 &&
			(mime == "image/jpeg" || mime == "image/png" || mime == "image/webp")
	case resource.ReviewResourceFileVideo:
		return size <= storagestand.MaxReviewObjectBytes && mime == "video/mp4"
	case resource.ReviewResourceFileAudio:
		return size <= 50<<20 && mime == "audio/mpeg"
	case resource.ReviewResourceFileMaterial:
		return size <= 20<<20 && mime == "application/pdf"
	default:
		return false
	}
}
