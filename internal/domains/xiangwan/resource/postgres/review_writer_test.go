package resourcepostgres

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	contentstandalonepg "github.com/wzyhn/xiangwanai/internal/capabilities/content/standalonepg"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestValidateReviewResourceCommand(t *testing.T) {
	t.Parallel()

	base := resource.CreateReviewResourceCommand{
		ActorID:               uuid.New(),
		IdentityLinkID:        uuid.New(),
		InstanceID:            uuid.New(),
		ExpectedTargetVersion: 3,
		Title:                 "本期视频回顾",
		Description:           "活动内容摘要",
		VideoURL:              "https://video.example.com/channel/1",
		SortOrder:             2,
		OperationID:           uuid.New(),
		RequestID:             "request-review-test",
	}
	if err := validateReviewResourceCommand(base); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*resource.CreateReviewResourceCommand)
	}{
		{name: "missing actor", mutate: func(value *resource.CreateReviewResourceCommand) { value.ActorID = uuid.Nil }},
		{name: "missing identity link", mutate: func(value *resource.CreateReviewResourceCommand) { value.IdentityLinkID = uuid.Nil }},
		{name: "missing target version", mutate: func(value *resource.CreateReviewResourceCommand) { value.ExpectedTargetVersion = 0 }},
		{name: "untrimmed title", mutate: func(value *resource.CreateReviewResourceCommand) { value.Title = " 本期视频回顾" }},
		{name: "empty video URL without another resource", mutate: func(value *resource.CreateReviewResourceCommand) { value.VideoURL = "" }},
		{name: "negative sort order", mutate: func(value *resource.CreateReviewResourceCommand) { value.SortOrder = -1 }},
		{name: "nil session", mutate: func(value *resource.CreateReviewResourceCommand) { id := uuid.Nil; value.SessionID = &id }},
		{name: "missing request id", mutate: func(value *resource.CreateReviewResourceCommand) { value.RequestID = "" }},
		{name: "title too long", mutate: func(value *resource.CreateReviewResourceCommand) {
			value.Title = string(make([]rune, maxReviewTitleRunes+1))
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			if !errors.Is(validateReviewResourceCommand(value), resource.ErrInvalidReviewResourceCommand) {
				t.Fatalf("validateReviewResourceCommand(%s) did not reject input", test.name)
			}
		})
	}
	withPhoto := base
	withPhoto.VideoURL = ""
	withPhoto.Photos = []string{"https://media.example.com/photo.webp"}
	if err := validateReviewResourceCommand(withPhoto); err != nil {
		t.Fatalf("photo-only review should allow an empty video URL: %v", err)
	}
	withLink := base
	withLink.VideoURL = ""
	withLink.Links = []resource.ReviewResourceLink{{
		Kind: resource.ReviewResourceLinkMaterials, Title: "资料",
		URL: "https://feishu.example.com/materials",
	}}
	if err := validateReviewResourceCommand(withLink); err != nil {
		t.Fatalf("link-only review should allow an empty video URL: %v", err)
	}
}

func TestReviewReplayComparesJSONBFieldsWithoutAcceptingDifferentKeys(t *testing.T) {
	if !sameReviewBlockData([]byte(`{"label": "活动照片", "url": "https://cdn.example/photo.png"}`),
		[]byte(`{"url":"https://cdn.example/photo.png","label":"活动照片"}`)) {
		t.Fatal("JSONB object key reordering changed an exact review replay")
	}
	if sameReviewBlockData([]byte(`{"subtitle":""}`), []byte(`{"label":""}`)) {
		t.Fatal("review replay accepted a different empty-valued field")
	}
}

func TestReviewResourceFileSelectionRejectsDuplicatesAndUnboundFacts(t *testing.T) {
	t.Parallel()
	fileID := uuid.New()
	base := resource.CreateReviewResourceCommand{
		ActorID: uuid.New(), IdentityLinkID: uuid.New(), InstanceID: uuid.New(),
		ExpectedTargetVersion: 1, Title: "活动资料", SortOrder: 0,
		OperationID: uuid.New(), RequestID: "file-review-test",
		Files: []resource.ReviewResourceFile{{
			FileID: fileID, Kind: resource.ReviewResourceFileMaterial,
			SHA256: strings.Repeat("a", 64), Reviewed: true,
		}},
	}
	if err := validateReviewResourceCommand(base); err != nil {
		t.Fatalf("file-only review rejected: %v", err)
	}
	for _, mutate := range []func(*resource.CreateReviewResourceCommand){
		func(v *resource.CreateReviewResourceCommand) { v.Files[0].FileID = uuid.Nil },
		func(v *resource.CreateReviewResourceCommand) { v.Files[0].Kind = "arbitrary" },
		func(v *resource.CreateReviewResourceCommand) { v.Files[0].SHA256 = strings.Repeat("A", 64) },
		func(v *resource.CreateReviewResourceCommand) { v.Files[0].Reviewed = false },
		func(v *resource.CreateReviewResourceCommand) { v.Files = append(v.Files, v.Files[0]) },
		func(v *resource.CreateReviewResourceCommand) {
			v.Files = append(v.Files, resource.ReviewResourceFile{
				FileID: uuid.New(), Kind: resource.ReviewResourceFileMaterial,
				SHA256:   strings.Repeat("b", 64),
				Reviewed: true,
			})
		},
	} {
		value := base
		value.Files = append([]resource.ReviewResourceFile(nil), base.Files...)
		mutate(&value)
		if !errors.Is(validateReviewResourceCommand(value),
			resource.ErrInvalidReviewResourceCommand) {
			t.Fatalf("invalid File selection accepted: %+v", value.Files)
		}
	}
}

