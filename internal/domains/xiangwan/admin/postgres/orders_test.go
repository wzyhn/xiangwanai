package postgres

import (
	"errors"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

func TestAdminOrderCursorBindsTenantStatusAndSnapshot(t *testing.T) {
	now := time.Date(2026, 9, 30, 8, 0, 0, 123456789, time.UTC)
	tenantID := uuid.New()
	filter, asOf, cursor, err := normalizeAdminOrderFilter(
		tenantID, xiangwanadmin.OrderListFilter{}, now,
	)
	if err != nil || filter.Status != "all" ||
		filter.Limit != xiangwanadmin.DefaultOrderListLimit || cursor != nil ||
		!asOf.Equal(now.Truncate(time.Microsecond)) {
		t.Fatalf("default Order filter = %+v, %s, %+v, %v", filter, asOf, cursor, err)
	}
	token, err := encodeAdminOrderCursor(adminOrderCursor{
		Version: 1, TenantID: tenantID, Status: "paid_confirmed", AsOf: asOf,
		CreatedAt: asOf.Add(-time.Minute), OrderID: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := xiangwanadmin.OrderListFilter{Status: "paid_confirmed", Limit: 10, Cursor: token}
	_, nextAsOf, decoded, err := normalizeAdminOrderFilter(tenantID, valid, now)
	if err != nil || decoded == nil || !nextAsOf.Equal(asOf) {
		t.Fatalf("valid Order cursor = %s, %+v, %v", nextAsOf, decoded, err)
	}
	for name, candidate := range map[string]struct {
		tenant uuid.UUID
		filter xiangwanadmin.OrderListFilter
		now    time.Time
	}{
		"other tenant":     {uuid.New(), valid, now},
		"other status":     {tenantID, xiangwanadmin.OrderListFilter{Status: "pending", Cursor: token}, now},
		"future snapshot":  {tenantID, valid, now.Add(-2 * time.Minute)},
		"malformed cursor": {tenantID, xiangwanadmin.OrderListFilter{Cursor: "malformed"}, now},
		"unknown status":   {tenantID, xiangwanadmin.OrderListFilter{Status: "refunded"}, now},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := normalizeAdminOrderFilter(candidate.tenant, candidate.filter, candidate.now)
			if !errors.Is(err, xiangwanadmin.ErrInvalidCatalogRequest) {
				t.Fatalf("normalizeAdminOrderFilter() error = %v", err)
			}
		})
	}
}
