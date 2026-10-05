package people

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHostApplicationLifecyclePreservesSubmittedFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	principalID := uuid.New()
	current, err := NewHostApplication(NewHostApplicationCommand{
		TenantID:             uuid.New(),
		PrincipalID:          principalID,
		ApplicationCycle:     `  2026-q4  `,
		PolicyVersion:        `  host-rules-v3  `,
		PersonalIntroduction: `  I build welcoming groups.  `,
		RelevantExperience:   `  Facilitated ten workshops.  `,
		Availability:         `  Weekday evenings.  `,
		ContactMethod:        `  wechat:verified-contact  `,
		SubmittedAt:          now,
	})
	if err != nil {
		t.Fatalf(`NewHostApplication() error = %v`, err)
	}
	if current.ApplicationStatus != HostApplicationStatusPending ||
		current.ApplicationCycle != `2026-q4` ||
		current.PolicyVersion != `host-rules-v3` ||
		current.PersonalIntroduction != `I build welcoming groups.` ||
		current.RelevantExperience != `Facilitated ten workshops.` ||
		current.Availability != `Weekday evenings.` ||
		current.ContactMethod != `wechat:verified-contact` ||
		current.Version != 1 ||
		ValidateHostApplication(current) != nil {
		t.Fatalf(`NewHostApplication() = %+v`, current)
	}
	formatted := fmt.Sprintf(`%+v %#v`, current, current)
	if strings.Contains(formatted, current.ContactMethod) ||
		strings.Contains(formatted, current.PersonalIntroduction) ||
		!strings.Contains(formatted, `[REDACTED]`) {
		t.Fatalf(`HostApplication formatting leaked sensitive fields: %s`, formatted)
	}

	reviewerID := uuid.New()
	approved, duplicate, err := ReviewHostApplication(
		current,
		ReviewHostApplicationCommand{
			Decision: HostApplicationStatusApproved,
			ActorID:  reviewerID,
			Comment:  `  Approved after interview.  `,
			At:       now.Add(time.Hour),
		},
	)
	if err != nil || duplicate ||
		approved.ApplicationStatus != HostApplicationStatusApproved ||
		approved.ReviewedBy == nil ||
		*approved.ReviewedBy != reviewerID ||
		approved.ReviewComment == nil ||
		*approved.ReviewComment != `Approved after interview.` ||
		approved.Version != 2 ||
		current.ReviewedAt != nil {
		t.Fatalf(
			`ReviewHostApplication() = %+v, duplicate=%t, error=%v`,
			approved,
			duplicate,
			err,
		)
	}
	replayed, duplicate, err := ReviewHostApplication(
		approved,
		ReviewHostApplicationCommand{
			Decision: HostApplicationStatusApproved,
			ActorID:  reviewerID,
			Comment:  `Approved after interview.`,
			At:       now.Add(2 * time.Hour),
		},
	)
	if err != nil || !duplicate || replayed.ID != approved.ID ||
		!replayed.UpdatedAt.Equal(approved.UpdatedAt) {
		t.Fatalf(
			`ReviewHostApplication(replay) = %+v, duplicate=%t, error=%v`,
			replayed,
			duplicate,
			err,
		)
	}
	if _, _, err := ReviewHostApplication(
		approved,
		ReviewHostApplicationCommand{
			Decision: HostApplicationStatusRejected,
			ActorID:  reviewerID,
			Comment:  `changed decision`,
			At:       now.Add(2 * time.Hour),
		},
	); !errors.Is(err, ErrHostApplicationTerminal) {
		t.Fatalf(`conflicting terminal review error = %v`, err)
	}
}

