package resourcepostgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	contentstandalonepg "github.com/wzyhn/xiangwanai/internal/capabilities/content/standalonepg"
	storagestand "github.com/wzyhn/xiangwanai/internal/capabilities/storage/standalonepg"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

const (
	reviewWriterProvider       = "xiangwan.admin"
	reviewWriterPolicyDefault  = "xiangwan-review-manual-v1"
	reviewWriterReason         = "管理员在后台确认本期回顾内容"
	maxReviewTitleRunes        = 160
	maxReviewDescriptionRunes  = 8000
	maxReviewPhotos            = 30
	maxReviewLinks             = 2
	maxReviewLinkTitleRunes    = 160
	maxReviewLinkSubtitleRunes = 240
	maxReviewFiles             = 33
)

// ReviewResourceWriter persists an operator-authored review document through
// the provider-owned Content seam and the Xiangwan immutable publication
// chain. It never exposes shared Content or File repositories to the API.
type ReviewResourceWriter struct {
	db             *sql.DB
	tenantID       uuid.UUID
	externalDomain resource.ExternalDomainPolicy
	policyVersion  string
	authorize      ReviewResourceAuthorizer
	mediaStager    *storagestand.ReviewLocalStager
}

// NewReviewResourceWriterWithMedia enables the reviewed-byte path only when a
// private Storage stager is explicitly supplied. The ordinary constructor
// stays closed to File references, including in the production Runtime.
func NewReviewResourceWriterWithMedia(
	db *sql.DB, tenantID uuid.UUID,
	externalDomain resource.ExternalDomainPolicy, policyVersion string,
	mediaStager *storagestand.ReviewLocalStager,
	authorize ReviewResourceAuthorizer,
) (*ReviewResourceWriter, error) {
	if mediaStager == nil || authorize == nil {
		return nil, resource.ErrReviewResourceUnavailable
	}
	writer, err := NewReviewResourceWriter(db, tenantID, externalDomain,
		policyVersion, authorize)
	if err != nil {
		return nil, err
	}
	writer.mediaStager = mediaStager
	return writer, nil
}

// ReviewResourceAuthorizer is called after the writer opens its transaction
// and before it reads the idempotency receipt or writes any Content/resource
// fact.  The callback must lock and validate the active runtime generation,
// the exact administrator identity link, the live Principal, and the active
// activity-operator Grant on that same transaction.  Keeping this as a
// function seam avoids importing the administrator package into the resource
// provider (which would create a package ownership cycle).
type ReviewResourceAuthorizer func(
	context.Context,
	*sql.Tx,
	uuid.UUID,
	uuid.UUID,
) error

// NewReviewResourceWriter creates the concrete runtime writer. An empty
// external-domain policy is intentionally accepted here; link creation then
// fails closed per the public resource policy instead of making startup depend
// on optional customer configuration. A transaction-bound authorizer is
// required before CreateReviewResource can write; the variadic form preserves
// source compatibility for constructor-only tests while an unauthenticated
// writer always fails closed at execution time.
func NewReviewResourceWriter(
	db *sql.DB,
	tenantID uuid.UUID,
	externalDomain resource.ExternalDomainPolicy,
	policyVersion string,
	authorizers ...ReviewResourceAuthorizer,
) (*ReviewResourceWriter, error) {
	policyVersion = strings.TrimSpace(policyVersion)
	if db == nil || tenantID == uuid.Nil {
		return nil, resource.ErrReviewResourceUnavailable
	}
	if policyVersion == "" {
		policyVersion = reviewWriterPolicyDefault
	}
	var authorize ReviewResourceAuthorizer
	if len(authorizers) > 1 {
		return nil, resource.ErrReviewResourceUnavailable
	}
	if len(authorizers) == 1 {
		authorize = authorizers[0]
	}
	return &ReviewResourceWriter{
		db:             db,
		tenantID:       tenantID,
		externalDomain: externalDomain,
		policyVersion:  policyVersion,
		authorize:      authorize,
	}, nil
}

