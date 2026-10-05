package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestPublisherCommitsFirstPublicationAtomically(t *testing.T) {
	t.Parallel()

	command := validPublisherCommand()
	second := command.Candidate.Sessions[0]
	second.SessionID = uuid.New()
	second.Title = "Second Session"
	second.SessionStartAt = second.SessionStartAt.Add(3 * time.Hour)
	second.SessionEndAt = second.SessionEndAt.Add(3 * time.Hour)
	command.Candidate.Sessions = append(command.Candidate.Sessions, second)

	publisher, tx, capture := newPublisherHarness(t, command, publisherScenario{
		seriesStatus:       activity.SeriesStatusDraft,
		seriesCount:        1,
		seriesVersion:      7,
		instanceStatus:     activity.InstanceStatusPendingPublish,
		instanceVersion:    command.ExpectedInstanceVersion,
		sessionStatus:      activity.SessionStatusDraft,
		sessionVersion:     3,
		publicationVersion: 0,
	})

	event, err := publisher.Publish(context.Background(), command)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
	if capture.seriesUpdates != 1 || capture.instanceUpdates != 1 || capture.sessionUpdates != 2 {
		t.Fatalf("updates = %+v", capture)
	}
	if capture.seriesUpdateArgs[2] != command.Candidate.InstanceID ||
		capture.seriesUpdateArgs[3] != 2 ||
		capture.seriesUpdateArgs[4] != true {
		t.Fatalf("Series update args = %#v", capture.seriesUpdateArgs)
	}
	if capture.instanceUpdateArgs[3] != activity.ActivityTypeAIRoundtable ||
		!reflect.DeepEqual(capture.instanceUpdateArgs[4], []string{"ai"}) {
		t.Fatalf("Instance snapshot args = %#v", capture.instanceUpdateArgs)
	}
	if event.PublicationVersion != 1 ||
		event.SessionCount != 2 ||
		event.PublishedBy != command.PublishedBy ||
		len(event.CandidateDigest) != 64 {
		t.Fatalf("Publish() event = %+v", event)
	}

	wantOrder := []string{
		command.Candidate.Sessions[0].SessionID.String(),
		command.Candidate.Sessions[1].SessionID.String(),
	}
	sort.Strings(wantOrder)
	if !reflect.DeepEqual(capture.sessionUpdateOrder, wantOrder) {
		t.Fatalf("Session update order = %v, want %v", capture.sessionUpdateOrder, wantOrder)
	}
	if capture.isolation != sql.LevelSerializable {
		t.Fatalf("transaction isolation = %v, want serializable", capture.isolation)
	}
}

func TestReadAdminPublicationReferenceReadinessRejectsUnknownQuickTag(t *testing.T) {
	t.Parallel()

	tx := &fakePublicationTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			if !strings.Contains(query, "FROM xiangwan_brand_profiles") {
				t.Fatalf("unexpected readiness query: %s", query)
			}
			return &fakeRow{values: []any{true, false, true, true, true}}
		},
	}}
	_, err := readAdminPublicationReferenceReadiness(
		context.Background(), tx, uuid.New(), uuid.New(), []string{"legacy_tag"},
	)
	if !errors.Is(err, ErrPublicationQuickTagUnavailable) {
		t.Fatalf("readiness error = %v, want ErrPublicationQuickTagUnavailable", err)
	}
}

func TestValidatePublishedQuickTagCodesAllowsEmptyTagsWithoutBrandProfile(t *testing.T) {
	t.Parallel()

	if err := ValidatePublishedQuickTagCodes(
		context.Background(), nil, uuid.New(), nil,
	); err != nil {
		t.Fatalf("empty quick tags error = %v, want nil", err)
	}
}

