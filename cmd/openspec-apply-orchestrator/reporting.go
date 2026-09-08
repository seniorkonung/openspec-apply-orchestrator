package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/openspec"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo"
)

func reportOutcome(output io.Writer, serverID string, outcome orchestrator.CommitPreparationOutcome) int {
	switch result := outcome.(type) {
	case orchestrator.NoCommitPreparationNeeded:
		fmt.Fprintln(output, "Поручение не требуется: незакоммиченных изменений нет.")
		return exitSuccess
	case orchestrator.CommitPreparationCompleted:
		fmt.Fprintf(output, "Подготовка коммитов завершена; сессия %s закрыта.\n", result.SessionID().String())
		return exitSuccess
	case orchestrator.HumanInterventionRequired:
		fmt.Fprintf(
			output,
			"Требуется участие человека: %s. Сессия: %s\n",
			attentionReason(result.Reason()),
			sessionLink(serverID, result.SessionID()),
		)
		return exitObstacle
	case orchestrator.ClosedSessionWithChanges:
		fmt.Fprintf(output, "Сессия %s закрыта, но Git содержит незакоммиченные изменения.\n", result.SessionID().String())
		return exitObstacle
	default:
		fmt.Fprintln(output, "Ошибка: ядро вернуло неизвестный результат.")
		return exitObstacle
	}
}

func reportCommandError(output io.Writer, err error) int {
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(output, "Сопровождение прервано; сессия Paseo остаётся доступной.")
		return exitObstacle
	}
	fmt.Fprintf(output, "Ошибка: %v\n", err)
	if isConfigurationError(err) {
		return exitUsageOrConfiguration
	}
	return exitObstacle
}

func isConfigurationError(err error) bool {
	configurationErrors := []error{
		config.ErrInvalidRoot,
		config.ErrConfigNotFound,
		config.ErrReadConfig,
		config.ErrConfigTooLarge,
		config.ErrInvalidJSON,
		config.ErrUnknownField,
		config.ErrMissingField,
		config.ErrInvalidValue,
		config.ErrDuplicateField,
		openspec.ErrInvalidSelection,
		paseo.ErrInvalidSessionSettings,
		paseo.ErrProviderNotFound,
		paseo.ErrUnsupportedProvider,
		paseo.ErrProviderUnavailable,
		paseo.ErrModelNotFound,
		paseo.ErrReasoningNotFound,
	}
	for _, target := range configurationErrors {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func attentionReason(reason orchestrator.SessionAttentionReason) string {
	switch reason {
	case orchestrator.SessionTurnFinished:
		return "ход агента завершён, но Git остаётся изменённым"
	case orchestrator.SessionAgentError:
		return "агент сообщил об ошибке"
	case orchestrator.SessionPermissionCompatibilityViolation:
		return "Paseo запросил разрешение вопреки режиму полного доступа"
	default:
		return "причина не распознана"
	}
}

func sessionLink(serverID string, sessionID orchestrator.SessionID) string {
	return "paseo://h/" + url.PathEscape(serverID) + "/agent/" + url.PathEscape(sessionID.String())
}
