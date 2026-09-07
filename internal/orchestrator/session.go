package orchestrator

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

const (
	LabelOwner   = "oa.owner"
	LabelVersion = "oa.version"
	LabelChange  = "oa.change"
	LabelKind    = "oa.kind"

	ManagedOwner            = "openspec-apply-orchestrator"
	CurrentOwnershipVersion = "1"
	CommitPreparationKind   = "commit-preparation"
)

var (
	ErrInvalidIdentifier           = errors.New("некорректный идентификатор")
	ErrInvalidOwnership            = errors.New("некорректная принадлежность собственной сессии")
	ErrUnsupportedOwnershipVersion = errors.New("неподдерживаемая версия принадлежности собственной сессии")
	ErrContradictorySessionState   = errors.New("противоречивое состояние собственной сессии")
)

type ChangeKey struct {
	value string
}

func NewChangeKey(value string) (ChangeKey, error) {
	if err := validateIdentifier("ключ change", value); err != nil {
		return ChangeKey{}, err
	}
	return ChangeKey{value: value}, nil
}

func (key ChangeKey) String() string {
	return key.value
}

type WorkspaceID struct {
	value string
}

func NewWorkspaceID(value string) (WorkspaceID, error) {
	if err := validateIdentifier("идентификатор workspace", value); err != nil {
		return WorkspaceID{}, err
	}
	return WorkspaceID{value: value}, nil
}

func (id WorkspaceID) String() string {
	return id.value
}

type SessionID struct {
	value string
}

func NewSessionID(value string) (SessionID, error) {
	if err := validateIdentifier("идентификатор сессии", value); err != nil {
		return SessionID{}, err
	}
	return SessionID{value: value}, nil
}

func (id SessionID) String() string {
	return id.value
}

func validateIdentifier(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s", ErrInvalidIdentifier, name)
	}
	return nil
}

type UntrustedOwnSession struct {
	ID                string
	WorkspaceID       string
	Status            string
	Labels            map[string]string
	RequiresAttention bool
	AttentionReason   string
}

type ManagedSession struct {
	id          SessionID
	workspaceID WorkspaceID
	changeKey   ChangeKey
}

func (session ManagedSession) ID() SessionID {
	return session.id
}

func (session ManagedSession) WorkspaceID() WorkspaceID {
	return session.workspaceID
}

func (session ManagedSession) ChangeKey() ChangeKey {
	return session.changeKey
}

type OwnSessionObservation interface {
	isOwnSessionObservation()
}

type NoActiveOwnSession struct{}

func (NoActiveOwnSession) isOwnSessionObservation() {}

type WorkingOwnSession struct {
	Session ManagedSession
}

func (WorkingOwnSession) isOwnSessionObservation() {}

type OwnSessionAwaitingAction struct {
	Session ManagedSession
}

func (OwnSessionAwaitingAction) isOwnSessionObservation() {}

type ObservedOwnSessionClosed struct {
	Session ManagedSession
}

func (ObservedOwnSessionClosed) isOwnSessionObservation() {}

type AmbiguousOwnSessions struct {
	Sessions []ManagedSession
}

func (AmbiguousOwnSessions) isOwnSessionObservation() {}

type sessionState uint8

const (
	sessionWorking sessionState = iota + 1
	sessionAwaitingAction
	sessionClosed
)

type validatedSession struct {
	session ManagedSession
	state   sessionState
}

func ObserveOwnSessions(expectedChange ChangeKey, rawSessions []UntrustedOwnSession) (OwnSessionObservation, error) {
	if expectedChange.value == "" {
		return nil, fmt.Errorf("%w: пустой доверенный ключ change", ErrInvalidIdentifier)
	}

	validated := make([]validatedSession, 0, len(rawSessions))
	for index, raw := range rawSessions {
		session, err := validateOwnSession(expectedChange, raw)
		if err != nil {
			return nil, fmt.Errorf("сессия %d: %w", index+1, err)
		}
		validated = append(validated, session)
	}

	switch len(validated) {
	case 0:
		return NoActiveOwnSession{}, nil
	case 1:
		return observationFor(validated[0]), nil
	default:
		sessions := make([]ManagedSession, 0, len(validated))
		for _, item := range validated {
			sessions = append(sessions, item.session)
		}
		return AmbiguousOwnSessions{Sessions: sessions}, nil
	}
}

