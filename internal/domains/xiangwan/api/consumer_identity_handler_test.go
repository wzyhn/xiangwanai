package xiangwanapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/capabilities/storage/publicobject"
	identitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// fakeConsumerIdentityStore doubles as the handler's consumerIdentityWriter
// and the login handler's consumerIdentityReader.
type fakeConsumerIdentityStore struct {
	nickname      string
	avatarURL     string
	previousURL   string
	err           error
	readErr       error
	nicknameCalls int
	avatarCalls   int
	readCalls     int
	gotNickname   string
	gotAvatarURL  string
}

type fakeLegacyAvatarReader struct {
	object      *publicobject.Object
	err         error
	principalID uuid.UUID
	fileID      uuid.UUID
}

func (fake *fakeLegacyAvatarReader) Open(
	_ context.Context,
	principalID, fileID uuid.UUID,
) (*publicobject.Object, error) {
	fake.principalID = principalID
	fake.fileID = fileID
	return fake.object, fake.err
}

func (fake *fakeConsumerIdentityStore) UpdateNickname(
	_ context.Context,
	_ uuid.UUID,
	nickname string,
	_ string,
) (string, string, string, error) {
	fake.nicknameCalls++
	fake.gotNickname = nickname
	if fake.err != nil {
		return "", "", "", fake.err
	}
	fake.nickname = nickname
	return fake.nickname, fake.avatarURL, "etag-after-write", nil
}

type fakeNicknameModerator struct{ err error }

func (fake *fakeNicknameModerator) CheckNickname(
	context.Context, uuid.UUID, string,
) error {
	if fake == nil {
		return ErrNicknameModerationUnavailable
	}
	return fake.err
}

func (fake *fakeConsumerIdentityStore) UpdateAvatarURL(
	_ context.Context,
	_ uuid.UUID,
	avatarURL string,
) (string, error) {
	fake.avatarCalls++
	fake.gotAvatarURL = avatarURL
	if fake.err != nil {
		return "", fake.err
	}
	previous := fake.previousURL
	fake.previousURL = fake.avatarURL
	fake.avatarURL = avatarURL
	return previous, nil
}

func (fake *fakeConsumerIdentityStore) ReadConsumerIdentity(
	_ context.Context,
	_ uuid.UUID,
) (string, string, error) {
	fake.readCalls++
	if fake.readErr != nil {
		return "", "", fake.readErr
	}
	return fake.nickname, fake.avatarURL, nil
}

func newConsumerIdentityTestHandler(
	t *testing.T,
	writer *fakeConsumerIdentityStore,
) (*ConsumerIdentityHandler, string) {
	t.Helper()
	root := t.TempDir()
	store, err := NewLocalConsumerAvatarStore(root)
	if err != nil {
		t.Fatalf("NewLocalConsumerAvatarStore() error = %v", err)
	}
	return &ConsumerIdentityHandler{
		writer:            writer,
		avatars:           store,
		principal:         testConsumerPrincipalResolver,
		nicknameModerator: &fakeNicknameModerator{},
	}, root
}

func testConsumerPrincipalResolver(*gin.Context) (uuid.UUID, error) {
	return apiUUID(42), nil
}

