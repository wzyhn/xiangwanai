package activitypostgres

import (
	"context"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

type instanceCancellationPlan struct {
	snapshot   activity.InstanceCancellationSnapshot
	assessment activity.InstanceCancellationAssessment
	sessions   []instanceCancellationSessionPlan
}

type instanceCancellationSessionPlan struct {
	current      activity.Session
	cancellation *sessionCancellationPlan
}

func buildInstanceCancellationPlan(
	ctx context.Context,
	tx instanceCancellationTransaction,
	instance activity.Instance,
	sessions []activity.Session,
) (instanceCancellationPlan, error) {
	plan := instanceCancellationPlan{
		sessions: make([]instanceCancellationSessionPlan, 0, len(sessions)),
	}
	snapshots := make(
		[]activity.InstanceCancellationSessionSnapshot,
		0,
		len(sessions),
	)
	for _, current := range sessions {
		if current.TenantID != instance.TenantID ||
			current.InstanceID != instance.ID {
			return instanceCancellationPlan{}, ErrInstanceCancellationTransaction
		}
		sessionPlan := instanceCancellationSessionPlan{current: current}
		snapshot := activity.InstanceCancellationSessionSnapshot{
			SessionID:        current.ID,
			SessionStatus:    current.Status,
			SessionVersion:   current.Version,
			SessionUpdatedAt: current.UpdatedAt,
		}
		switch current.Status {
		case activity.SessionStatusPublished:
			registrations, err := tx.listOpenRegistrations(
				ctx,
				current.TenantID,
				current.ID,
			)
			if err != nil {
				return instanceCancellationPlan{}, err
			}
			cancellation, err := buildSessionCancellationPlan(
				ctx,
				tx,
				current,
				instance.SeriesID,
				registrations,
				instanceCancellationCause,
			)
			if err != nil {
				return instanceCancellationPlan{}, err
			}
			sessionPlan.cancellation = &cancellation
			snapshot.Cancellation = &cancellation.snapshot
		case activity.SessionStatusCancelled:
			registrations, err := tx.listOpenRegistrations(
				ctx,
				current.TenantID,
				current.ID,
			)
			if err != nil {
				return instanceCancellationPlan{}, err
			}
			if len(registrations) != 0 ||
				current.ConfirmedRegistrationCount != 0 ||
				current.ActiveHoldCount != 0 {
				return instanceCancellationPlan{},
					ErrInstanceCancellationTransaction
			}
		default:
			return instanceCancellationPlan{}, ErrInstanceCancellationConflict
		}
		plan.sessions = append(plan.sessions, sessionPlan)
		snapshots = append(snapshots, snapshot)
	}
	plan.snapshot = activity.InstanceCancellationSnapshot{
		TenantID:          instance.TenantID,
		SeriesID:          instance.SeriesID,
		InstanceID:        instance.ID,
		InstanceStatus:    instance.Status,
		InstanceVersion:   instance.Version,
		InstanceUpdatedAt: instance.UpdatedAt,
		Sessions:          snapshots,
	}
	assessment, _, err := activity.AssessInstanceCancellation(plan.snapshot)
	if err != nil {
		return instanceCancellationPlan{}, fmt.Errorf(
			"%w: %v",
			ErrInstanceCancellationTransaction,
			err,
		)
	}
	plan.assessment = assessment
	return plan, nil
}

func instanceCancellationSessionPreviewKey(
	sessionID uuid.UUID,
) string {
	return "instance-session-preview:" + sessionID.String()
}

func instanceCancellationSessionOperationKey(
	sessionID uuid.UUID,
) string {
	return "instance-session-cancel:" + sessionID.String()
}
