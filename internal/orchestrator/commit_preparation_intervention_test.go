package orchestrator

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/notify"
)

func TestПереданнаяСессияОжидаетсяДоПодтверждённогоЗакрытия(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions: ownSessionObservation(
			t, change, workspace, "session-1", "idle", "finished",
		),
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-1", "idle", "finished"),
			ownSessionObservation(t, change, workspace, "session-1", "closed", ""),
		},
		gitStates: []WorkingTreeObservation{
			DirtyWorkingTree{},
			CleanWorkingTree{},
			CleanWorkingTree{},
		},
	}
	delivery := &fakeInterventionDelivery{events: &gateway.events}
	reconciler := mustMonitoredCommitPreparationReconciler(t, gateway, delivery, nil)

	outcome, err := reconciler.Run(context.Background(), change, "/repo")
	if err != nil {
		t.Fatalf("сопроводить переданную сессию: %v", err)
	}
	completed, ok := outcome.(CommitPreparationCompleted)
	if !ok || completed.SessionID().String() != "session-1" {
		t.Fatalf("ожидалось подтверждённое завершение session-1, получено %#v", outcome)
	}

	want := []string{
		"change:read", "workspace:read", "sessions:read", "git:read",
		"intervention:deliver:finished", "intervention:pause",
		"change:read", "workspace:read", "session:observe:session-1", "git:read",
		"intervention:pause",
		"change:read", "workspace:read", "session:observe:session-1", "git:read",
	}
	if !reflect.DeepEqual(gateway.events, want) {
		t.Fatalf("неожиданный ход сопровождения:\nполучено: %v\nожидалось: %v", gateway.events, want)
	}
	if gateway.archivedSessions != 0 || gateway.createdSessions != 0 {
		t.Fatalf("переданная сессия была архивирована или заменена: %v", gateway.events)
	}
	if delivery.calls != 1 {
		t.Fatalf("неизменный успешно доставленный эпизод отправлен %d раз", delivery.calls)
	}
}

func TestВозобновившийсяХодВозвращаетсяКОдномуБлокирующемуWait(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions: ownSessionObservation(
			t, change, workspace, "session-1", "error", "error",
		),
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-1", "running", ""),
			ownSessionObservation(t, change, workspace, "session-1", "idle", "permission"),
			ownSessionObservation(t, change, workspace, "session-1", "closed", ""),
		},
		gitStates: []WorkingTreeObservation{CleanWorkingTree{}},
	}
	delivery := &fakeInterventionDelivery{events: &gateway.events}
	reconciler := mustMonitoredCommitPreparationReconciler(t, gateway, delivery, nil)

	outcome, err := reconciler.Run(context.Background(), change, "/repo")
	if err != nil {
		t.Fatalf("сопроводить возобновившийся ход: %v", err)
	}
	if _, ok := outcome.(CommitPreparationCompleted); !ok {
		t.Fatalf("ожидалось завершённое поручение, получено %T", outcome)
	}
	if countEvent(gateway.events, "session:wait:session-1") != 1 {
		t.Fatalf("возобновившийся ход не получил ровно один wait: %v", gateway.events)
	}
	if delivery.calls != 2 {
		t.Fatalf("смена причины должна создать два эпизода, доставлено %d", delivery.calls)
	}
	if countEvent(gateway.events, "intervention:pause") != 2 {
		t.Fatalf("idle-сессия перечитывалась без ограничивающего ожидания: %v", gateway.events)
	}
}

func TestЗакрытиеПереданнойСессииПриГрязномGitЗавершаетТолькоТекущуюПопытку(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions: ownSessionObservation(
			t, change, workspace, "session-1", "idle", "permission",
		),
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-1", "closed", ""),
		},
		gitStates: []WorkingTreeObservation{DirtyWorkingTree{}},
	}
	delivery := &fakeInterventionDelivery{events: &gateway.events}
	reconciler := mustMonitoredCommitPreparationReconciler(t, gateway, delivery, nil)

	outcome, err := reconciler.Run(context.Background(), change, "/repo")
	if err != nil {
		t.Fatalf("сопроводить закрытие с изменениями: %v", err)
	}
	closed, ok := outcome.(ClosedSessionWithChanges)
	if !ok || closed.SessionID().String() != "session-1" {
		t.Fatalf("ожидалось закрытие session-1 с изменениями, получено %#v", outcome)
	}
	if gateway.preparedSessions != 0 || gateway.createdSessions != 0 || gateway.archivedSessions != 0 {
		t.Fatalf("закрытие вызвало новую сессию или архивирование: %v", gateway.events)
	}
}

