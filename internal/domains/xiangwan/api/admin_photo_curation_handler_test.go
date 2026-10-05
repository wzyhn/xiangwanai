package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type photoCurationStub struct {
	list  resource.PhotoCurationListCommand
	read  resource.PhotoCurationReadCommand
	write resource.PhotoCurationWriteCommand
}

func (stub *photoCurationStub) ListPhotoCurations(
	_ context.Context, command resource.PhotoCurationListCommand,
) ([]resource.PhotoCurationView, error) {
	stub.list = command
	sessionID := apiUUID(250)
	return []resource.PhotoCurationView{{
		RelationID: apiUUID(246), InstanceID: command.InstanceID,
		OriginalPhotos:  []resource.ReviewPhoto{{BlockID: apiUUID(247), URL: "https://images.example.com/hidden.jpg"}},
		OrderedBlockIDs: []uuid.UUID{}, Version: 1,
	}, {
		RelationID: apiUUID(251), InstanceID: command.InstanceID, SessionID: &sessionID,
		OriginalPhotos: []resource.ReviewPhoto{}, OrderedBlockIDs: []uuid.UUID{}, Version: 0,
	}}, nil
}

func (stub *photoCurationStub) ReadPhotoCuration(
	_ context.Context, command resource.PhotoCurationReadCommand,
) (resource.PhotoCurationView, error) {
	stub.read = command
	return resource.PhotoCurationView{
		RelationID: command.RelationID, InstanceID: apiUUID(240),
		Version: 1, OriginalPhotos: []resource.ReviewPhoto{{BlockID: apiUUID(241), URL: "https://images.example.com/hidden.jpg"}},
		OrderedBlockIDs: []uuid.UUID{},
	}, nil
}

func (stub *photoCurationStub) WritePhotoCuration(
	_ context.Context, command resource.PhotoCurationWriteCommand,
) (resource.PhotoCurationReceipt, error) {
	stub.write = command
	return resource.PhotoCurationReceipt{
		RelationID: command.RelationID, Version: command.ExpectedVersion + 1,
		OrderedBlockIDs: command.OrderedBlockIDs, CoverBlockID: command.CoverBlockID,
	}, nil
}

type photoCurationCatalogStub struct{ xiangwanadmin.Catalog }

func servePhotoCuration(
	principal xiangwanadmin.Principal, stub *photoCurationStub, request *http.Request,
) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, principal) })
	handler := NewAdminCatalogHandler(photoCurationCatalogStub{})
	handler.SetPhotoCurationService(stub)
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestAdminPhotoCurationReadsHiddenOriginalOnlyForExactAdminRelation(t *testing.T) {
	t.Parallel()
	principal, stub := xiangwanAdminPrincipalForTest(), &photoCurationStub{}
	relationID := apiUUID(242)
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/review-resources/"+relationID.String()+"/photo-curation", nil)
	recorder := servePhotoCuration(principal, stub, request)
	if recorder.Code != http.StatusOK || stub.read.RelationID != relationID ||
		stub.read.ActorID != principal.PrincipalID || stub.read.IdentityLinkID != principal.IdentityLinkID ||
		!strings.Contains(recorder.Body.String(), `"ordered_block_ids":[]`) ||
		!strings.Contains(recorder.Body.String(), "hidden.jpg") ||
		recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("photo curation read: status=%d command=%+v body=%s", recorder.Code, stub.read, recorder.Body.String())
	}
}

func TestAdminPhotoCurationListsHiddenPublishedRelation(t *testing.T) {
	t.Parallel()
	principal, stub := xiangwanAdminPrincipalForTest(), &photoCurationStub{}
	instanceID := apiUUID(248)
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/review-photo-curations", nil)
	recorder := servePhotoCuration(principal, stub, request)
	if recorder.Code != http.StatusOK || stub.list.InstanceID != instanceID ||
		stub.list.ActorID != principal.PrincipalID ||
		!strings.Contains(recorder.Body.String(), "hidden.jpg") ||
		!strings.Contains(recorder.Body.String(), `"session_id":"`+apiUUID(250).String()+`"`) ||
		recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("photo curation list: status=%d command=%+v body=%s", recorder.Code, stub.list, recorder.Body.String())
	}
}