func TestPublisherRepublishDoesNotMoveSeriesPointerOrIncrementCount(t *testing.T) {
	t.Parallel()

	command := validPublisherCommand()
	command.ExpectedInstanceVersion = 5
	publisher, tx, capture := newPublisherHarness(t, command, publisherScenario{
		seriesStatus:       activity.SeriesStatusActive,
		seriesCount:        4,
		seriesRecurring:    true,
		seriesVersion:      9,
		instanceStatus:     activity.InstanceStatusPublished,
		instanceVersion:    5,
		publicationVersion: 1,
		sessionStatus:      activity.SessionStatusPublished,
		sessionVersion:     6,
	})

	event, err := publisher.Publish(context.Background(), command)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if !tx.committed || capture.seriesUpdates != 0 {
		t.Fatalf("committed=%t Series updates=%d", tx.committed, capture.seriesUpdates)
	}
	if event.PublicationVersion != 2 {
		t.Fatalf("PublicationVersion = %d, want 2", event.PublicationVersion)
	}
}

func TestPublisherRejectsInvalidInputBeforeTransaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*PublishInstanceCommand)
		wantErr error
	}{
		{
			name:    "tenant",
			mutate:  func(command *PublishInstanceCommand) { command.TenantID = uuid.Nil },
			wantErr: ErrInvalidPublicationCommand,
		},
		{
			name:    "publisher",
			mutate:  func(command *PublishInstanceCommand) { command.PublishedBy = uuid.Nil },
			wantErr: ErrInvalidPublicationCommand,
		},
		{
			name: "expected version",
			mutate: func(command *PublishInstanceCommand) {
				command.ExpectedInstanceVersion = 0
			},
			wantErr: ErrInvalidPublicationCommand,
		},
		{
			name: "candidate",
			mutate: func(command *PublishInstanceCommand) {
				command.Candidate.Sessions = nil
			},
			wantErr: activity.ErrInvalidInstancePublication,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			command := validPublisherCommand()
			test.mutate(&command)
			starter := &fakePublicationTransactionStarter{}
			publisher := &Publisher{transactions: starter, now: time.Now}
			if _, err := publisher.Publish(context.Background(), command); !errors.Is(err, test.wantErr) {
				t.Fatalf("Publish() error = %v, want %v", err, test.wantErr)
			}
			if starter.begins != 0 {
				t.Fatalf("transaction began %d time(s)", starter.begins)
			}
		})
	}
}