func TestHostApplicationCanBeRejectedOrSelfWithdrawn(t *testing.T) {
	t.Parallel()

	current := validHostApplication(t)
	rejected, duplicate, err := ReviewHostApplication(
		current,
		ReviewHostApplicationCommand{
			Decision: HostApplicationStatusRejected,
			ActorID:  uuid.New(),
			Comment:  `More facilitation experience is needed.`,
			At:       current.UpdatedAt.Add(time.Minute),
		},
	)
	if err != nil || duplicate ||
		rejected.ApplicationStatus != HostApplicationStatusRejected ||
		ValidateHostApplication(rejected) != nil {
		t.Fatalf(
			`ReviewHostApplication(reject) = %+v, %t, %v`,
			rejected,
			duplicate,
			err,
		)
	}

	withdrawable := validHostApplication(t)
	withdrawn, duplicate, err := WithdrawHostApplication(
		withdrawable,
		withdrawable.PrincipalID,
		withdrawable.UpdatedAt.Add(time.Minute),
	)
	if err != nil || duplicate ||
		withdrawn.ApplicationStatus != HostApplicationStatusWithdrawn ||
		withdrawn.WithdrawnBy == nil ||
		*withdrawn.WithdrawnBy != withdrawable.PrincipalID ||
		withdrawn.Version != 2 ||
		ValidateHostApplication(withdrawn) != nil {
		t.Fatalf(
			`WithdrawHostApplication() = %+v, %t, %v`,
			withdrawn,
			duplicate,
			err,
		)
	}
	replayed, duplicate, err := WithdrawHostApplication(
		withdrawn,
		withdrawn.PrincipalID,
		withdrawn.UpdatedAt.Add(time.Minute),
	)
	if err != nil || !duplicate || replayed.ID != withdrawn.ID {
		t.Fatalf(
			`WithdrawHostApplication(replay) = %+v, %t, %v`,
			replayed,
			duplicate,
			err,
		)
	}
	if _, _, err := WithdrawHostApplication(
		withdrawable,
		uuid.New(),
		withdrawable.UpdatedAt.Add(time.Minute),
	); !errors.Is(err, ErrInvalidHostApplication) {
		t.Fatalf(`cross-principal withdrawal error = %v`, err)
	}
}

func TestHostApplicationRejectsInvalidInputAndReview(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	valid := NewHostApplicationCommand{
		TenantID:             uuid.New(),
		PrincipalID:          uuid.New(),
		ApplicationCycle:     `2026-q4`,
		PolicyVersion:        `host-rules-v3`,
		PersonalIntroduction: `Introduction`,
		RelevantExperience:   `Experience`,
		Availability:         `Availability`,
		ContactMethod:        `Contact`,
		SubmittedAt:          now,
	}
	tests := []struct {
		name   string
		mutate func(*NewHostApplicationCommand)
	}{
		{`tenant`, func(value *NewHostApplicationCommand) {
			value.TenantID = uuid.Nil
		}},
		{`principal`, func(value *NewHostApplicationCommand) {
			value.PrincipalID = uuid.Nil
		}},
		{`cycle`, func(value *NewHostApplicationCommand) {
			value.ApplicationCycle = `bad cycle`
		}},
		{`policy`, func(value *NewHostApplicationCommand) {
			value.PolicyVersion = ``
		}},
		{`introduction`, func(value *NewHostApplicationCommand) {
			value.PersonalIntroduction = ``
		}},
		{`experience`, func(value *NewHostApplicationCommand) {
			value.RelevantExperience = ``
		}},
		{`availability`, func(value *NewHostApplicationCommand) {
			value.Availability = ``
		}},
		{`contact`, func(value *NewHostApplicationCommand) {
			value.ContactMethod = strings.Repeat(
				`联`,
				MaxHostApplicationContactRunes+1,
			)
		}},
		{`time`, func(value *NewHostApplicationCommand) {
			value.SubmittedAt = time.Time{}
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := valid
			test.mutate(&command)
			if _, err := NewHostApplication(command); !errors.Is(
				err,
				ErrInvalidHostApplication,
			) {
				t.Fatalf(`NewHostApplication() error = %v`, err)
			}
		})
	}

	current, err := NewHostApplication(valid)
	if err != nil {
		t.Fatalf(`NewHostApplication(valid) error = %v`, err)
	}
	if _, _, err := ReviewHostApplication(
		current,
		ReviewHostApplicationCommand{
			Decision: HostApplicationStatusWithdrawn,
			ActorID:  uuid.New(),
			Comment:  `not a review decision`,
			At:       now.Add(time.Minute),
		},
	); !errors.Is(err, ErrInvalidHostApplicationReview) {
		t.Fatalf(`invalid review decision error = %v`, err)
	}
	if _, _, err := ReviewHostApplication(
		current,
		ReviewHostApplicationCommand{
			Decision: HostApplicationStatusApproved,
			ActorID:  uuid.New(),
			Comment:  ``,
			At:       now.Add(time.Minute),
		},
	); !errors.Is(err, ErrInvalidHostApplicationReview) {
		t.Fatalf(`missing review comment error = %v`, err)
	}
}

func validHostApplication(t *testing.T) HostApplication {
	t.Helper()
	value, err := NewHostApplication(NewHostApplicationCommand{
		TenantID:             uuid.New(),
		PrincipalID:          uuid.New(),
		ApplicationCycle:     `2026-q4`,
		PolicyVersion:        `host-rules-v3`,
		PersonalIntroduction: `Introduction`,
		RelevantExperience:   `Experience`,
		Availability:         `Availability`,
		ContactMethod:        `Contact`,
		SubmittedAt:          time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf(`NewHostApplication() error = %v`, err)
	}
	return value
}
