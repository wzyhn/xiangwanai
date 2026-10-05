package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

func TestMyBenefitsServiceFixesOwnerScope(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	want := people.MyBenefits{
		CurrentRoles:           []people.IdentityRoleSummary{},
		RoleHistory:            []people.IdentityRoleSummary{},
		HostRulesState:         people.HostRulesStatePending,
		HostApplicationHistory: []people.HostApplicationSummary{},
	}
	reader := &fakeMyBenefitsReader{result: want}
	service, err := NewMyBenefitsService(tenantID, reader)
	if err != nil {
		t.Fatalf("NewMyBenefitsService() error = %v", err)
	}
	got, err := service.Read(context.Background(), principalID)
	if err != nil || !reflect.DeepEqual(got, want) || reader.calls != 1 ||
		reader.tenantID != tenantID || reader.principalID != principalID {
		t.Fatalf("Read() = %+v, %v reader=%+v", got, err, reader)
	}
}

func TestMyBenefitsServiceRejectsInvalidOwnerAndWrapsReaderError(t *testing.T) {
	t.Parallel()

	reader := &fakeMyBenefitsReader{err: errors.New("read failed")}
	service, err := NewMyBenefitsService(uuid.New(), reader)
	if err != nil {
		t.Fatalf("NewMyBenefitsService() error = %v", err)
	}
	if _, err := service.Read(
		context.Background(),
		uuid.Nil,
	); !errors.Is(err, ErrInvalidMyBenefitsRequest) || reader.calls != 0 {
		t.Fatalf("Read(nil) error=%v calls=%d", err, reader.calls)
	}
	if _, err := service.Read(
		context.Background(),
		uuid.New(),
	); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("Read(error) = %v", err)
	}
}

type fakeMyBenefitsReader struct {
	result people.MyBenefits
	err    error

	calls       int
	tenantID    uuid.UUID
	principalID uuid.UUID
}

func (fake *fakeMyBenefitsReader) Read(
	_ context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (people.MyBenefits, error) {
	fake.calls++
	fake.tenantID = tenantID
	fake.principalID = principalID
	return fake.result, fake.err
}
