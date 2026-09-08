package orchestrator

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrSourceRead                = errors.New("ошибка чтения источника")
	ErrInvalidSourceReadObstacle = errors.New("некорректное препятствие чтения источника")
)

type ReadSource uint8

const (
	ReadSourceOpenSpec ReadSource = iota + 1
	ReadSourcePaseo
	ReadSourceGit
)

func (source ReadSource) String() string {
	switch source {
	case ReadSourceOpenSpec:
		return "OpenSpec"
	case ReadSourcePaseo:
		return "Paseo"
	case ReadSourceGit:
		return "Git"
	default:
		return "неизвестный источник"
	}
}

func (source ReadSource) valid() bool {
	return source >= ReadSourceOpenSpec && source <= ReadSourceGit
}

type SourceReadObstacle struct {
	source ReadSource
	cause  error
}

func ClassifySourceReadError(ctx context.Context, source ReadSource, cause error) error {
	if cause == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) ||
		errors.Is(cause, ErrReconcileObservationChanged) {
		return cause
	}
	var obstacle *SourceReadObstacle
	if errors.As(cause, &obstacle) {
		return cause
	}
	if !source.valid() {
		return ErrInvalidSourceReadObstacle
	}
	return &SourceReadObstacle{source: source, cause: cause}
}

func (obstacle *SourceReadObstacle) Error() string {
	if obstacle == nil || !obstacle.source.valid() || obstacle.cause == nil {
		return ErrInvalidSourceReadObstacle.Error()
	}
	return fmt.Sprintf("%s %s: %v", ErrSourceRead, obstacle.source, obstacle.cause)
}

func (obstacle *SourceReadObstacle) Unwrap() error {
	if obstacle == nil {
		return nil
	}
	return obstacle.cause
}

func (obstacle *SourceReadObstacle) Is(target error) bool {
	return target == ErrSourceRead
}

func (obstacle *SourceReadObstacle) Source() ReadSource {
	if obstacle == nil {
		return 0
	}
	return obstacle.source
}