func TestPublisherRollsBackConcurrencyConflict(t *testing.T) {
	t.Parallel()

	command := validPublisherCommand()
	tx := &fakePublicationTransaction{
		fakeQueryExecutor: &fakeQueryExecutor{
			queryRow: func(query string, _ ...any) rowScanner {
				switch {
				case strings.Contains(query, "FROM xiangwan_activity_series"):
					return &fakeRow{values: []any{
						activity.SeriesStatusDraft,
						0,
						false,
						int64(1),
					}}
				case strings.Contains(query, "FROM xiangwan_activity_instances"):
					return &fakeRow{values: []any{
						activity.InstanceStatusDraft,
						int64(0),
						command.ExpectedInstanceVersion + 1,
					}}
				default:
					t.Fatalf("unexpected query: %s", query)
					return &fakeRow{}
				}
			},
		},
	}
	publisher := &Publisher{
		transactions: &fakePublicationTransactionStarter{tx: tx},
		now:          time.Now,
	}

	if _, err := publisher.Publish(context.Background(), command); !errors.Is(err, ErrPublicationConflict) {
		t.Fatalf("Publish() error = %v, want ErrPublicationConflict", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestPublisherRollsBackCommitFailure(t *testing.T) {
	t.Parallel()

	command := validPublisherCommand()
	publisher, tx, _ := newPublisherHarness(t, command, publisherScenario{
		seriesStatus:       activity.SeriesStatusDraft,
		seriesVersion:      1,
		instanceStatus:     activity.InstanceStatusDraft,
		instanceVersion:    command.ExpectedInstanceVersion,
		sessionStatus:      activity.SessionStatusDraft,
		sessionVersion:     1,
		publicationVersion: 0,
	})
	commitErr := errors.New("commit unavailable")
	tx.commitErr = commitErr

	if _, err := publisher.Publish(context.Background(), command); !errors.Is(err, commitErr) {
		t.Fatalf("Publish() error = %v, want commit error", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestPublisherReportsBeginFailure(t *testing.T) {
	t.Parallel()

	beginErr := errors.New("database unavailable")
	starter := &fakePublicationTransactionStarter{err: beginErr}
	publisher := &Publisher{transactions: starter, now: time.Now}
	if _, err := publisher.Publish(
		context.Background(),
		validPublisherCommand(),
	); !errors.Is(err, beginErr) {
		t.Fatalf("Publish() error = %v, want begin error", err)
	}
	if starter.begins != 1 {
		t.Fatalf("transaction began %d time(s), want 1", starter.begins)
	}
}

func TestAdminPublisherLocksReferencesBeforeActivityRows(t *testing.T) {
	t.Parallel()

	command := validPublisherCommand()
	command.AdminIdentityLinkID = uuid.New()
	command.IdempotencyKey = uuid.New()
	command.RequestID = "request-lock-order"
	grantID := uuid.New()
	order := make([]string, 0, 4)
	tx := &fakePublicationTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_runtime_generations"):
				return &fakeRow{values: []any{int64(1)}}
			case strings.Contains(query, "FROM principals AS principal"):
				if !strings.Contains(query, "FOR SHARE OF principal, identity_link, admin_grant") {
					t.Fatalf("administrator authorization does not lock rows: %s", query)
				}
				return &fakeRow{values: []any{
					command.PublishedBy,
					command.AdminIdentityLinkID,
					grantID,
				}}
			case strings.Contains(query, "publication-references"):
				order = append(order, "references")
				return &fakeRow{values: []any{true}}
			case strings.Contains(query, "FROM xiangwan_admin_operations"):
				return &fakeRow{err: sql.ErrNoRows}
			case strings.Contains(query, "FROM xiangwan_activity_series"):
				order = append(order, "series")
				return &fakeRow{values: []any{
					activity.SeriesStatusDraft,
					0,
					false,
					int64(1),
				}}
			case strings.Contains(query, "FROM xiangwan_activity_instances"):
				return &fakeRow{values: []any{
					activity.InstanceStatusDraft,
					int64(0),
					command.ExpectedInstanceVersion + 1,
				}}
			default:
				t.Fatalf("unexpected row query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	starter := &fakePublicationTransactionStarter{
		tx: tx,
		beginHook: func() {
			order = append(order, "begin")
		},
	}
	lockedConn := &sql.Conn{}
	publisher := &Publisher{
		transactions: starter,
		adminOperationLocks: fakeAdminPublicationOperationLocker{
			conn: lockedConn,
			lockHook: func() {
				order = append(order, "operation")
			},
		},
		adminGenerationID: uuid.New(),
		now:               time.Now,
	}

	if _, err := publisher.Publish(context.Background(), command); !errors.Is(
		err,
		ErrPublicationConflict,
	) {
		t.Fatalf("Publish() error = %v, want ErrPublicationConflict", err)
	}
	if !reflect.DeepEqual(order, []string{"operation", "begin", "references", "series"}) {
		t.Fatalf("lock order = %v, want operation before transaction and references before Series", order)
	}
	if starter.conn != lockedConn {
		t.Fatal("publication transaction did not use the advisory-lock connection")
	}
}

func TestPublisherRejectsChangedOrTerminalSessionSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		storedID      func(PublishInstanceCommand) uuid.UUID
		sessionStatus activity.SessionStatus
		wantErr       error
	}{
		{
			name:          "different Session",
			storedID:      func(PublishInstanceCommand) uuid.UUID { return uuid.New() },
			sessionStatus: activity.SessionStatusDraft,
			wantErr:       ErrPublicationSessionSet,
		},
		{
			name:          "cancelled Session",
			storedID:      func(command PublishInstanceCommand) uuid.UUID { return command.Candidate.Sessions[0].SessionID },
			sessionStatus: activity.SessionStatusCancelled,
			wantErr:       ErrPublicationState,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			command := validPublisherCommand()
			tx := &fakePublicationTransaction{
				fakeQueryExecutor: &fakeQueryExecutor{
					queryRow: func(query string, _ ...any) rowScanner {
						switch {
						case strings.Contains(query, "FROM xiangwan_activity_series"):
							return &fakeRow{values: []any{
								activity.SeriesStatusDraft,
								0,
								false,
								int64(1),
							}}
						case strings.Contains(query, "FROM xiangwan_activity_instances"):
							return &fakeRow{values: []any{
								activity.InstanceStatusDraft,
								int64(0),
								command.ExpectedInstanceVersion,
							}}
						default:
							t.Fatalf("unexpected row query: %s", query)
							return &fakeRow{}
						}
					},
					queryRows: func(query string, _ ...any) (rowsScanner, error) {
						if !strings.Contains(query, "FROM xiangwan_activity_sessions") {
							t.Fatalf("unexpected rows query: %s", query)
						}
						return &fakeRows{rows: [][]any{{
							test.storedID(command),
							test.sessionStatus,
							int64(1),
						}}}, nil
					},
				},
			}
			publisher := &Publisher{
				transactions: &fakePublicationTransactionStarter{tx: tx},
				now:          time.Now,
			}

			if _, err := publisher.Publish(context.Background(), command); !errors.Is(err, test.wantErr) {
				t.Fatalf("Publish() error = %v, want %v", err, test.wantErr)
			}
			if tx.committed || !tx.rolledBack {
				t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
			}
		})
	}
}

type publisherScenario struct {
	seriesStatus       activity.SeriesStatus
	seriesCount        int
	seriesRecurring    bool
	seriesVersion      int64
	instanceStatus     activity.InstanceStatus
	instanceVersion    int64
	publicationVersion int64
	sessionStatus      activity.SessionStatus
	sessionVersion     int64
}

type publisherCapture struct {
	isolation          sql.IsolationLevel
	sessionUpdates     int
	instanceUpdates    int
	seriesUpdates      int
	sessionUpdateOrder []string
	seriesUpdateArgs   []any
	instanceUpdateArgs []any
}

func newPublisherHarness(
	t *testing.T,
	command PublishInstanceCommand,
	scenario publisherScenario,
) (*Publisher, *fakePublicationTransaction, *publisherCapture) {
	t.Helper()

	capture := &publisherCapture{}
	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	tx := &fakePublicationTransaction{}
	tx.fakeQueryExecutor = &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_activity_series"):
				return &fakeRow{values: []any{
					scenario.seriesStatus,
					scenario.seriesCount,
					scenario.seriesRecurring,
					scenario.seriesVersion,
				}}
			case strings.Contains(query, "FROM xiangwan_activity_instances"):
				return &fakeRow{values: []any{
					scenario.instanceStatus,
					scenario.publicationVersion,
					scenario.instanceVersion,
				}}
			case strings.Contains(query, "UPDATE xiangwan_activity_sessions"):
				capture.sessionUpdates++
				capture.sessionUpdateOrder = append(
					capture.sessionUpdateOrder,
					args[2].(uuid.UUID).String(),
				)
				return &fakeRow{values: []any{scenario.sessionVersion + 1}}
			case strings.Contains(query, "UPDATE xiangwan_activity_instances"):
				capture.instanceUpdates++
				capture.instanceUpdateArgs = append([]any(nil), args...)
				return &fakeRow{values: []any{scenario.instanceVersion + 1}}
			case strings.Contains(query, "UPDATE xiangwan_activity_series"):
				capture.seriesUpdates++
				capture.seriesUpdateArgs = append([]any(nil), args...)
				return &fakeRow{values: []any{scenario.seriesVersion + 1}}
			case strings.Contains(query, "INSERT INTO xiangwan_publication_events"):
				event := activity.PublicationEvent{
					ID:                 args[0].(uuid.UUID),
					TenantID:           args[1].(uuid.UUID),
					SeriesID:           args[2].(uuid.UUID),
					InstanceID:         args[3].(uuid.UUID),
					PublicationVersion: args[4].(int64),
					SessionCount:       args[5].(int),
					CandidateDigest:    args[6].(string),
					PublishedBy:        args[7].(uuid.UUID),
					PublishedAt:        args[8].(time.Time),
					CreatedAt:          now,
				}
				return &fakeRow{values: publicationEventScanValues(event)}
			default:
				t.Fatalf("unexpected row query: %s", query)
				return &fakeRow{}
			}
		},
		queryRows: func(query string, _ ...any) (rowsScanner, error) {
			if !strings.Contains(query, "FROM xiangwan_activity_sessions") {
				t.Fatalf("unexpected rows query: %s", query)
			}
			rows := make([][]any, 0, len(command.Candidate.Sessions))
			for _, session := range command.Candidate.Sessions {
				rows = append(rows, []any{
					session.SessionID,
					scenario.sessionStatus,
					scenario.sessionVersion,
				})
			}
			return &fakeRows{rows: rows}, nil
		},
	}
	starter := &fakePublicationTransactionStarter{tx: tx, capture: capture}
	return &Publisher{transactions: starter, now: func() time.Time { return now }}, tx, capture
}