func TestНеуспешнаяДоставкаПовторяетсяТолькоПослеСвежегоПодтверждения(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions: ownSessionObservation(
			t, change, workspace, "session-1", "idle", "permission",
		),
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-1", "idle", "permission"),
			ownSessionObservation(t, change, workspace, "session-1", "closed", ""),
		},
		gitStates: []WorkingTreeObservation{DirtyWorkingTree{}},
	}
	delivery := &fakeInterventionDelivery{
		events: &gateway.events,
		errors: []*notify.DeliveryError{notify.NewDeliveryError(), nil},
	}
	reconciler := mustMonitoredCommitPreparationReconciler(t, gateway, delivery, nil)

	if _, err := reconciler.Run(context.Background(), change, "/repo"); err != nil {
		t.Fatalf("сопроводить с повтором доставки: %v", err)
	}
	if delivery.calls != 2 {
		t.Fatalf("неподтверждённая доставка повторена %d раз вместо двух попыток", delivery.calls)
	}
	firstDelivery := indexEvent(gateway.events, "intervention:deliver:permission")
	observation := indexEvent(gateway.events, "session:observe:session-1")
	secondDelivery := lastIndexEvent(gateway.events, "intervention:deliver:permission")
	if firstDelivery < 0 || observation <= firstDelivery || secondDelivery <= observation {
		t.Fatalf("повтор выполнен без свежего подтверждения потребности: %v", gateway.events)
	}
}

func TestНовыйХодЗавершаетПрежнийЭпизодПотребностиВЧеловеке(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions: ownSessionObservation(
			t, change, workspace, "session-1", "error", "error",
		),
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-1", "running", ""),
			ownSessionObservation(t, change, workspace, "session-1", "error", "error"),
			ownSessionObservation(t, change, workspace, "session-1", "closed", ""),
		},
		gitStates: []WorkingTreeObservation{CleanWorkingTree{}},
	}
	delivery := &fakeInterventionDelivery{events: &gateway.events}
	reconciler := mustMonitoredCommitPreparationReconciler(t, gateway, delivery, nil)

	if _, err := reconciler.Run(context.Background(), change, "/repo"); err != nil {
		t.Fatalf("сопроводить два эпизода с новым ходом: %v", err)
	}
	if delivery.calls != 2 {
		t.Fatalf("новая потребность после хода должна быть доставлена повторно, попыток: %d", delivery.calls)
	}
}

func TestИсчезновениеПотребностиПоЧистомуGitЗавершаетПрежнийЭпизод(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions: ownSessionObservation(
			t, change, workspace, "session-1", "idle", "finished",
		),
		knownObservations: []OwnSessionObservation{
			ownSessionObservation(t, change, workspace, "session-1", "idle", "finished"),
			ownSessionObservation(t, change, workspace, "session-1", "idle", "finished"),
			ownSessionObservation(t, change, workspace, "session-1", "closed", ""),
		},
		gitStates: []WorkingTreeObservation{
			DirtyWorkingTree{},
			CleanWorkingTree{},
			DirtyWorkingTree{},
			CleanWorkingTree{},
		},
	}
	delivery := &fakeInterventionDelivery{events: &gateway.events}
	reconciler := mustMonitoredCommitPreparationReconciler(t, gateway, delivery, nil)

	if _, err := reconciler.Run(context.Background(), change, "/repo"); err != nil {
		t.Fatalf("сопроводить повторную потребность после чистого Git: %v", err)
	}
	if delivery.calls != 2 {
		t.Fatalf("возникшая заново потребность должна быть доставлена повторно, попыток: %d", delivery.calls)
	}
}