func (writer *ReviewResourceWriter) CreateReviewResource(
	ctx context.Context,
	command resource.CreateReviewResourceCommand,
) (resource.ReviewResourceReceipt, error) {
	if writer == nil || writer.db == nil || writer.authorize == nil || ctx == nil {
		return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceUnavailable
	}
	if err := validateReviewResourceCommand(command); err != nil {
		return resource.ReviewResourceReceipt{}, err
	}
	canonicalURL := ""
	if command.VideoURL != "" {
		var allowed bool
		canonicalURL, allowed = writer.externalDomain.AllowURL(command.VideoURL)
		if !allowed {
			return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceExternalLink
		}
	}
	canonicalPhotos, canonicalLinks, err := canonicalReviewExtras(
		writer.externalDomain,
		command.Photos,
		command.Links,
	)
	if err != nil {
		return resource.ReviewResourceReceipt{}, err
	}
	// Replays still return their immutable receipt if an old approved File was
	// later taken down, so defer any preflight failure until after the receipt
	// lookup. New publication cannot proceed without verified selected bytes.
	mediaVerifyErr := writer.verifyReviewFiles(ctx, command)

	now := command.Now.UTC()
	if command.Now.IsZero() {
		now = time.Now().UTC()
	}
	// PostgreSQL timestamptz keeps microseconds.  Normalize the single
	// operation timestamp before constructing Content, moderation, relation,
	// and publication facts so the first response and an idempotent replay
	// expose the same immutable receipt.
	now = now.Truncate(time.Microsecond)
	tx, err := writer.db.BeginTx(ctx, nil)
	if err != nil {
		return resource.ReviewResourceReceipt{}, fmt.Errorf("begin review resource transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := lockReviewOperation(ctx, tx, writer.tenantID, command); err != nil {
		return resource.ReviewResourceReceipt{}, fmt.Errorf(
			"lock review resource operation: %w", err,
		)
	}
	if err := writer.authorize(
		ctx, tx, command.ActorID, command.IdentityLinkID,
	); err != nil {
		return resource.ReviewResourceReceipt{}, err
	}

	repository := NewRepository(tx)
	idempotencyKey := command.OperationID.String()
	if existing, readErr := repository.GetByIdempotencyKey(
		ctx, writer.tenantID, command.ActorID, idempotencyKey,
	); readErr == nil {
		contentID := uuid.NewSHA1(
			uuid.NameSpaceURL,
			[]byte("wq-xiangwan/review/"+writer.tenantID.String()+"/"+idempotencyKey),
		)
		reader, readerErr := contentstandalonepg.NewReader(tx)
		if readerErr != nil {
			return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceUnavailable
		}
		document, documentErr := reader.ReadReviewAt(
			ctx, writer.tenantID, existing.CreatedBy, existing.ContentID,
			existing.ContentRevision,
		)
		if documentErr != nil || existing.ContentID != contentID ||
			!reviewRelationMatchesCommand(existing, command) ||
			!reviewDocumentMatchesCommand(document, command, canonicalURL, canonicalPhotos, canonicalLinks) {
			return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceConflict
		}
		matches, matchErr := writer.reviewReplacementReplayMatches(ctx, tx, command, existing.ID)
		if matchErr != nil {
			return resource.ReviewResourceReceipt{}, matchErr
		}
		if !matches {
			return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceConflict
		}
		publication, publicationErr := repository.GetPublicationByRelation(
			ctx, writer.tenantID, existing.ID,
		)
		if publicationErr != nil {
			return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceConflict
		}
		if err := tx.Commit(); err != nil {
			return resource.ReviewResourceReceipt{}, fmt.Errorf("commit review resource replay: %w", err)
		}
		committed = true
		return reviewResourceReceipt(existing, publication, document.Title), nil
	} else if !errors.Is(readErr, ErrRelationNotFound) {
		return resource.ReviewResourceReceipt{}, mapReviewResourceWriteError(
			fmt.Errorf("read review resource idempotency receipt: %w", readErr),
		)
	}
	previousPhotos, err := writer.lockReviewReplacement(ctx, tx, command, canonicalPhotos)
	if err != nil {
		return resource.ReviewResourceReceipt{}, err
	}
	if err := lockReviewTarget(ctx, tx, writer.tenantID, command); err != nil {
		return resource.ReviewResourceReceipt{}, err
	}
	if mediaVerifyErr != nil {
		return resource.ReviewResourceReceipt{}, mediaVerifyErr
	}
	if err := pinReviewFiles(ctx, tx, writer.tenantID, command); err != nil {
		return resource.ReviewResourceReceipt{}, err
	}

	contentID := uuid.NewSHA1(
		uuid.NameSpaceURL,
		[]byte("wq-xiangwan/review/"+writer.tenantID.String()+"/"+idempotencyKey),
	)
	blocks := reviewBlocksForCommand(contentID, command, canonicalURL, canonicalPhotos, canonicalLinks)
	contentWriter, err := contentstandalonepg.NewWriter(tx)
	if err != nil {
		return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceUnavailable
	}
	document, err := contentWriter.CreateReview(ctx, contentstandalonepg.CreateReviewInput{
		ID:          contentID,
		TenantID:    writer.tenantID,
		PrincipalID: command.ActorID,
		Title:       strings.TrimSpace(command.Title),
		Status:      "active",
		CreatedAt:   now,
		Blocks:      blocks,
	})
	if err != nil {
		return resource.ReviewResourceReceipt{}, fmt.Errorf("create review Content: %w", err)
	}

	kind := resource.RelationKindInstanceReview
	accessPolicy := resource.AccessPolicyPublic
	if command.SessionID != nil {
		kind = resource.RelationKindSessionResources
	}
	var seriesID uuid.UUID
	if err := tx.QueryRowContext(ctx, `
SELECT series_id
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
`, writer.tenantID, command.InstanceID).Scan(&seriesID); err != nil {
		return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceConflict
	}
	relation, err := resource.NewDraft(resource.CreateDraftCommand{
		TenantID:              writer.tenantID,
		SeriesID:              seriesID,
		InstanceID:            command.InstanceID,
		SessionID:             command.SessionID,
		Kind:                  kind,
		ContentID:             document.ID,
		ContentRevision:       document.UpdatedAt,
		AccessPolicy:          accessPolicy,
		SortOrder:             command.SortOrder,
		ExpectedTargetVersion: command.ExpectedTargetVersion,
		CreatedBy:             command.ActorID,
		IdempotencyKey:        idempotencyKey,
		CreatedAt:             now,
	})
	if err != nil {
		return resource.ReviewResourceReceipt{}, err
	}
	if _, err := repository.CreateDraft(ctx, relation); err != nil {
		return resource.ReviewResourceReceipt{}, mapReviewResourceWriteError(
			fmt.Errorf("create review resource relation: %w", err),
		)
	}
	snapshot, err := repository.GetContentSnapshotByRelation(ctx, writer.tenantID, relation.ID)
	if err != nil {
		return resource.ReviewResourceReceipt{}, mapReviewResourceWriteError(
			fmt.Errorf("read review content snapshot: %w", err),
		)
	}
	payloadDigest := reviewPayloadDigest(command, canonicalURL, canonicalPhotos, canonicalLinks)
	actorID := command.ActorID
	observation, err := resource.NewModerationObservation(resource.RecordModerationCommand{
		TenantID:          writer.tenantID,
		RelationID:        relation.ID,
		ContentID:         relation.ContentID,
		ContentRevision:   relation.ContentRevision,
		Provider:          reviewWriterProvider,
		ProviderReference: "operation:" + idempotencyKey,
		PolicyVersion:     writer.policyVersion,
		Source:            resource.ModerationSourceManualReview,
		Decision:          resource.ModerationDecisionApproved,
		SubjectDigest:     snapshot.SubjectDigest[:],
		PayloadDigest:     payloadDigest[:],
		ActorID:           &actorID,
		Reason:            stringPointer(reviewWriterReason),
		ObservedAt:        now,
		RecordedAt:        now,
	})
	if err != nil {
		return resource.ReviewResourceReceipt{}, fmt.Errorf("build review moderation evidence: %w", err)
	}
	moderationResult, err := repository.RecordModeration(ctx, observation)
	if err != nil {
		return resource.ReviewResourceReceipt{}, mapReviewResourceWriteError(
			fmt.Errorf("record review moderation evidence: %w", err),
		)
	}
	publication, err := resource.NewPublication(resource.PublishCommand{
		TenantID:              writer.tenantID,
		RelationID:            relation.ID,
		ContentID:             relation.ContentID,
		ContentRevision:       relation.ContentRevision,
		ApprovalObservationID: moderationResult.Observation.ID,
		AccessPolicy:          accessPolicy,
		ExpectedTargetVersion: command.ExpectedTargetVersion,
		PublishedBy:           command.ActorID,
		IdempotencyKey:        idempotencyKey,
		PublishedAt:           now,
	})
	if err != nil {
		return resource.ReviewResourceReceipt{}, fmt.Errorf("build review publication: %w", err)
	}
	publicationResult, err := repository.Publish(ctx, publication)
	if err != nil {
		return resource.ReviewResourceReceipt{}, mapReviewResourceWriteError(
			fmt.Errorf("publish review resource: %w", err),
		)
	}
	if err := writer.recordReviewReplacement(ctx, tx, command, relation, previousPhotos, now); err != nil {
		return resource.ReviewResourceReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return resource.ReviewResourceReceipt{}, fmt.Errorf("commit review resource: %w", err)
	}
	committed = true
	return reviewResourceReceipt(relation, publicationResult.Publication, command.Title), nil
}

func validateReviewResourceCommand(command resource.CreateReviewResourceCommand) error {
	if command.ActorID == uuid.Nil || command.InstanceID == uuid.Nil ||
		command.IdentityLinkID == uuid.Nil ||
		command.OperationID == uuid.Nil || command.ExpectedTargetVersion < 1 ||
		command.SortOrder < 0 || command.SortOrder > resource.MaxResourceSortOrder ||
		strings.TrimSpace(command.Title) != command.Title ||
		strings.TrimSpace(command.Description) != command.Description ||
		strings.TrimSpace(command.VideoURL) != command.VideoURL ||
		strings.TrimSpace(command.RequestID) != command.RequestID ||
		command.Title == "" ||
		command.RequestID == "" || len(command.RequestID) > 128 ||
		strings.ContainsAny(command.RequestID, "\r\n\x00") ||
		len([]rune(command.Title)) > maxReviewTitleRunes ||
		len([]rune(command.Description)) > maxReviewDescriptionRunes {
		return resource.ErrInvalidReviewResourceCommand
	}
	if (command.ReplacesRelationID == nil) != (command.ExpectedPhotoCurationVersion == nil) ||
		(command.ReplacesRelationID != nil && (*command.ReplacesRelationID == uuid.Nil || *command.ExpectedPhotoCurationVersion < 0)) {
		return resource.ErrInvalidReviewResourceCommand
	}
	if command.VideoChannel != nil && (!resource.ValidReviewVideoChannel(*command.VideoChannel) || command.VideoURL != "") {
		return resource.ErrInvalidReviewResourceCommand
	}
	if command.SessionID != nil && *command.SessionID == uuid.Nil {
		return resource.ErrInvalidReviewResourceCommand
	}
	if len(command.Photos) > maxReviewPhotos || len(command.Links) > maxReviewLinks ||
		len(command.Files) > maxReviewFiles {
		return resource.ErrInvalidReviewResourceCommand
	}
	// A description alone is not a published resource.  The operator must
	// provide at least one concrete media/link item; video is optional when a
	// photo, recording, or materials link is supplied.
	if command.VideoURL == "" && command.VideoChannel == nil && len(command.Photos) == 0 &&
		len(command.Links) == 0 && len(command.Files) == 0 {
		return resource.ErrInvalidReviewResourceCommand
	}
	seenFiles := make(map[uuid.UUID]struct{}, len(command.Files))
	fileKinds := make(map[string]int, 4)
	for _, file := range command.Files {
		if !file.Reviewed || storagestand.ReviewObjectKey(file.FileID) == "" ||
			!validReviewResourceSHA(file.SHA256) {
			return resource.ErrInvalidReviewResourceCommand
		}
		if _, duplicate := seenFiles[file.FileID]; duplicate {
			return resource.ErrInvalidReviewResourceCommand
		}
		seenFiles[file.FileID] = struct{}{}
		fileKinds[file.Kind]++
		if file.Kind != resource.ReviewResourceFilePhoto &&
			file.Kind != resource.ReviewResourceFileVideo &&
			file.Kind != resource.ReviewResourceFileAudio &&
			file.Kind != resource.ReviewResourceFileMaterial {
			return resource.ErrInvalidReviewResourceCommand
		}
	}
	if len(command.Photos)+fileKinds[resource.ReviewResourceFilePhoto] > maxReviewPhotos ||
		fileKinds[resource.ReviewResourceFileVideo] > 1 ||
		fileKinds[resource.ReviewResourceFileAudio] > 1 ||
		fileKinds[resource.ReviewResourceFileMaterial] > 1 {
		return resource.ErrInvalidReviewResourceCommand
	}
	seenLinks := make(map[string]struct{}, len(command.Links))
	for _, photo := range command.Photos {
		if strings.TrimSpace(photo) != photo || photo == "" || len(photo) > 4096 {
			return resource.ErrInvalidReviewResourceCommand
		}
	}
	for _, link := range command.Links {
		if link.Kind != resource.ReviewResourceLinkRecording &&
			link.Kind != resource.ReviewResourceLinkMaterials ||
			strings.TrimSpace(link.Title) != link.Title ||
			strings.TrimSpace(link.Subtitle) != link.Subtitle ||
			strings.TrimSpace(link.URL) != link.URL ||
			link.Title == "" || len(link.URL) > 4096 ||
			len([]rune(link.Title)) > maxReviewLinkTitleRunes ||
			len([]rune(link.Subtitle)) > maxReviewLinkSubtitleRunes {
			return resource.ErrInvalidReviewResourceCommand
		}
		if _, exists := seenLinks[link.Kind]; exists {
			return resource.ErrInvalidReviewResourceCommand
		}
		seenLinks[link.Kind] = struct{}{}
	}
	return nil
}

func validReviewResourceSHA(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, digit := range value {
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
			return false
		}
	}
	return true
}