type fakePublicationTransactionStarter struct {
	tx        publicationTransaction
	err       error
	begins    int
	capture   *publisherCapture
	beginHook func()
	conn      *sql.Conn
}

func (starter *fakePublicationTransactionStarter) beginTx(
	_ context.Context,
	conn *sql.Conn,
	options *sql.TxOptions,
) (publicationTransaction, error) {
	starter.begins++
	starter.conn = conn
	if starter.beginHook != nil {
		starter.beginHook()
	}
	if starter.capture != nil {
		starter.capture.isolation = options.Isolation
	}
	if starter.err != nil {
		return nil, starter.err
	}
	return starter.tx, nil
}

type fakeAdminPublicationOperationLocker struct {
	lockHook func()
	conn     *sql.Conn
}

func (locker fakeAdminPublicationOperationLocker) lock(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (*sql.Conn, func() error, error) {
	if locker.lockHook != nil {
		locker.lockHook()
	}
	return locker.conn, func() error { return nil }, nil
}

type fakePublicationTransaction struct {
	*fakeQueryExecutor
	commitErr  error
	committed  bool
	rolledBack bool
}

func (tx *fakePublicationTransaction) Commit() error {
	if tx.commitErr != nil {
		return tx.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *fakePublicationTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

func validPublisherCommand() PublishInstanceCommand {
	registrationStart := time.Date(2026, time.September, 13, 1, 0, 0, 0, time.UTC)
	registrationEnd := registrationStart.Add(24 * time.Hour)
	sessionStart := registrationEnd.Add(time.Hour)
	longitude := 117.2
	latitude := 39.1
	lowStock := 3
	return PublishInstanceCommand{
		TenantID:                uuid.New(),
		PublishedBy:             uuid.New(),
		ExpectedInstanceVersion: 1,
		Candidate: activity.InstancePublicationCandidate{
			SeriesID:      uuid.New(),
			InstanceID:    uuid.New(),
			ActivityType:  activity.ActivityTypeAIRoundtable,
			QuickTagCodes: []string{"ai"},
			Sessions: []activity.SessionPublicationCandidate{
				{
					SessionID:           uuid.New(),
					Title:               "Tianjin maker night",
					RegistrationStartAt: registrationStart,
					RegistrationEndAt:   registrationEnd,
					SessionStartAt:      sessionStart,
					SessionEndAt:        sessionStart.Add(2 * time.Hour),
					Capacity:            20,
					GroupMinimum:        5,
					LowStockThreshold:   &lowStock,
					PriceCents:          9900,
					DeliveryMode:        activity.DeliveryModeOffline,
					Area:                activity.AreaCodeHeping,
					OfflineLocation: &activity.OfflineLocation{
						VenueName: "Xiangwan Lab",
						Address:   "Tianjin",
						Longitude: &longitude,
						Latitude:  &latitude,
					},
					References: activity.PublicationReferenceReadiness{
						QuestionnaireReady: true,
						PeopleReady:        true,
						ContentReady:       true,
						ResourcesReady:     true,
						QuickTagsReady:     true,
					},
				},
			},
		},
	}
}
