package xiangwanapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type registrationReadCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, uuid.UUID, string) (xiangwanadmin.RegistrationDetail, error)
}

func (catalog registrationReadCatalog) GetRegistration(
	ctx context.Context, principal xiangwanadmin.Principal, id uuid.UUID, purpose string,
) (xiangwanadmin.RegistrationDetail, error) {
	return catalog.read(ctx, principal, id, purpose)
}

func TestGetRegistrationContactQuery(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	id := apiUUID(160)
	for _, test := range []struct {
		name       string
		query      string
		purpose    string
		err        error
		wantStatus int
		wantCall   bool
	}{
		{name: "masked detail", wantStatus: http.StatusOK, wantCall: true},
		{name: "activity coordination", query: "?contact_purpose=activity_coordination", purpose: "activity_coordination", wantStatus: http.StatusOK, wantCall: true},
		{name: "onsite verification", query: "?contact_purpose=onsite_verification", purpose: "onsite_verification", wantStatus: http.StatusOK, wantCall: true},
		{name: "unknown key", query: "?unexpected=value", wantStatus: http.StatusBadRequest},
		{name: "duplicate purpose", query: "?contact_purpose=activity_coordination&contact_purpose=onsite_verification", wantStatus: http.StatusBadRequest},
		{name: "invalid purpose", query: "?contact_purpose=export", purpose: "export", err: xiangwanadmin.ErrInvalidCatalogRequest, wantStatus: http.StatusBadRequest, wantCall: true},
		{name: "unauthorized reveal", query: "?contact_purpose=activity_coordination", purpose: "activity_coordination", err: xiangwanadmin.ErrScopeForbidden, wantStatus: http.StatusForbidden, wantCall: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			catalog := registrationReadCatalog{read: func(_ context.Context, actor xiangwanadmin.Principal, gotID uuid.UUID, purpose string) (xiangwanadmin.RegistrationDetail, error) {
				called = true
				if actor.PrincipalID != principal.PrincipalID || gotID != id || purpose != test.purpose {
					t.Fatal("registration read did not receive the authenticated actor, target and purpose")
				}
				value := xiangwanadmin.RegistrationDetail{RegistrationItem: xiangwanadmin.RegistrationItem{ID: id}}
				if purpose != "" && test.err == nil {
					value.Contact = &xiangwanadmin.RegistrationContact{Name: "Test participant", Phone: "13800000000"}
				}
				return value, test.err
			}}
			recorder := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
				"/api/v1/xiangwan/admin/registrations/"+id.String()+test.query, nil))
			if recorder.Code != test.wantStatus || called != test.wantCall {
				t.Fatalf("status = %d, called = %t; want %d, %t", recorder.Code, called, test.wantStatus, test.wantCall)
			}
			if recorder.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("registration detail must not be cached")
			}
			if test.wantStatus == http.StatusOK {
				var body struct {
					Data struct {
						Contact *struct{ Name, Phone string } `json:"contact"`
					} `json:"data"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if test.purpose == "" && body.Data.Contact != nil {
					t.Fatal("default detail unexpectedly exposed contact")
				}
				if test.purpose != "" && (body.Data.Contact == nil || body.Data.Contact.Name != "Test participant" || body.Data.Contact.Phone != "13800000000") {
					t.Fatal("authorized detail did not project the contact")
				}
			}
		})
	}
}