func canonicalReviewExtras(
	policy resource.ExternalDomainPolicy,
	photos []string,
	links []resource.ReviewResourceLink,
) ([]string, []resource.ReviewResourceLink, error) {
	canonicalPhotos := make([]string, 0, len(photos))
	for _, photo := range photos {
		canonical, allowed := policy.AllowURL(photo)
		if !allowed {
			return nil, nil, resource.ErrReviewResourceExternalLink
		}
		canonicalPhotos = append(canonicalPhotos, canonical)
	}
	canonicalLinks := make([]resource.ReviewResourceLink, 0, len(links))
	for _, link := range links {
		// A configured card may intentionally have no destination yet. It still
		// follows the same immutable Content/review chain; the public projection
		// exposes it as unavailable, never as an actionable unvalidated URL.
		if link.URL != "" {
			canonical, allowed := policy.AllowURL(link.URL)
			if !allowed {
				return nil, nil, resource.ErrReviewResourceExternalLink
			}
			link.URL = canonical
		}
		canonicalLinks = append(canonicalLinks, link)
	}
	return canonicalPhotos, canonicalLinks, nil
}

// lockReviewOperation serializes retries carrying the same operation key
// before the deterministic Content ID is inserted.  Without this xact lock,
// two concurrent first attempts could both miss the relation receipt and one
// would surface a raw Content unique-key error instead of replaying the
// durable receipt.
func lockReviewOperation(
	ctx context.Context,
	tx *sql.Tx,
	tenantID uuid.UUID,
	command resource.CreateReviewResourceCommand,
) error {
	if tx == nil || tenantID == uuid.Nil || command.ActorID == uuid.Nil ||
		command.OperationID == uuid.Nil {
		return resource.ErrReviewResourceUnavailable
	}
	lockKey := tenantID.String() + ":" + command.ActorID.String() + ":" +
		command.OperationID.String()
	if _, err := tx.ExecContext(ctx, `
SELECT pg_advisory_xact_lock(hashtextextended($1, 0))
`, lockKey); err != nil {
		return fmt.Errorf("acquire review resource operation lock: %w", err)
	}
	return nil
}

