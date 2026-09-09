package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/notify"
)

const (
	testChange      = "orchestrate-commit-preparation"
	testSessionID   = "agent-local-123"
	testSessionLink = "paseo://h/server-local/agent/agent-local-123"
)

func TestСобытиеПотребностиВЧеловекеСодержитТолькоБезопасныеДанные(t *testing.T) {
	session, err := notify.NewKnownSession(testSessionID, testSessionLink)
	if err != nil {
		t.Fatalf("создать известную сессию: %v", err)
	}

	tests := []struct {
		name    string
		reason  notify.Reason
		message string
	}{
		{
			name:    "ход завершён при оставшейся работе",
			reason:  notify.ReasonTurnFinished,
			message: "После хода агента в Git остались незакоммиченные изменения.",
		},
		{
			name:    "агент сообщил об ошибке",
			reason:  notify.ReasonAgentError,
			message: "Агент сообщил об ошибке и ожидает участия пользователя.",
		},
		{
			name:    "запрошено разрешение",
			reason:  notify.ReasonPermissionRequested,
			message: "Сессия ожидает решения пользователя по запросу разрешения.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, eventErr := notify.NewIntervention(testChange, tt.reason, session)
			if eventErr != nil {
				t.Fatalf("создать событие: %v", eventErr)
			}
			if event.Change() != testChange {
				t.Fatalf("неожиданный change: %q", event.Change())
			}
			if event.Reason() != tt.reason {
				t.Fatalf("неожиданная причина: %v", event.Reason())
			}
			if event.Message() != tt.message {
				t.Fatalf("неожиданное безопасное сообщение: %q", event.Message())
			}
			if event.SessionID().String() != testSessionID {
				t.Fatalf("неожиданный ID сессии: %q", event.SessionID().String())
			}
			if event.SessionLink().String() != testSessionLink {
				t.Fatalf("неожиданная ссылка сессии: %q", event.SessionLink().String())
			}

			key := event.EpisodeKey()
			if key.SessionID() != event.SessionID() || key.Reason() != event.Reason() {
				t.Fatalf("ключ эпизода не соответствует событию: %#v", key)
			}
		})
	}
}

func TestСобытиеНельзяСоздатьБезИзвестнойСессииИлиСсылки(t *testing.T) {
	tests := []struct {
		name string
		id   string
		link string
		want error
	}{
		{name: "пустой ID", id: "", link: testSessionLink, want: notify.ErrInvalidSessionID},
		{name: "ID с пробелами по краям", id: " agent-1 ", link: testSessionLink, want: notify.ErrInvalidSessionID},
		{name: "ID с управляющим символом", id: "agent\n1", link: testSessionLink, want: notify.ErrInvalidSessionID},
		{name: "пустая ссылка", id: testSessionID, link: "", want: notify.ErrInvalidSessionLink},
		{name: "относительная ссылка", id: testSessionID, link: "/agent/agent-local-123", want: notify.ErrInvalidSessionLink},
		{name: "ссылка с данными пользователя", id: testSessionID, link: "paseo://user:secret@h/server/agent/id", want: notify.ErrInvalidSessionLink},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := notify.NewKnownSession(tt.id, tt.link)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ожидалась ошибка %v, получено %v", tt.want, err)
			}
		})
	}

	session, err := notify.NewKnownSession(testSessionID, testSessionLink)
	if err != nil {
		t.Fatalf("создать известную сессию: %v", err)
	}
	if _, err := notify.NewIntervention(testChange, 0, session); !errors.Is(err, notify.ErrInvalidReason) {
		t.Fatalf("ожидалась ошибка причины, получено %v", err)
	}
	if _, err := notify.NewIntervention("", notify.ReasonAgentError, session); !errors.Is(err, notify.ErrInvalidChange) {
		t.Fatalf("ожидалась ошибка change, получено %v", err)
	}
	if _, err := notify.NewIntervention(testChange, notify.ReasonAgentError, notify.KnownSession{}); !errors.Is(err, notify.ErrInvalidKnownSession) {
		t.Fatalf("ожидалась ошибка известной сессии, получено %v", err)
	}
}

func TestКлючЭпизодаОпределяетсяИдентификаторомСессииИПричиной(t *testing.T) {
	firstSession, err := notify.NewKnownSession(testSessionID, testSessionLink)
	if err != nil {
		t.Fatalf("создать первую сессию: %v", err)
	}
	secondSession, err := notify.NewKnownSession("agent-local-456", "paseo://h/server-local/agent/agent-local-456")
	if err != nil {
		t.Fatalf("создать вторую сессию: %v", err)
	}

	first := mustIntervention(t, testChange, notify.ReasonAgentError, firstSession).EpisodeKey()
	same := mustIntervention(t, "another-change", notify.ReasonAgentError, firstSession).EpisodeKey()
	differentReason := mustIntervention(t, testChange, notify.ReasonPermissionRequested, firstSession).EpisodeKey()
	differentSession := mustIntervention(t, testChange, notify.ReasonAgentError, secondSession).EpisodeKey()

	if first != same {
		t.Fatal("change не должен входить в ключ неизменного эпизода")
	}
	if first == differentReason {
		t.Fatal("другая причина должна образовать другой эпизод")
	}
	if first == differentSession {
		t.Fatal("другая сессия должна образовать другой эпизод")
	}
}

func TestИнтерфейсДоставкиРазличаетПодтверждениеИБезопаснуюОшибку(t *testing.T) {
	session, err := notify.NewKnownSession(testSessionID, testSessionLink)
	if err != nil {
		t.Fatalf("создать известную сессию: %v", err)
	}
	event := mustIntervention(t, testChange, notify.ReasonTurnFinished, session)

	confirmed := fakeDeliverer{}
	if deliveryErr := confirmed.Deliver(context.Background(), event); deliveryErr != nil {
		t.Fatalf("подтверждённая доставка вернула ошибку: %v", deliveryErr)
	}

	failed := fakeDeliverer{err: notify.NewDeliveryError()}
	deliveryErr := failed.Deliver(context.Background(), event)
	if !errors.Is(deliveryErr, notify.ErrDeliveryNotConfirmed) {
		t.Fatalf("ожидалась безопасная ошибка доставки, получено %v", deliveryErr)
	}
	if deliveryErr.Error() != "доставка уведомления не подтверждена" {
		t.Fatalf("ошибка раскрывает неожиданные сведения: %q", deliveryErr.Error())
	}
}

type fakeDeliverer struct {
	err *notify.DeliveryError
}

func (delivery fakeDeliverer) Deliver(context.Context, notify.Intervention) *notify.DeliveryError {
	return delivery.err
}

var _ notify.Deliverer = fakeDeliverer{}

func mustIntervention(
	t *testing.T,
	change string,
	reason notify.Reason,
	session notify.KnownSession,
) notify.Intervention {
	t.Helper()
	event, err := notify.NewIntervention(change, reason, session)
	if err != nil {
		t.Fatalf("создать событие: %v", err)
	}
	return event
}
