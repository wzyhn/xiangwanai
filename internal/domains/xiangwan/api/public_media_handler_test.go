package xiangwanapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/capabilities/storage/publicobject"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestPublicMediaHandlerStreamsAuthorizedRange(t *testing.T) {
	t.Parallel()

	grant := knownPublicMediaGrant()
	content := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	application := &fakePublicMediaApplication{
		open: func() (*PublicMedia, error) {
			return &PublicMedia{
				Grant: grant,
				Object: &publicobject.Object{
					FileID:       grant.FileID,
					MIME:         grant.MIME,
					DetectedMIME: grant.MIME,
					Size:         int64(len(content)),
					Content:      bytes.NewReader(content),
					Closer:       io.NopCloser(bytes.NewReader(nil)),
				},
			}, nil
		},
	}
	engine := gin.New()
	NewPublicMediaHandler(application).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		PublicMediaPath(grant.RelationID, grant.BlockID, grant.FileID),
		nil,
	)
	request.Header.Set("Range", "bytes=1-3")
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusPartialContent ||
		responseRecorder.Header().Get("Content-Type") != "image/png" ||
		responseRecorder.Header().Get("Content-Disposition") != "inline" ||
		!bytes.Equal(responseRecorder.Body.Bytes(), content[1:4]) ||
		application.relationID != grant.RelationID ||
		application.blockID != grant.BlockID ||
		application.fileID != grant.FileID {
		t.Fatalf("GET media status=%d headers=%#v body=%v application=%+v", responseRecorder.Code, responseRecorder.Header(), responseRecorder.Body.Bytes(), application)
	}
}

func TestPublicMediaHandlerRegistersGetAndHead(t *testing.T) {
	t.Parallel()

	engine := gin.New()
	NewPublicMediaHandler(&fakePublicMediaApplication{}).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	routes := engine.Routes()
	if len(routes) != 2 || routes[0].Method != http.MethodGet ||
		routes[1].Method != http.MethodHead || routes[0].Path != routes[1].Path ||
		routes[0].Path !=
			"/api/v1/xiangwan/media/:relation_id/:block_id/:file_id" {
		t.Fatalf("registered media routes = %+v", routes)
	}
}

func TestPublicMediaHandlerRejectsInvalidIdentityBeforeOpen(t *testing.T) {
	t.Parallel()

	application := &fakePublicMediaApplication{}
	engine := gin.New()
	NewPublicMediaHandler(application).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/xiangwan/media/not-an-id/also-bad/still-bad",
		nil,
	)
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != http.StatusBadRequest || application.calls != 0 {
		t.Fatalf("invalid media status=%d calls=%d body=%s", responseRecorder.Code, application.calls, responseRecorder.Body.String())
	}
}

func TestPublicMediaHandlerUsesOpaqueErrors(t *testing.T) {
	t.Parallel()

	grant := knownPublicMediaGrant()
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "not found", err: resource.ErrPublicFileUnavailable, wantStatus: http.StatusNotFound},
		{name: "backend", err: errors.New("private object path"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewPublicMediaHandler(&fakePublicMediaApplication{
				open: func() (*PublicMedia, error) { return nil, test.err },
			}).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			request := httptest.NewRequest(
				http.MethodGet,
				PublicMediaPath(grant.RelationID, grant.BlockID, grant.FileID),
				nil,
			)
			responseRecorder := httptest.NewRecorder()
			engine.ServeHTTP(responseRecorder, request)
			if responseRecorder.Code != test.wantStatus ||
				strings.Contains(responseRecorder.Body.String(), "object path") {
				t.Fatalf("media error status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
			}
		})
	}
}

type fakePublicMediaApplication struct {
	open func() (*PublicMedia, error)

	calls      int
	relationID uuid.UUID
	blockID    uuid.UUID
	fileID     uuid.UUID
}

func (fake *fakePublicMediaApplication) Open(
	_ context.Context,
	relationID uuid.UUID,
	blockID uuid.UUID,
	fileID uuid.UUID,
) (*PublicMedia, error) {
	fake.calls++
	fake.relationID = relationID
	fake.blockID = blockID
	fake.fileID = fileID
	if fake.open == nil {
		return nil, errors.New("media fixture is not configured")
	}
	return fake.open()
}