// lockReviewTarget reproduces the target predicates enforced by the
// ResourceRelation publication triggers before creating Content.  Locking in
// the same Instance-then-Session order as the triggers makes the target
// version and lifecycle decision stable through the final publication insert.
func lockReviewTarget(
	ctx context.Context,
	tx *sql.Tx,
	tenantID uuid.UUID,
	command resource.CreateReviewResourceCommand,
) error {
	if tx == nil || tenantID == uuid.Nil {
		return resource.ErrReviewResourceUnavailable
	}
	var instanceStatus string
	var instanceVersion int64
	err := tx.QueryRowContext(ctx, `
SELECT status, version
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, command.InstanceID).Scan(&instanceStatus, &instanceVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.ErrReviewResourceConflict
	}
	if err != nil {
		return fmt.Errorf("lock review resource Instance: %w", err)
	}
	if command.SessionID == nil {
		if !validInstanceReviewTarget(
			instanceStatus, instanceVersion, command.ExpectedTargetVersion,
		) {
			return resource.ErrReviewResourceConflict
		}
		return nil
	}
	if !validSessionReviewInstanceTarget(instanceStatus) {
		return resource.ErrReviewResourceConflict
	}
	var sessionInstanceID uuid.UUID
	var sessionStatus string
	var sessionVersion int64
	err = tx.QueryRowContext(ctx, `
SELECT instance_id, status, version
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
	`, tenantID, *command.SessionID).Scan(
		&sessionInstanceID, &sessionStatus, &sessionVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.ErrReviewResourceConflict
	}
	if err != nil {
		return fmt.Errorf("lock review resource Session: %w", err)
	}
	if sessionInstanceID != command.InstanceID || !validSessionReviewTarget(
		sessionStatus, sessionVersion, command.ExpectedTargetVersion,
	) {
		return resource.ErrReviewResourceConflict
	}
	return nil
}

func validInstanceReviewTarget(status string, version, expectedVersion int64) bool {
	return (status == "completed" || status == "archived") &&
		version == expectedVersion
}

func validSessionReviewInstanceTarget(status string) bool {
	return status == "published" || status == "completed" || status == "archived"
}

func validSessionReviewTarget(status string, version, expectedVersion int64) bool {
	return (status == "published" || status == "ended" || status == "archived") &&
		version == expectedVersion
}

// mapReviewResourceWriteError keeps trigger/repository facts at the public
// review command's conflict boundary.  Those errors are expected stale-target
// or duplicate-publication outcomes and must not become a generic 500.
func mapReviewResourceWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, resource.ErrReviewResourceConflict) {
		return err
	}
	for _, candidate := range []error{
		ErrRelationFactsConflict,
		ErrRelationTargetChanged,
		ErrRelationExists,
		ErrContentSnapshotNotFound,
		ErrContentSnapshotFactsConflict,
		ErrModerationNotFound,
		ErrModerationExists,
		ErrModerationFactsConflict,
		ErrPublicationNotFound,
		ErrPublicationExists,
		ErrPublicationFactsConflict,
		ErrPublicationStateChanged,
	} {
		if errors.Is(err, candidate) {
			return errors.Join(resource.ErrReviewResourceConflict, err)
		}
	}
	return err
}

func reviewBlocksForCommand(contentID uuid.UUID, command resource.CreateReviewResourceCommand, videoURL string, photos []string, links []resource.ReviewResourceLink) []contentstandalonepg.ReviewBlockInput {
	blocks := reviewBlocksWithExtras(contentID, command.Description, videoURL, photos, links, command.Files...)
	if command.VideoChannel != nil {
		data, _ := json.Marshal(map[string]string{"kind": "video_channel_native", "label": "本期视频回顾", "finder_user_name": command.VideoChannel.FinderUserName, "feed_id": command.VideoChannel.FeedID})
		blocks = append(blocks, contentstandalonepg.ReviewBlockInput{ID: uuid.NewSHA1(contentID, []byte("video-channel-native")), Type: "link", SortOrder: len(blocks), Data: data})
	}
	return blocks
}

func reviewBlocks(
	contentID uuid.UUID,
	description string,
	videoURL string,
) []contentstandalonepg.ReviewBlockInput {
	return reviewBlocksWithExtras(contentID, description, videoURL, nil, nil)
}

func reviewBlocksWithExtras(
	contentID uuid.UUID,
	description string,
	videoURL string,
	photos []string,
	links []resource.ReviewResourceLink,
	files ...resource.ReviewResourceFile,
) []contentstandalonepg.ReviewBlockInput {
	blocks := make([]contentstandalonepg.ReviewBlockInput, 0,
		3+len(photos)+len(links)+len(files))
	if strings.TrimSpace(description) != "" {
		data, _ := json.Marshal(map[string]string{"text": description})
		blocks = append(blocks, contentstandalonepg.ReviewBlockInput{
			ID:        uuid.NewSHA1(contentID, []byte("description")),
			Type:      "text",
			SortOrder: len(blocks),
			Data:      data,
		})
	}
	for index, photoURL := range photos {
		data, _ := json.Marshal(map[string]string{
			"url":   photoURL,
			"label": "活动照片",
			"kind":  "photo",
		})
		blocks = append(blocks, contentstandalonepg.ReviewBlockInput{
			ID:        uuid.NewSHA1(contentID, []byte(fmt.Sprintf("photo-%d", index))),
			Type:      "image",
			SortOrder: len(blocks),
			Data:      data,
		})
	}
	if strings.TrimSpace(videoURL) != "" {
		data, _ := json.Marshal(map[string]string{
			"url":   videoURL,
			"label": "打开视频号",
			"kind":  "video_channel",
		})
		blocks = append(blocks, contentstandalonepg.ReviewBlockInput{
			ID:        uuid.NewSHA1(contentID, []byte("video-channel")),
			Type:      "link",
			SortOrder: len(blocks),
			Data:      data,
		})
	}
	for _, link := range links {
		fields := map[string]string{
			"url":      link.URL,
			"label":    link.Title,
			"subtitle": link.Subtitle,
			"kind":     link.Kind,
		}
		if link.Hidden {
			fields["enabled"] = "false"
		}
		data, _ := json.Marshal(fields)
		blocks = append(blocks, contentstandalonepg.ReviewBlockInput{
			ID:        uuid.NewSHA1(contentID, []byte("resource-"+link.Kind)),
			Type:      "link",
			SortOrder: len(blocks),
			Data:      data,
		})
	}
	for _, file := range files {
		blockType, label := reviewFileBlockPresentation(file.Kind)
		data, _ := json.Marshal(map[string]string{
			"file_id": file.FileID.String(),
			"label":   label,
			"kind":    file.Kind,
			"sha256":  file.SHA256,
		})
		blocks = append(blocks, contentstandalonepg.ReviewBlockInput{
			ID:        uuid.NewSHA1(contentID, []byte("file-"+file.FileID.String())),
			Type:      blockType,
			SortOrder: len(blocks),
			Data:      data,
		})
	}
	return blocks
}

func reviewFileBlockPresentation(kind string) (string, string) {
	switch kind {
	case resource.ReviewResourceFilePhoto:
		return "image", "活动照片"
	case resource.ReviewResourceFileVideo:
		return "video", "本期视频回顾"
	case resource.ReviewResourceFileAudio:
		return "audio", "录音梳理"
	case resource.ReviewResourceFileMaterial:
		return "file", "活动资料"
	default:
		return "", ""
	}
}

func reviewRelationMatchesCommand(
	relation resource.Relation,
	command resource.CreateReviewResourceCommand,
) bool {
	wantKind := resource.RelationKindInstanceReview
	if command.SessionID != nil {
		wantKind = resource.RelationKindSessionResources
	}
	return relation.InstanceID == command.InstanceID &&
		equalReviewUUID(relation.SessionID, command.SessionID) &&
		relation.Kind == wantKind &&
		relation.AccessPolicy == resource.AccessPolicyPublic &&
		relation.SortOrder == command.SortOrder &&
		relation.ExpectedTargetVersion == command.ExpectedTargetVersion
}

func reviewDocumentMatchesCommand(
	document contentstandalonepg.ReviewDocument,
	command resource.CreateReviewResourceCommand,
	canonicalURL string,
	canonicalPhotos []string,
	canonicalLinks []resource.ReviewResourceLink,
) bool {
	if document.Title != command.Title || len(document.Blocks) != len(reviewBlocksForCommand(document.ID, command, canonicalURL, canonicalPhotos, canonicalLinks)) {
		return false
	}
	want := reviewBlocksForCommand(document.ID, command, canonicalURL, canonicalPhotos, canonicalLinks)
	for index, block := range want {
		got := document.Blocks[index]
		if got.ID != block.ID || got.Type != block.Type || got.SortOrder != block.SortOrder ||
			!sameReviewBlockData(got.Data, block.Data) {
			return false
		}
	}
	return true
}

// PostgreSQL jsonb may reorder object keys and whitespace. Compare the
// immutable block facts, not their serialized JSON representation, on replay.
func sameReviewBlockData(stored, expected []byte) bool {
	var storedFields, expectedFields map[string]string
	if json.Unmarshal(stored, &storedFields) != nil ||
		json.Unmarshal(expected, &expectedFields) != nil ||
		storedFields == nil || expectedFields == nil ||
		len(storedFields) != len(expectedFields) {
		return false
	}
	for key, value := range expectedFields {
		if storedValue, exists := storedFields[key]; !exists || storedValue != value {
			return false
		}
	}
	return true
}

func equalReviewUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func reviewPayloadDigest(
	command resource.CreateReviewResourceCommand,
	canonicalURL string,
	canonicalPhotos []string,
	canonicalLinks []resource.ReviewResourceLink,
) [resource.DigestSize]byte {
	payload, _ := json.Marshal(struct {
		InstanceID            uuid.UUID                     `json:"instance_id"`
		SessionID             *uuid.UUID                    `json:"session_id,omitempty"`
		ExpectedTargetVersion int64                         `json:"expected_target_version"`
		Title                 string                        `json:"title"`
		Description           string                        `json:"description"`
		VideoURL              string                        `json:"video_url"`
		VideoChannel          *resource.ReviewVideoChannel  `json:"video_channel,omitempty"`
		Photos                []string                      `json:"photos,omitempty"`
		Links                 []resource.ReviewResourceLink `json:"links,omitempty"`
		Files                 []resource.ReviewResourceFile `json:"files,omitempty"`
		SortOrder             int                           `json:"sort_order"`
	}{
		InstanceID: command.InstanceID, SessionID: command.SessionID,
		ExpectedTargetVersion: command.ExpectedTargetVersion,
		Title:                 command.Title, Description: command.Description,
		VideoURL: canonicalURL, Photos: canonicalPhotos, Links: canonicalLinks,
		VideoChannel: command.VideoChannel,
		Files:        command.Files,
		SortOrder:    command.SortOrder,
	})
	return sha256.Sum256(payload)
}

func reviewResourceReceipt(
	relation resource.Relation,
	publication resource.Publication,
	title string,
) resource.ReviewResourceReceipt {
	return resource.ReviewResourceReceipt{
		RelationID:    relation.ID,
		PublicationID: publication.ID,
		ContentID:     relation.ContentID,
		TenantID:      relation.TenantID,
		InstanceID:    relation.InstanceID,
		SessionID:     relation.SessionID,
		Title:         title,
		PublishedAt:   publication.PublishedAt,
	}
}

func stringPointer(value string) *string { return &value }