func TestReviewFileBlocksBindExactIDAndChecksumInContentSnapshot(t *testing.T) {
	t.Parallel()
	contentID, fileID := uuid.New(), uuid.New()
	selected := resource.ReviewResourceFile{
		FileID: fileID, Kind: resource.ReviewResourceFileMaterial,
		SHA256: strings.Repeat("a", 64), Reviewed: true,
	}
	blocks := reviewBlocksWithExtras(contentID, "", "", nil, nil, selected)
	if len(blocks) != 1 || blocks[0].Type != "file" {
		t.Fatalf("file review blocks = %+v", blocks)
	}
	var data map[string]string
	if err := json.Unmarshal(blocks[0].Data, &data); err != nil ||
		data["file_id"] != fileID.String() || data["sha256"] != selected.SHA256 ||
		data["label"] != "活动资料" {
		t.Fatalf("file review block = %s, %v", blocks[0].Data, err)
	}
	command := resource.CreateReviewResourceCommand{InstanceID: uuid.New(),
		ExpectedTargetVersion: 1, Title: "活动资料", Files: []resource.ReviewResourceFile{selected}}
	first := reviewPayloadDigest(command, "", nil, nil)
	command.Files[0].SHA256 = strings.Repeat("b", 64)
	if first == reviewPayloadDigest(command, "", nil, nil) {
		t.Fatal("review digest did not bind verified File checksum")
	}
}

func TestReviewTargetPredicatesRejectStaleOrUnfinishedFacts(t *testing.T) {
	t.Parallel()

	instanceCases := []struct {
		status   string
		version  int64
		expected bool
	}{
		{status: "completed", version: 3, expected: true},
		{status: "archived", version: 3, expected: true},
		{status: "published", version: 3, expected: false},
		{status: "completed", version: 2, expected: false},
	}
	for _, test := range instanceCases {
		if got := validInstanceReviewTarget(test.status, test.version, 3); got != test.expected {
			t.Errorf("validInstanceReviewTarget(%q, %d) = %v, want %v", test.status, test.version, got, test.expected)
		}
	}
	if validSessionReviewInstanceTarget("draft") || validSessionReviewInstanceTarget("cancelled") {
		t.Fatal("session resources accepted a non-public Instance status")
	}
	for _, test := range []struct {
		status   string
		version  int64
		expected bool
	}{
		{status: "published", version: 4, expected: true},
		{status: "ended", version: 4, expected: true},
		{status: "archived", version: 4, expected: true},
		{status: "cancelled", version: 4, expected: false},
		{status: "ended", version: 3, expected: false},
	} {
		if got := validSessionReviewTarget(test.status, test.version, 4); got != test.expected {
			t.Errorf("validSessionReviewTarget(%q, %d) = %v, want %v", test.status, test.version, got, test.expected)
		}
	}
}

func TestMapReviewResourceWriteErrorKeepsClientConflictBoundary(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		ErrRelationFactsConflict,
		ErrRelationTargetChanged,
		ErrPublicationFactsConflict,
		ErrPublicationStateChanged,
		ErrModerationFactsConflict,
	} {
		if !errors.Is(mapReviewResourceWriteError(err), resource.ErrReviewResourceConflict) {
			t.Errorf("mapReviewResourceWriteError(%v) did not map to review conflict", err)
		}
	}
	serverError := errors.New("database unavailable")
	if errors.Is(mapReviewResourceWriteError(serverError), resource.ErrReviewResourceConflict) {
		t.Fatal("unexpectedly mapped an unrelated server error to a target conflict")
	}
}

