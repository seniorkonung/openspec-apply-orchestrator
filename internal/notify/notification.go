package notify

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

var (
	ErrInvalidChange       = errors.New("некорректный change события")
	ErrInvalidReason       = errors.New("некорректная причина потребности в человеке")
	ErrInvalidKnownSession = errors.New("некорректная известная сессия")
	ErrInvalidSessionID    = errors.New("некорректный идентификатор сессии")
	ErrInvalidSessionLink  = errors.New("некорректная ссылка сессии")

	ErrDeliveryNotConfirmed = errors.New("доставка уведомления не подтверждена")
)

// Reason задаёт наблюдаемую причину потребности в человеке.
type Reason uint8

const (
	ReasonTurnFinished Reason = iota + 1
	ReasonAgentError
	ReasonPermissionRequested
)

// SessionID — проверенный идентификатор известной активной собственной сессии.
type SessionID struct {
	value string
}

func (id SessionID) String() string {
	return id.value
}

// SessionLink — проверенная абсолютная ссылка известной сессии.
type SessionLink struct {
	value string
}

func (link SessionLink) String() string {
	return link.value
}

// KnownSession связывает проверенные идентификатор и ссылку одной сессии.
type KnownSession struct {
	id   SessionID
	link SessionLink
}

func NewKnownSession(id, link string) (KnownSession, error) {
	if !validText(id) {
		return KnownSession{}, ErrInvalidSessionID
	}
	parsed, err := url.Parse(link)
	if err != nil || !validText(link) || parsed.Scheme == "" || parsed.Host == "" ||
		parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return KnownSession{}, ErrInvalidSessionLink
	}
	return KnownSession{
		id:   SessionID{value: id},
		link: SessionLink{value: link},
	}, nil
}

func (session KnownSession) valid() bool {
	return session.id.value != "" && session.link.value != ""
}

// EpisodeKey однозначно задаёт наблюдаемый эпизод потребности в человеке.
type EpisodeKey struct {
	sessionID SessionID
	reason    Reason
}

func (key EpisodeKey) SessionID() SessionID {
	return key.sessionID
}

func (key EpisodeKey) Reason() Reason {
	return key.reason
}

// Intervention — закрытое транспортно-нейтральное событие потребности в человеке.
type Intervention interface {
	Change() string
	Reason() Reason
	Message() string
	SessionID() SessionID
	SessionLink() SessionLink
	EpisodeKey() EpisodeKey
	isIntervention()
}

type intervention struct {
	change  string
	reason  Reason
	message string
	session KnownSession
}

func NewIntervention(change string, reason Reason, session KnownSession) (Intervention, error) {
	if !validText(change) {
		return nil, ErrInvalidChange
	}
	message, ok := safeMessage(reason)
	if !ok {
		return nil, ErrInvalidReason
	}
	if !session.valid() {
		return nil, ErrInvalidKnownSession
	}
	return intervention{
		change:  change,
		reason:  reason,
		message: message,
		session: session,
	}, nil
}

func (intervention) isIntervention() {}

func (event intervention) Change() string {
	return event.change
}

func (event intervention) Reason() Reason {
	return event.reason
}

func (event intervention) Message() string {
	return event.message
}

func (event intervention) SessionID() SessionID {
	return event.session.id
}

func (event intervention) SessionLink() SessionLink {
	return event.session.link
}

func (event intervention) EpisodeKey() EpisodeKey {
	return EpisodeKey{sessionID: event.session.id, reason: event.reason}
}

func safeMessage(reason Reason) (string, bool) {
	switch reason {
	case ReasonTurnFinished:
		return "После хода агента в Git остались незакоммиченные изменения.", true
	case ReasonAgentError:
		return "Агент сообщил об ошибке и ожидает участия пользователя.", true
	case ReasonPermissionRequested:
		return "Сессия ожидает решения пользователя по запросу разрешения.", true
	default:
		return "", false
	}
}

func validText(value string) bool {
	return value != "" && strings.TrimSpace(value) == value &&
		strings.IndexFunc(value, unicode.IsControl) < 0
}

// Deliverer — транспортно-нейтральный интерфейс подтверждаемой доставки.
// Nil означает подтверждённую доставку, ненулевой результат — безопасную ошибку.
type Deliverer interface {
	Deliver(context.Context, Intervention) *DeliveryError
}

// DeliveryError сообщает только об отсутствии подтверждения доставки.
type DeliveryError struct{}

func NewDeliveryError() *DeliveryError {
	return &DeliveryError{}
}

func (*DeliveryError) Error() string {
	return ErrDeliveryNotConfirmed.Error()
}

func (*DeliveryError) Is(target error) bool {
	return target == ErrDeliveryNotConfirmed
}