func TestAdminPhotoCurationPassesOrderedVisibleBlocksAndOperation(t *testing.T) {
	t.Parallel()
	principal, stub := xiangwanAdminPrincipalForTest(), &photoCurationStub{}
	relationID, first, second, operation := apiUUID(243), apiUUID(244), apiUUID(245), uuid.New()
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/review-resources/"+relationID.String()+"/photo-curation",
		strings.NewReader(`{"expected_version":1,"ordered_block_ids":["`+second.String()+`","`+first.String()+`"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operation.String())
	recorder := servePhotoCuration(principal, stub, request)
	if recorder.Code != http.StatusOK || stub.write.RelationID != relationID ||
		stub.write.OperationID != operation || stub.write.ExpectedVersion != 1 ||
		stub.write.ActorID != principal.PrincipalID ||
		len(stub.write.OrderedBlockIDs) != 2 ||
		stub.write.OrderedBlockIDs[0] != second || stub.write.OrderedBlockIDs[1] != first ||
		recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("photo curation write: status=%d command=%+v body=%s", recorder.Code, stub.write, recorder.Body.String())
	}
}

func TestAdminPhotoCoverCommandDistinguishesOmittedAutomaticAndSelected(t *testing.T) {
	t.Parallel()
	for _, cover := range []string{"", `,"cover_block_id":""`, `,"cover_block_id":"` + apiUUID(244).String() + `"`} {
		stub := &photoCurationStub{}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/xiangwan/admin/review-resources/"+apiUUID(243).String()+"/photo-curation",
			strings.NewReader(`{"expected_version":0,"ordered_block_ids":["`+apiUUID(244).String()+`"]`+cover+`}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", uuid.NewString())
		recorder := servePhotoCuration(xiangwanAdminPrincipalForTest(), stub, request)
		if recorder.Code != http.StatusOK || stub.write.CoverSpecified != (cover != "") {
			t.Fatalf("cover command %q: %d %+v", cover, recorder.Code, stub.write)
		}
		if strings.Contains(cover, apiUUID(244).String()) && (stub.write.CoverBlockID == nil || *stub.write.CoverBlockID != apiUUID(244) || !strings.Contains(recorder.Body.String(), `"cover_block_id"`)) {
			t.Fatalf("selected cover lost: %+v %s", stub.write, recorder.Body.String())
		}
	}
}

func TestAdminPhotoCurationRequiresExplicitPhotoArrayBeforeHideAll(t *testing.T) {
	t.Parallel()
	principal, stub := xiangwanAdminPrincipalForTest(), &photoCurationStub{}
	path := "/api/v1/xiangwan/admin/review-resources/" + apiUUID(249).String() + "/photo-curation"
	for _, body := range []string{`{"expected_version":1}`, `{"expected_version":1,"ordered_block_ids":null}`} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", uuid.NewString())
		if recorder := servePhotoCuration(principal, stub, request); recorder.Code != http.StatusBadRequest ||
			stub.write.RelationID != uuid.Nil {
			t.Fatalf("omitted photo list accepted: status=%d write=%+v", recorder.Code, stub.write)
		}
	}
	request := httptest.NewRequest(http.MethodPost, path,
		strings.NewReader(`{"expected_version":1,"ordered_block_ids":[]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	if recorder := servePhotoCuration(principal, stub, request); recorder.Code != http.StatusOK ||
		stub.write.RelationID == uuid.Nil || len(stub.write.OrderedBlockIDs) != 0 {
		t.Fatalf("explicit hide-all rejected: status=%d write=%+v", recorder.Code, stub.write)
	}
}
