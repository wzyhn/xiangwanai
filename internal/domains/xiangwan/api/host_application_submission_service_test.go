package xiangwanapi

import (
	"context"
	"errors"
	"testing"

	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/google/uuid"
)

func TestHostApplicationSubmissionServiceBindsRuntimeIdentity(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(151)
	principalID := apiUUID(152)
	written := peoplepostgres.HostApplicationResult{Duplicate: true}
	writer := &fakeHostApplicationSubmitter{result: written}
	service, err := NewHostApplicationSubmissionService(tenantID, writer)
	if err != nil {
		t.Fatalf("NewHostApplicationSubmissionService() error = %v", err)
	}
	request := HostApplicationSubmissionRequest{
		PersonalIntroduction: "I facilitate technical communities.",
		RelevantExperience:   "Three years of community events.",
		Availability:         "Weekday evenings.",
		ContactMethod:        "customer-approved contact",
	}
	result, err := service.Apply(context.Background(), principalID, request)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if result.Duplicate != written.Duplicate || writer.calls != 1 ||
		writer.command.TenantID != tenantID ||
		writer.command.PrincipalID != principalID ||
		writer.command.PersonalIntroduction != request.PersonalIntroduction ||
		writer.command.RelevantExperience != request.RelevantExperience ||
		writer.command.Availability != request.Availability ||
		writer.command.ContactMethod != request.ContactMethod {
		t.Fatalf("result=%+v writer=%+v", result, writer)
	}
}

func TestHostApplicationSubmissionServiceRejectsInvalidConstructionAndIdentity(
	t *testing.T,
) {
	t.Parallel()

	if _, err := NewHostApplicationSubmissionService(uuid.Nil, &fakeHostApplicationSubmitter{}); !errors.Is(
		err,
		ErrInvalidHostApplicationSubmissionService,
	) {
		t.Fatalf("nil tenant error = %v", err)
	}
	if _, err := NewHostApplicationSubmissionService(apiUUID(153), nil); !errors.Is(
		err,
		ErrInvalidHostApplicationSubmissionService,
	) {
		t.Fatalf("nil writer error = %v", err)
	}
	service, err := NewHostApplicationSubmissionService(
		apiUUID(154),
		&fakeHostApplicationSubmitter{},
	)
	if err != nil {
		t.Fatalf("NewHostApplicationSubmissionService() error = %v", err)
	}
	if _, err := service.Apply(context.Background(), uuid.Nil, HostApplicationSubmissionRequest{}); !errors.Is(
		err,
		ErrInvalidHostApplicationSubmissionRequest,
	) {
		t.Fatalf("nil principal error = %v", err)
	}
}

func TestHostApplicationSubmissionServicePreservesWriterError(t *testing.T) {
	t.Parallel()

	writer := &fakeHostApplicationSubmitter{
		err: peoplepostgres.ErrHostApplicationRulesUnavailable,
	}
	service, err := NewHostApplicationSubmissionService(apiUUID(156), writer)
	if err != nil {
		t.Fatalf("NewHostApplicationSubmissionService() error = %v", err)
	}
	_, err = service.Apply(
		context.Background(),
		apiUUID(157),
		HostApplicationSubmissionRequest{},
	)
	if !errors.Is(err, peoplepostgres.ErrHostApplicationRulesUnavailable) {
		t.Fatalf("Apply() error = %v", err)
	}
}

type fakeHostApplicationSubmitter struct {
	result peoplepostgres.HostApplicationResult
	err    error

	calls   int
	command peoplepostgres.ApplyHostApplicationCommand
}

func (fake *fakeHostApplicationSubmitter) Apply(
	_ context.Context,
	command peoplepostgres.ApplyHostApplicationCommand,
) (peoplepostgres.HostApplicationResult, error) {
	fake.calls++
	fake.command = command
	return fake.result, fake.err
}