func consumerNicknameContext(
	t *testing.T,
	body string,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(
		http.MethodPatch,
		"/me/profile/nickname",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	context.Request = request
	return context, recorder
}

func TestUpdateNicknameTrimsValidatesAndWrites(t *testing.T) {
	t.Parallel()

	writer := &fakeConsumerIdentityStore{avatarURL: "/api/v1/xiangwan/avatars/0123456789abcdef0123456789abcdef.png"}
	handler, _ := newConsumerIdentityTestHandler(t, writer)
	context, recorder := consumerNicknameContext(t, `{"nickname":"  享玩用户  ","principal_profile_etag":"etag-before"}`)
	handler.UpdateNickname(context)

	if recorder.Code != http.StatusOK || writer.nicknameCalls != 1 ||
		writer.gotNickname != "享玩用户" {
		t.Fatalf("UpdateNickname() status=%d calls=%d wrote=%q body=%s",
			recorder.Code, writer.nicknameCalls, writer.gotNickname, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"nickname":"享玩用户"`) ||
		!strings.Contains(recorder.Body.String(),
			`"avatar_url":"/api/v1/xiangwan/avatars/0123456789abcdef0123456789abcdef.png"`) {
		t.Fatalf("UpdateNickname() body=%s", recorder.Body.String())
	}
}

func TestUpdateNicknameBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		nickname   string
		wantStatus int
		wantWrite  bool
	}{
		{name: "one rune", nickname: "甲", wantStatus: http.StatusOK, wantWrite: true},
		{name: "64 runes", nickname: strings.Repeat("甲", 64), wantStatus: http.StatusOK, wantWrite: true},
		{name: "65 runes", nickname: strings.Repeat("甲", 65), wantStatus: http.StatusBadRequest},
		{name: "blank", nickname: "   ", wantStatus: http.StatusBadRequest},
		{name: "empty", nickname: "", wantStatus: http.StatusBadRequest},
		{name: "control character", nickname: "a\nb", wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writer := &fakeConsumerIdentityStore{}
			handler, _ := newConsumerIdentityTestHandler(t, writer)
			encoded, _ := json.Marshal(map[string]string{
				"nickname": test.nickname, "principal_profile_etag": "etag-before",
			})
			context, recorder := consumerNicknameContext(t, string(encoded))
			handler.UpdateNickname(context)
			if recorder.Code != test.wantStatus ||
				(writer.nicknameCalls == 1) != test.wantWrite {
				t.Fatalf("UpdateNickname(%q) status=%d calls=%d",
					test.nickname, recorder.Code, writer.nicknameCalls)
			}
		})
	}
}

func TestUpdateNicknameRejectsUnknownFieldsAndInactiveAccount(t *testing.T) {
	t.Parallel()

	writer := &fakeConsumerIdentityStore{}
	handler, _ := newConsumerIdentityTestHandler(t, writer)
	context, recorder := consumerNicknameContext(t, `{"nickname":"甲","principal_profile_etag":"etag-before","role":"admin"}`)
	handler.UpdateNickname(context)
	if recorder.Code != http.StatusBadRequest || writer.nicknameCalls != 0 {
		t.Fatalf("unknown field status=%d calls=%d", recorder.Code, writer.nicknameCalls)
	}

	writer.err = identitypostgres.ErrPrincipalUnavailable
	context, recorder = consumerNicknameContext(t, `{"nickname":"甲","principal_profile_etag":"etag-before"}`)
	handler.UpdateNickname(context)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("inactive account status=%d, want 403", recorder.Code)
	}
}

func TestUpdateNicknameFailsClosedWhenModerationRejectsOrIsUnavailable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "rejected", err: ErrNicknameRejected, wantStatus: http.StatusBadRequest},
		{name: "unavailable", err: ErrNicknameModerationUnavailable, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &fakeConsumerIdentityStore{}
			handler, _ := newConsumerIdentityTestHandler(t, writer)
			handler.nicknameModerator = &fakeNicknameModerator{err: tc.err}
			context, recorder := consumerNicknameContext(
				t, `{"nickname":"新昵称","principal_profile_etag":"etag-before"}`,
			)
			handler.UpdateNickname(context)
			if recorder.Code != tc.wantStatus || writer.nicknameCalls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, writer.nicknameCalls, recorder.Body.String())
			}
		})
	}
}

func TestUpdateNicknameMapsProfileETagConflict(t *testing.T) {
	t.Parallel()
	writer := &fakeConsumerIdentityStore{err: identitypostgres.ErrProfileETagConflict}
	handler, _ := newConsumerIdentityTestHandler(t, writer)
	context, recorder := consumerNicknameContext(
		t, `{"nickname":"新昵称","principal_profile_etag":"etag-before"}`,
	)
	handler.UpdateNickname(context)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s, want 409", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateNicknameRequiresConsumerSession(t *testing.T) {
	t.Parallel()

	handler, _ := newConsumerIdentityTestHandler(t, &fakeConsumerIdentityStore{})
	handler.principal = func(*gin.Context) (uuid.UUID, error) {
		return uuid.Nil, errx.NewUnauthorized("missing consumer session")
	}
	context, recorder := consumerNicknameContext(t, `{"nickname":"甲","principal_profile_etag":"etag-before"}`)
	handler.UpdateNickname(context)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d, want 401", recorder.Code)
	}
}

func consumerAvatarMultipart(t *testing.T, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", "avatar.bin")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(payload); err != nil {
		t.Fatalf("part.Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
	return body, writer.FormDataContentType()
}

func consumerAvatarUploadContext(
	t *testing.T,
	body *bytes.Buffer,
	contentType string,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/me/avatar", body)
	request.Header.Set("Content-Type", contentType)
	context.Request = request
	return context, recorder
}

func testAvatarPNG() []byte {
	return []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
}

func TestUploadAvatarStoresImageAndRepointsPrincipal(t *testing.T) {
	t.Parallel()

	writer := &fakeConsumerIdentityStore{}
	handler, root := newConsumerIdentityTestHandler(t, writer)
	body, contentType := consumerAvatarMultipart(t, testAvatarPNG())
	context, recorder := consumerAvatarUploadContext(t, body, contentType)
	handler.UploadAvatar(context)

	if recorder.Code != http.StatusOK || writer.avatarCalls != 1 {
		t.Fatalf("UploadAvatar() status=%d calls=%d body=%s",
			recorder.Code, writer.avatarCalls, recorder.Body.String())
	}
	name := strings.TrimPrefix(writer.gotAvatarURL, "/api/v1/xiangwan/avatars/")
	if !strings.HasSuffix(name, ".png") || len(name) != 32+4 {
		t.Fatalf("stored avatar name = %q", name)
	}
	if _, err := os.Stat(filepath.Join(root, name)); err != nil {
		t.Fatalf("stored avatar missing: %v", err)
	}
	if !strings.Contains(recorder.Body.String(), `"avatar_url":"/api/v1/xiangwan/avatars/`+name+`"`) {
		t.Fatalf("UploadAvatar() body=%s", recorder.Body.String())
	}
}

func TestUploadAvatarKeepsReplacedXiangwanAvatar(t *testing.T) {
	t.Parallel()

	writer := &fakeConsumerIdentityStore{}
	handler, root := newConsumerIdentityTestHandler(t, writer)
	// Plant the previous Xiangwan-owned avatar object: published avatar
	// objects answer immutable-cache reads, so a replacement must never
	// delete them (delayed GC collects stale objects instead).
	oldPayload := testAvatarPNG()
	oldName, err := handler.avatars.Save(oldPayload, ".png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	writer.previousURL = ConsumerAvatarPath(oldName)

	body, contentType := consumerAvatarMultipart(t, testAvatarPNG())
	context, recorder := consumerAvatarUploadContext(t, body, contentType)
	handler.UploadAvatar(context)
	if recorder.Code != http.StatusOK {
		t.Fatalf("UploadAvatar() status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, oldName)); err != nil {
		t.Fatalf("replaced avatar must be kept for immutable-cache reads: %v", err)
	}
}

func TestUploadAvatarRejectsNonImageAndOversize(t *testing.T) {
	t.Parallel()

	handler, _ := newConsumerIdentityTestHandler(t, &fakeConsumerIdentityStore{})
	body, contentType := consumerAvatarMultipart(t, []byte("definitely not an image"))
	context, recorder := consumerAvatarUploadContext(t, body, contentType)
	handler.UploadAvatar(context)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("non-image status=%d, want 400", recorder.Code)
	}

	writer := &fakeConsumerIdentityStore{}
	handler, _ = newConsumerIdentityTestHandler(t, writer)
	oversized := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		make([]byte, maxConsumerAvatarBytes)...)
	body, contentType = consumerAvatarMultipart(t, oversized)
	context, recorder = consumerAvatarUploadContext(t, body, contentType)
	handler.UploadAvatar(context)
	if recorder.Code != http.StatusBadRequest || writer.avatarCalls != 0 {
		t.Fatalf("oversize status=%d calls=%d, want 400/0", recorder.Code, writer.avatarCalls)
	}
}

// TestConsumerAvatarByteBoundMatchesWechatMediaCap pins the handler bound to
// the wxa/img_sec_check upstream media cap: the API rejects media above
// 1 MiB, so a larger handler bound would only move the refusal to the
// provider after a wasted upload.
func TestConsumerAvatarByteBoundMatchesWechatMediaCap(t *testing.T) {
	t.Parallel()

	if maxConsumerAvatarBytes != 1<<20 {
		t.Fatalf("maxConsumerAvatarBytes = %d, want 1 MiB (1<<20)", maxConsumerAvatarBytes)
	}
	if maxConsumerAvatarUploadBytes != (1<<20)+(1<<20) {
		t.Fatalf("maxConsumerAvatarUploadBytes = %d, want max + 1 MiB framing slack",
			maxConsumerAvatarUploadBytes)
	}
}

func TestUploadAvatarOrphansAreRemovedWhenTheWriteFails(t *testing.T) {
	t.Parallel()

	writer := &fakeConsumerIdentityStore{err: errors.New("database is gone")}
	handler, root := newConsumerIdentityTestHandler(t, writer)
	body, contentType := consumerAvatarMultipart(t, testAvatarPNG())
	context, recorder := consumerAvatarUploadContext(t, body, contentType)
	handler.UploadAvatar(context)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("failed write status=%d, want 500", recorder.Code)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("orphaned avatar left behind: entries=%v err=%v", entries, err)
	}
}

// fakeAvatarModerator records the screened image and replays a canned verdict.
type fakeAvatarModerator struct {
	calls   int
	gotData []byte
	gotName string
	err     error
}

func (fake *fakeAvatarModerator) CheckAvatarImage(
	_ context.Context,
	data []byte,
	filename string,
) error {
	fake.calls++
	fake.gotData = append([]byte(nil), data...)
	fake.gotName = filename
	return fake.err
}

func TestUploadAvatarModerationGate(t *testing.T) {
	t.Parallel()

	t.Run("pass stores the avatar", func(t *testing.T) {
		t.Parallel()
		moderator := &fakeAvatarModerator{}
		writer := &fakeConsumerIdentityStore{}
		handler, root := newConsumerIdentityTestHandler(t, writer)
		handler.moderator = moderator
		body, contentType := consumerAvatarMultipart(t, testAvatarPNG())
		context, recorder := consumerAvatarUploadContext(t, body, contentType)
		handler.UploadAvatar(context)
		if recorder.Code != http.StatusOK || moderator.calls != 1 ||
			writer.avatarCalls != 1 {
			t.Fatalf("UploadAvatar() status=%d moderator=%d writes=%d",
				recorder.Code, moderator.calls, writer.avatarCalls)
		}
		if !bytes.Equal(moderator.gotData, testAvatarPNG()) ||
			moderator.gotName != "avatar.png" {
			t.Fatalf("moderator got %d bytes name=%q", len(moderator.gotData), moderator.gotName)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 1 {
			t.Fatalf("stored avatars = %d, want 1", len(entries))
		}
	})

	t.Run("rejected image is refused and nothing is stored", func(t *testing.T) {
		t.Parallel()
		moderator := &fakeAvatarModerator{err: ErrAvatarImageRejected}
		writer := &fakeConsumerIdentityStore{}
		handler, root := newConsumerIdentityTestHandler(t, writer)
		handler.moderator = moderator
		body, contentType := consumerAvatarMultipart(t, testAvatarPNG())
		context, recorder := consumerAvatarUploadContext(t, body, contentType)
		handler.UploadAvatar(context)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("UploadAvatar() status=%d, want 400", recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), avatarRejectedCustomerMessage) {
			t.Fatalf("UploadAvatar() body=%s", recorder.Body.String())
		}
		if writer.avatarCalls != 0 {
			t.Fatalf("writer called %d times, want 0", writer.avatarCalls)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Fatalf("rejected avatar was stored: %d entries", len(entries))
		}
	})

	t.Run("unavailable service fails closed", func(t *testing.T) {
		t.Parallel()
		moderator := &fakeAvatarModerator{err: ErrAvatarModerationUnavailable}
		writer := &fakeConsumerIdentityStore{}
		handler, root := newConsumerIdentityTestHandler(t, writer)
		handler.moderator = moderator
		body, contentType := consumerAvatarMultipart(t, testAvatarPNG())
		context, recorder := consumerAvatarUploadContext(t, body, contentType)
		handler.UploadAvatar(context)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("UploadAvatar() status=%d, want 400", recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), avatarModerationUnavailableCustomerMessage) {
			t.Fatalf("UploadAvatar() body=%s", recorder.Body.String())
		}
		if writer.avatarCalls != 0 {
			t.Fatalf("writer called %d times, want 0", writer.avatarCalls)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Fatalf("avatar stored while moderation was unavailable: %d entries", len(entries))
		}
	})

	// The handler attaches the moderation failure to the request log, so the
	// error must stay a classified, credential-free shape: the WeChat client
	// layer strips access_token/secret from URLs before this point.
	t.Run("logged failure stays credential-free", func(t *testing.T) {
		t.Parallel()
		moderator := &fakeAvatarModerator{err: fmt.Errorf(
			"%w: wechat img_sec_check request failed: "+
				"Post https://api.weixin.qq.com/wxa/img_sec_check: "+
				"dial tcp 203.0.113.10:443: i/o timeout",
			ErrAvatarModerationUnavailable,
		)}
		writer := &fakeConsumerIdentityStore{}
		handler, root := newConsumerIdentityTestHandler(t, writer)
		handler.moderator = moderator
		body, contentType := consumerAvatarMultipart(t, testAvatarPNG())
		context, recorder := consumerAvatarUploadContext(t, body, contentType)
		handler.UploadAvatar(context)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("UploadAvatar() status=%d, want 400", recorder.Code)
		}
		if len(context.Errors) != 1 {
			t.Fatalf("logged errors = %d, want the classified moderation failure",
				len(context.Errors))
		}
		if !errors.Is(context.Errors[0].Err, ErrAvatarModerationUnavailable) {
			t.Fatalf("logged error = %v, want the moderation-unavailable class",
				context.Errors[0].Err)
		}
		for _, leaked := range []string{"access_token", "secret"} {
			if strings.Contains(context.Errors[0].Error(), leaked) {
				t.Fatalf("logged moderation error leaks %q: %v",
					leaked, context.Errors[0].Err)
			}
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Fatalf("avatar stored while moderation was unavailable: %d entries", len(entries))
		}
	})
}

func TestUploadAvatarAcceptsFileAtExactSizeLimit(t *testing.T) {
	t.Parallel()

	// The multipart framing must not trip the body reader: a file of exactly
	// the cap plus its part headers stays under the reader slack and passes
	// the per-file size check.
	payload := append(
		[]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		make([]byte, maxConsumerAvatarBytes-8)...,
	)
	writer := &fakeConsumerIdentityStore{}
	handler, _ := newConsumerIdentityTestHandler(t, writer)
	body, contentType := consumerAvatarMultipart(t, payload)
	context, recorder := consumerAvatarUploadContext(t, body, contentType)
	handler.UploadAvatar(context)
	if recorder.Code != http.StatusOK || writer.avatarCalls != 1 {
		t.Fatalf("UploadAvatar(exact size) status=%d calls=%d body=%s",
			recorder.Code, writer.avatarCalls, recorder.Body.String())
	}
}

func TestLocalConsumerAvatarStoreGuardsTraversal(t *testing.T) {
	t.Parallel()

	store, err := NewLocalConsumerAvatarStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalConsumerAvatarStore() error = %v", err)
	}
	for _, name := range []string{
		"../secret.png",
		"0123456789abcdef0123456789abcdef.gif",
		"short.png",
	} {
		if object, openErr := store.Open(name); openErr == nil {
			_ = object.Close()
			t.Fatalf("Open(%q) succeeded, want rejection", name)
		}
		if removeErr := store.Remove(name); removeErr == nil {
			t.Fatalf("Remove(%q) succeeded, want rejection", name)
		}
	}
}

func TestGetAvatarServesImmutablePublicRead(t *testing.T) {
	t.Parallel()

	handler, _ := newConsumerIdentityTestHandler(t, &fakeConsumerIdentityStore{})
	payload := testAvatarPNG()
	name, err := handler.avatars.Save(payload, ".png")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/avatars/"+name, nil)
	context.Params = gin.Params{{Key: "filename", Value: name}}
	handler.GetAvatar(context)

	if recorder.Code != http.StatusOK ||
		recorder.Header().Get("Content-Type") != "image/png" ||
		!strings.Contains(recorder.Header().Get("Cache-Control"), "immutable") ||
		!bytes.Equal(recorder.Body.Bytes(), payload) {
		t.Fatalf("GetAvatar() status=%d headers=%v", recorder.Code, recorder.Header())
	}
	recorder = httptest.NewRecorder()
	context, _ = gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/avatars/missing.png", nil)
	context.Params = gin.Params{{Key: "filename", Value: "missing.png"}}
	handler.GetAvatar(context)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing avatar status=%d, want 404", recorder.Code)
	}
}

func TestGetLegacyAvatarRequiresImageFactsAndUsesNoStore(t *testing.T) {
	t.Parallel()

	principalID := uuid.MustParse("00000000-0000-0000-0000-000000000031")
	fileID := uuid.MustParse("00000000-0000-0000-0000-000000000032")
	reader := &fakeLegacyAvatarReader{object: &publicobject.Object{
		FileID:       fileID,
		MIME:         "image/png",
		DetectedMIME: "image/png",
		Size:         int64(len(testAvatarPNG())),
		Content:      bytes.NewReader(testAvatarPNG()),
		Closer:       io.NopCloser(strings.NewReader("")),
	}}
	handler, _ := newConsumerIdentityTestHandler(t, &fakeConsumerIdentityStore{})
	handler.SetLegacyAvatarReader(reader)
	recorder := httptest.NewRecorder()
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(
		http.MethodGet,
		"/avatars/legacy/"+principalID.String()+"/"+fileID.String(),
		nil,
	)
	context.Params = gin.Params{
		{Key: "principal_id", Value: principalID.String()},
		{Key: "file_id", Value: fileID.String()},
	}
	handler.GetLegacyAvatar(context)
	if recorder.Code != http.StatusOK ||
		recorder.Header().Get("Content-Type") != "image/png" ||
		recorder.Header().Get("Cache-Control") != "no-store" ||
		!bytes.Equal(recorder.Body.Bytes(), testAvatarPNG()) ||
		reader.principalID != principalID || reader.fileID != fileID {
		t.Fatalf("GetLegacyAvatar() status=%d headers=%v body=%d", recorder.Code, recorder.Header(), recorder.Body.Len())
	}

	reader.object = &publicobject.Object{
		FileID:       fileID,
		MIME:         "image/png",
		DetectedMIME: "text/html",
		Size:         4,
		Content:      bytes.NewReader([]byte("oops")),
		Closer:       io.NopCloser(strings.NewReader("")),
	}
	recorder = httptest.NewRecorder()
	context, _ = gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/avatars/legacy", nil)
	context.Params = gin.Params{
		{Key: "principal_id", Value: principalID.String()},
		{Key: "file_id", Value: fileID.String()},
	}
	handler.GetLegacyAvatar(context)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("invalid legacy avatar facts status=%d, want 404", recorder.Code)
	}
}