func validateOwnSession(expectedChange ChangeKey, raw UntrustedOwnSession) (validatedSession, error) {
	if err := validateOwnershipLabels(expectedChange, raw.Labels); err != nil {
		return validatedSession{}, err
	}

	id, err := NewSessionID(raw.ID)
	if err != nil {
		return validatedSession{}, err
	}
	workspaceID, err := NewWorkspaceID(raw.WorkspaceID)
	if err != nil {
		return validatedSession{}, err
	}

	state, err := validateSessionState(raw)
	if err != nil {
		return validatedSession{}, err
	}

	return validatedSession{
		session: ManagedSession{id: id, workspaceID: workspaceID, changeKey: expectedChange},
		state:   state,
	}, nil
}

func validateOwnershipLabels(expectedChange ChangeKey, labels map[string]string) error {
	if labels[LabelOwner] != ManagedOwner {
		return fmt.Errorf("%w: метка %s", ErrInvalidOwnership, LabelOwner)
	}

	version, ok := labels[LabelVersion]
	if !ok || version == "" {
		return fmt.Errorf("%w: метка %s", ErrInvalidOwnership, LabelVersion)
	}
	if version != CurrentOwnershipVersion {
		return fmt.Errorf("%w: %q", ErrUnsupportedOwnershipVersion, version)
	}

	if labels[LabelChange] != expectedChange.String() {
		return fmt.Errorf("%w: метка %s", ErrInvalidOwnership, LabelChange)
	}
	if labels[LabelKind] != CommitPreparationKind {
		return fmt.Errorf("%w: метка %s", ErrInvalidOwnership, LabelKind)
	}
	return nil
}

func validateSessionState(raw UntrustedOwnSession) (sessionState, error) {
	if raw.AttentionReason != "" && !raw.RequiresAttention {
		return 0, fmt.Errorf("%w: причина участия без признака участия", ErrContradictorySessionState)
	}
	if raw.RequiresAttention && !knownAttentionReason(raw.AttentionReason) {
		return 0, fmt.Errorf("%w: неизвестная причина участия %q", ErrContradictorySessionState, raw.AttentionReason)
	}

	switch raw.Status {
	case "initializing", "running":
		if raw.RequiresAttention {
			return 0, fmt.Errorf("%w: работа и потребность в действии", ErrContradictorySessionState)
		}
		return sessionWorking, nil
	case "idle":
		return sessionAwaitingAction, nil
	case "error":
		if raw.RequiresAttention && raw.AttentionReason != "error" {
			return 0, fmt.Errorf("%w: ошибка с причиной %q", ErrContradictorySessionState, raw.AttentionReason)
		}
		return sessionAwaitingAction, nil
	case "closed":
		if raw.RequiresAttention {
			return 0, fmt.Errorf("%w: закрытие и потребность в действии", ErrContradictorySessionState)
		}
		return sessionClosed, nil
	default:
		return 0, fmt.Errorf("%w: неизвестное состояние %q", ErrContradictorySessionState, raw.Status)
	}
}

func knownAttentionReason(reason string) bool {
	switch reason {
	case "finished", "error", "permission":
		return true
	default:
		return false
	}
}

func observationFor(session validatedSession) OwnSessionObservation {
	switch session.state {
	case sessionWorking:
		return WorkingOwnSession{Session: session.session}
	case sessionAwaitingAction:
		return OwnSessionAwaitingAction{Session: session.session}
	case sessionClosed:
		return ObservedOwnSessionClosed{Session: session.session}
	default:
		panic("непроверенное состояние собственной сессии")
	}
}