func TestReviewBlocksExposeDescriptionAndVideoChannelLink(t *testing.T) {
	t.Parallel()

	contentID := uuid.New()
	blocks := reviewBlocks(contentID, "活动内容摘要", "https://video.example.com/channel/1")
	if len(blocks) != 2 || blocks[0].Type != "text" || blocks[1].Type != "link" {
		t.Fatalf("reviewBlocks() = %#v", blocks)
	}
	var description map[string]string
	if err := json.Unmarshal(blocks[0].Data, &description); err != nil || description["text"] != "活动内容摘要" {
		t.Fatalf("description block = %s, %v", blocks[0].Data, err)
	}
	var link map[string]string
	if err := json.Unmarshal(blocks[1].Data, &link); err != nil ||
		link["url"] != "https://video.example.com/channel/1" ||
		link["kind"] != "video_channel" || link["label"] != "打开视频号" {
		t.Fatalf("video-channel block = %s, %v", blocks[1].Data, err)
	}

	withoutDescription := reviewBlocks(contentID, "", "https://video.example.com/channel/1")
	if len(withoutDescription) != 1 || withoutDescription[0].SortOrder != 0 {
		t.Fatalf("reviewBlocks(empty description) = %#v", withoutDescription)
	}
	withoutVideo := reviewBlocksWithExtras(contentID, "活动内容摘要", "", []string{"https://media.example.com/photo.webp"}, nil)
	if len(withoutVideo) != 2 || withoutVideo[1].Type != "image" {
		t.Fatalf("reviewBlocksWithExtras(no video) = %#v", withoutVideo)
	}
}

func TestReviewBlocksExposePhotosAndTypedFeishuResources(t *testing.T) {
	t.Parallel()

	contentID := uuid.New()
	photos := []string{"https://media.example.com/photo-1.webp", "https://media.example.com/photo-2.webp"}
	links := []resource.ReviewResourceLink{
		{Kind: resource.ReviewResourceLinkRecording, Title: "讨论纪要", Subtitle: "核心观点", URL: "https://feishu.example.com/recording"},
		{Kind: resource.ReviewResourceLinkMaterials, Title: "资料合集", Subtitle: "现场分享", URL: "https://feishu.example.com/materials"},
	}
	blocks := reviewBlocksWithExtras(contentID, "摘要", "https://video.example.com/channel/1", photos, links)
	if len(blocks) != 6 || blocks[1].Type != "image" || blocks[2].Type != "image" || blocks[4].Type != "link" || blocks[5].Type != "link" {
		t.Fatalf("reviewBlocksWithExtras() = %#v", blocks)
	}
	var photo map[string]string
	if err := json.Unmarshal(blocks[1].Data, &photo); err != nil || photo["url"] != photos[0] || photo["kind"] != "photo" {
		t.Fatalf("photo block = %s, %v", blocks[1].Data, err)
	}
	var recording map[string]string
	if err := json.Unmarshal(blocks[4].Data, &recording); err != nil || recording["kind"] != resource.ReviewResourceLinkRecording || recording["subtitle"] != "核心观点" {
		t.Fatalf("recording block = %s, %v", blocks[4].Data, err)
	}
}

func TestCanonicalReviewExtrasRequiresConfiguredHTTPSAllowlist(t *testing.T) {
	t.Parallel()

	policy, err := resource.NewExternalDomainPolicy([]string{"media.example.com", "*.feishu.example.com"})
	if err != nil {
		t.Fatalf("NewExternalDomainPolicy() error = %v", err)
	}
	photos, links, err := canonicalReviewExtras(policy,
		[]string{"https://media.example.com/photo.webp"},
		[]resource.ReviewResourceLink{{Kind: resource.ReviewResourceLinkMaterials, Title: "资料", URL: "https://docs.feishu.example.com/materials"}},
	)
	if err != nil || len(photos) != 1 || len(links) != 1 || links[0].URL != "https://docs.feishu.example.com/materials" {
		t.Fatalf("canonicalReviewExtras() = %#v, %#v, %v", photos, links, err)
	}
	if _, _, err := canonicalReviewExtras(policy, []string{"https://evil.example.net/photo.webp"}, nil); !errors.Is(err, resource.ErrReviewResourceExternalLink) {
		t.Fatalf("unlisted photo error = %v", err)
	}
}

func TestReviewPayloadDigestIncludesCanonicalTargetAndURL(t *testing.T) {
	t.Parallel()

	command := resource.CreateReviewResourceCommand{
		InstanceID:            uuid.New(),
		ExpectedTargetVersion: 2,
		Title:                 "视频回顾",
		Description:           "摘要",
		VideoURL:              "https://video.example.com/raw",
		SortOrder:             0,
	}
	first := reviewPayloadDigest(command, "https://video.example.com/raw", nil, nil)
	second := reviewPayloadDigest(command, "https://video.example.com/raw?from=admin", nil, nil)
	if first == second {
		t.Fatal("reviewPayloadDigest must change when canonical external URL changes")
	}
	command.ExpectedTargetVersion++
	third := reviewPayloadDigest(command, "https://video.example.com/raw", nil, nil)
	if first == third {
		t.Fatal("reviewPayloadDigest must bind the target version")
	}
}