func TestНовыйЗапускМожетПовторитьУведомлениеТойЖеСессии(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions: ownSessionObservation(
			t, change, workspace, "session-1", "idle", "permission",
		),
	}
	delivery := &fakeInterventionDelivery{events: &gateway.events}
	reconciler := mustMonitoredCommitPreparationReconciler(
		t,
		gateway,
		delivery,
		[]error{context.Canceled, context.Canceled},
	)

	for run := 1; run <= 2; run++ {
		_, err := reconciler.Run(context.Background(), change, "/repo")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("запуск %d должен остановиться отдельной отменой, получено %v", run, err)
		}
		var obstacle *SourceReadObstacle
		if errors.As(err, &obstacle) {
			t.Fatalf("отмена запуска %d стала ошибкой источника: %v", run, err)
		}
	}
	if delivery.calls != 2 {
		t.Fatalf("новый запуск не повторил уведомление, попыток: %d", delivery.calls)
	}
}

func TestОшибкаИсточникаПослеПередачиНеПодменяетсяОшибкойДоставки(t *testing.T) {
	change := mustChangeKey(t, "orchestrate-commit-preparation")
	workspace := mustWorkspaceID(t, "workspace-1")
	readErr := errors.New("paseo inspect недоступен")
	gateway := &fakeCommitPreparationGateway{
		workspace: OneManagedWorkspace{ID: workspace},
		sessions: ownSessionObservation(
			t, change, workspace, "session-1", "error", "error",
		),
		observeErr: readErr,
	}
	delivery := &fakeInterventionDelivery{
		events: &gateway.events,
		errors: []*notify.DeliveryError{notify.NewDeliveryError()},
	}
	reconciler := mustMonitoredCommitPreparationReconciler(t, gateway, delivery, nil)

	outcome, err := reconciler.Run(context.Background(), change, "/repo")
	if outcome != nil {
		t.Fatalf("ошибка источника признана исходом: %#v", outcome)
	}
	assertSourceReadObstacle(t, err, ReadSourcePaseo, readErr)
	if delivery.calls != 1 {
		t.Fatalf("ошибка доставки неожиданно изменила число попыток: %d", delivery.calls)
	}
}

type fakeInterventionDelivery struct {
	events *([]string)
	calls  int
	errors []*notify.DeliveryError
}

func (delivery *fakeInterventionDelivery) Deliver(
	_ context.Context,
	event notify.Intervention,
) *notify.DeliveryError {
	delivery.calls++
	if delivery.events != nil {
		*delivery.events = append(
			*delivery.events,
			"intervention:deliver:"+interventionReasonName(event.Reason()),
		)
	}
	if len(delivery.errors) == 0 {
		return nil
	}
	err := delivery.errors[0]
	delivery.errors = delivery.errors[1:]
	return err
}

func mustMonitoredCommitPreparationReconciler(
	t *testing.T,
	gateway *fakeCommitPreparationGateway,
	delivery notify.Deliverer,
	pauseErrors []error,
) *CommitPreparationReconciler {
	t.Helper()
	pause := func(ctx context.Context, interval time.Duration) error {
		gateway.events = append(gateway.events, "intervention:pause")
		if interval != 30*time.Second {
			t.Fatalf("неожиданный интервал наблюдения: %s", interval)
		}
		if len(pauseErrors) == 0 {
			return nil
		}
		err := pauseErrors[0]
		pauseErrors = pauseErrors[1:]
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		return err
	}
	reconciler, err := newMonitoredCommitPreparationReconciler(
		gateway,
		delivery,
		func(id SessionID) (notify.KnownSession, error) {
			return notify.NewKnownSession(
				id.String(),
				"paseo://h/server-local/agent/"+id.String(),
			)
		},
		pause,
	)
	if err != nil {
		t.Fatalf("создать цикл длительного участия человека: %v", err)
	}
	return reconciler
}

func interventionReasonName(reason notify.Reason) string {
	switch reason {
	case notify.ReasonTurnFinished:
		return "finished"
	case notify.ReasonAgentError:
		return "error"
	case notify.ReasonPermissionRequested:
		return "permission"
	default:
		return "unknown"
	}
}

func lastIndexEvent(events []string, target string) int {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index] == target {
			return index
		}
	}
	return -1
}

var _ notify.Deliverer = (*fakeInterventionDelivery)(nil)