func TestNewReviewResourceWriterRejectsUnavailableDatabase(t *testing.T) {
	t.Parallel()

	_, err := NewReviewResourceWriter(nil, uuid.New(), resource.ExternalDomainPolicy{}, "")
	if !errors.Is(err, resource.ErrReviewResourceUnavailable) {
		t.Fatalf("NewReviewResourceWriter(nil) error = %v", err)
	}
	if _, err := NewReviewResourceWriter(nil, uuid.Nil, resource.ExternalDomainPolicy{}, ""); !errors.Is(err, resource.ErrReviewResourceUnavailable) {
		t.Fatalf("NewReviewResourceWriter(nil tenant) error = %v", err)
	}
}

func TestReviewResourceReceiptKeepsPublishedTimestamp(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC)
	relation := resource.Relation{
		ID: uuid.New(), TenantID: uuid.New(), SeriesID: uuid.New(), InstanceID: uuid.New(),
		Kind: resource.RelationKindInstanceReview, ContentID: uuid.New(),
		ContentRevision: publishedAt, AccessPolicy: resource.AccessPolicyPublic,
		ExpectedTargetVersion: 1, CreatedBy: uuid.New(), IdempotencyKey: "review-test",
		CreatedAt: publishedAt,
	}
	publication := resource.Publication{
		ID: uuid.New(), TenantID: relation.TenantID, RelationID: relation.ID,
		ContentID: relation.ContentID, ContentRevision: relation.ContentRevision,
		ApprovalObservationID: uuid.New(), AccessPolicy: resource.AccessPolicyPublic,
		ExpectedTargetVersion: 1, PublishedBy: relation.CreatedBy,
		IdempotencyKey: relation.IdempotencyKey, PublishedAt: publishedAt,
	}
	receipt := reviewResourceReceipt(relation, publication, "视频回顾")
	if receipt.RelationID != relation.ID || receipt.PublicationID != publication.ID ||
		receipt.ContentID != relation.ContentID || !receipt.PublishedAt.Equal(publishedAt) ||
		receipt.Title != "视频回顾" {
		t.Fatalf("reviewResourceReceipt() = %+v", receipt)
	}
}

func TestReviewReplayIntentChecksTargetAndImmutableBody(t *testing.T) {
	t.Parallel()

	instanceID := uuid.New()
	command := resource.CreateReviewResourceCommand{
		ActorID:               uuid.New(),
		IdentityLinkID:        uuid.New(),
		InstanceID:            instanceID,
		ExpectedTargetVersion: 4,
		Title:                 "本期视频回顾",
		Description:           "活动摘要",
		VideoURL:              "https://video.example.com/channel/1",
		SortOrder:             0,
		OperationID:           uuid.New(),
		RequestID:             "request-review-replay",
	}
	contentID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("replay"))
	relation := resource.Relation{
		InstanceID: instanceID, SessionID: nil,
		Kind: resource.RelationKindInstanceReview, ContentID: contentID,
		AccessPolicy: resource.AccessPolicyPublic, SortOrder: 0,
		ExpectedTargetVersion: 4,
	}
	if !reviewRelationMatchesCommand(relation, command) {
		t.Fatal("same target should be a replay match")
	}
	otherTarget := command
	otherTarget.InstanceID = uuid.New()
	if reviewRelationMatchesCommand(relation, otherTarget) {
		t.Fatal("changed target must be an idempotency conflict")
	}
	document := contentstandalonepg.ReviewDocument{
		ID: contentID, Title: command.Title,
		Blocks: make([]contentstandalonepg.ReviewBlock, 0, 2),
	}
	for _, block := range reviewBlocks(contentID, command.Description, command.VideoURL) {
		document.Blocks = append(document.Blocks, contentstandalonepg.ReviewBlock{
			ID: block.ID, Type: block.Type, SortOrder: block.SortOrder, Data: block.Data,
		})
	}
	if !reviewDocumentMatchesCommand(document, command, command.VideoURL, nil, nil) {
		t.Fatal("same immutable body should be a replay match")
	}
	changedBody := command
	changedBody.Description = "改过的摘要"
	if reviewDocumentMatchesCommand(document, changedBody, changedBody.VideoURL, nil, nil) {
		t.Fatal("changed immutable body must be an idempotency conflict")
	}
}
