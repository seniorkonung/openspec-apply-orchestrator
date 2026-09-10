package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/config"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/gitstate"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/notify"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/openspec"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/orchestrator"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/ownership"
	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo"
)

type commandReporter struct {
	output  io.Writer
	verbose bool
}

func newCommandReporter(output io.Writer, verbose bool) *commandReporter {
	return &commandReporter{output: output, verbose: verbose}
}

func (reporter *commandReporter) invalidArguments() int {
	reporter.line("Ошибка аргументов: используйте prepare-commits --change <name> [--store <id>] [--verbose].")
	return exitUsageOrConfiguration
}

func (reporter *commandReporter) checkingOpenSpec(change string) {
	reporter.linef("Проверяю OpenSpec change %s.", change)
}

func (reporter *commandReporter) openSpecRead() {
	reporter.technical("OpenSpec change прочитан.")
}

func (reporter *commandReporter) checkingWorkingTree() {
	reporter.line("Проверяю рабочий Git.")
}

func (reporter *commandReporter) workingTreeOpened() {
	reporter.technical("рабочий Git открыт.")
}

func (reporter *commandReporter) configurationSnapshotRead() {
	reporter.technical("снимок конфигурации получен.")
}

func (reporter *commandReporter) checkingLocalOwnership() {
	reporter.line("Проверяю локальную среду и владение change.")
}

func (reporter *commandReporter) localOwnershipAcquired() {
	reporter.technical("локальное владение change установлено.")
}

func (reporter *commandReporter) checkingPaseo() {
	reporter.line("Проверяю совместимость локального Paseo.")
}

func (reporter *commandReporter) paseoCompatible() {
	reporter.technical("совместимость Paseo подтверждена.")
}

func (reporter *commandReporter) monitoringStarted() {
	reporter.line("Сопровождение подготовки коммитов запущено.")
}

func (reporter *commandReporter) workspaceRead() {
	reporter.technical("состояние workspace прочитано.")
}

func (reporter *commandReporter) ownSessionsRead() {
	reporter.technical("список собственных сессий прочитан.")
}

func (reporter *commandReporter) ownSessionRead() {
	reporter.technical("состояние собственной сессии прочитано.")
}

func (reporter *commandReporter) workingTreeRead() {
	reporter.technical("состояние Git прочитано.")
}

func (reporter *commandReporter) checkingNewSessionInputs() {
	reporter.line("Проверяю конфигурацию и каталог новой сессии.")
}

func (reporter *commandReporter) newSessionInputsChecked() {
	reporter.technical("конфигурация и каталог новой сессии проверены.")
}

func (reporter *commandReporter) creatingWorkspace() {
	reporter.line("Создаю workspace выбранного change.")
}

func (reporter *commandReporter) workspaceCreated() {
	reporter.line("Workspace выбранного change создан.")
}

func (reporter *commandReporter) creatingSession() {
	reporter.line("Создаю собственную сессию подготовки коммитов.")
}

func (reporter *commandReporter) sessionCreated(id orchestrator.SessionID) {
	reporter.linef("Создана собственная сессия %s.", id.String())
}

func (reporter *commandReporter) sessionRecovered(id orchestrator.SessionID) {
	reporter.linef("Восстановлена собственная сессия %s.", id.String())
}

func (reporter *commandReporter) waitingForTurn(id orchestrator.SessionID) {
	reporter.linef("Ожидаю завершения хода сессии %s.", id.String())
}

func (reporter *commandReporter) waitHeartbeat(id orchestrator.SessionID) {
	reporter.linef("Ожидание продолжается: сессия %s.", id.String())
}

func (reporter *commandReporter) archivingSession(id orchestrator.SessionID) {
	reporter.linef("Архивирую собственную сессию %s.", id.String())
}

func (reporter *commandReporter) interventionRequired(event notify.Intervention) {
	reporter.linef(
		"Требуется участие человека: %s. Сессия: %s",
		interventionReason(event.Reason()),
		event.SessionLink().String(),
	)
}

func (reporter *commandReporter) retryingInterventionDelivery() {
	reporter.line("Повторяю доставку уведомления для текущей сессии.")
}

func (reporter *commandReporter) interventionConfigurationFailed(err error) {
	reporter.linef(
		"Уведомление не доставлено: %s. Исправьте конфигурацию и перезапустите CLI; сопровождение той же сессии продолжается.",
		publicErrorText(err),
	)
}

func (reporter *commandReporter) interventionDeliveryFailed() {
	reporter.line("Уведомление не доставлено; сопровождение той же сессии продолжается.")
}

func (reporter *commandReporter) interventionDelivered() {
	reporter.line("Уведомление доставлено; сопровождение той же сессии продолжается.")
}

func (reporter *commandReporter) changeLockReleaseFailed() {
	reporter.line("Ошибка: не удалось освободить локальное владение change.")
}

func (reporter *commandReporter) outcome(runtime paseoRuntime, outcome orchestrator.CommitPreparationOutcome) int {
	switch result := outcome.(type) {
	case orchestrator.NoCommitPreparationNeeded:
		reporter.line("Поручение не требуется: незакоммиченных изменений нет.")
		return exitSuccess
	case orchestrator.CommitPreparationCompleted:
		reporter.linef("Подготовка коммитов завершена; сессия %s закрыта.", result.SessionID().String())
		return exitSuccess
	case orchestrator.HumanInterventionRequired:
		reporter.linef(
			"Требуется участие человека: %s. Сессия: %s",
			attentionReason(result.Reason()),
			runtime.SessionLink(result.SessionID()),
		)
		return exitObstacle
	case orchestrator.ClosedSessionWithChanges:
		reporter.linef("Сессия %s закрыта, но Git содержит незакоммиченные изменения.", result.SessionID().String())
		return exitObstacle
	default:
		reporter.line("Ошибка: ядро вернуло неизвестный результат.")
		return exitObstacle
	}
}

func (reporter *commandReporter) commandError(err error) int {
	if errors.Is(err, context.Canceled) {
		reporter.line("Сопровождение прервано; сессия Paseo остаётся доступной.")
		return exitObstacle
	}
	reporter.linef("Ошибка: %s.", publicErrorText(err))
	if isConfigurationError(err) {
		return exitUsageOrConfiguration
	}
	return exitObstacle
}

func (reporter *commandReporter) technical(message string) {
	if reporter.verbose {
		reporter.line("Подробно: " + message)
	}
}

func (reporter *commandReporter) linef(format string, arguments ...any) {
	reporter.line(fmt.Sprintf(format, arguments...))
}

func (reporter *commandReporter) line(message string) {
	if reporter == nil || reporter.output == nil {
		return
	}
	fmt.Fprintln(reporter.output, singleLine(message))
}

func singleLine(value string) string {
	withoutControls := strings.Map(func(symbol rune) rune {
		if unicode.IsControl(symbol) {
			return ' '
		}
		return symbol
	}, value)
	return strings.Join(strings.Fields(withoutControls), " ")
}

func publicErrorText(err error) string {
	if err == nil {
		return "неизвестная ошибка"
	}
	known := knownPublicError(err)
	var source *orchestrator.SourceReadObstacle
	if errors.As(err, &source) {
		message := fmt.Sprintf("ошибка чтения источника %s", source.Source())
		if known != nil {
			message += ": " + known.Error()
		}
		return singleLine(message)
	}
	if known == nil {
		return "подробности внешней ошибки скрыты для защиты данных"
	}
	var field *config.FieldError
	if errors.As(err, &field) && validConfigurationPath(field.Path) {
		return singleLine(known.Error() + ": " + field.Path)
	}
	return singleLine(known.Error())
}

func knownPublicError(err error) error {
	for _, target := range publicErrors {
		if errors.Is(err, target) {
			return target
		}
	}
	return nil
}

func validConfigurationPath(path string) bool {
	if path == "" {
		return false
	}
	for _, symbol := range path {
		if (symbol >= 'a' && symbol <= 'z') || (symbol >= 'A' && symbol <= 'Z') ||
			(symbol >= '0' && symbol <= '9') || symbol == '.' || symbol == '-' || symbol == '_' {
			continue
		}
		return false
	}
	return true
}

var publicErrors = []error{
	config.ErrInvalidRoot,
	config.ErrConfigNotFound,
	config.ErrReadConfig,
	config.ErrConfigTooLarge,
	config.ErrInvalidJSON,
	config.ErrUnknownField,
	config.ErrMissingField,
	config.ErrInvalidValue,
	config.ErrDuplicateField,
	gitstate.ErrExecutableNotFound,
	gitstate.ErrCommandStart,
	gitstate.ErrCommandExit,
	gitstate.ErrCommandTimeout,
	gitstate.ErrCommandCanceled,
	gitstate.ErrStdoutLimit,
	gitstate.ErrStderrLimit,
	gitstate.ErrInvalidRepository,
	gitstate.ErrNotRepository,
	gitstate.ErrWorkingContextChanged,
	gitstate.ErrUnexpectedOutput,
	openspec.ErrExecutableNotFound,
	openspec.ErrInvalidRunner,
	openspec.ErrCommandStart,
	openspec.ErrCommandExit,
	openspec.ErrCommandTimeout,
	openspec.ErrCommandCanceled,
	openspec.ErrStdoutLimit,
	openspec.ErrStderrLimit,
	openspec.ErrInvalidSelection,
	openspec.ErrInvalidClient,
	openspec.ErrEmptyOutput,
	openspec.ErrTruncatedJSON,
	openspec.ErrUnexpectedJSON,
	openspec.ErrChangeUnavailable,
	openspec.ErrInconsistentContext,
	orchestrator.ErrInvalidCommitPreparationIdentity,
	orchestrator.ErrInvalidReconciler,
	orchestrator.ErrUnexpectedObservation,
	orchestrator.ErrAmbiguousManagedWorkspaces,
	orchestrator.ErrAmbiguousOwnSessions,
	orchestrator.ErrSessionNeedsAction,
	orchestrator.ErrReconcileObservationChanged,
	orchestrator.ErrInvalidIdentifier,
	orchestrator.ErrInvalidOwnership,
	orchestrator.ErrUnsupportedOwnershipVersion,
	orchestrator.ErrContradictorySessionState,
	orchestrator.ErrInvalidSourceReadObstacle,
	ownership.ErrUnsupportedPlatform,
	ownership.ErrInvalidRoot,
	ownership.ErrFilesystemInspection,
	ownership.ErrUnsupportedFilesystem,
	ownership.ErrChangeBusy,
	ownership.ErrChangeLock,
	ownership.ErrChangeRootChanged,
	paseo.ErrExecutableNotFound,
	paseo.ErrInvalidRunnerConfig,
	paseo.ErrCommandStart,
	paseo.ErrCommandExit,
	paseo.ErrCommandTimeout,
	paseo.ErrCommandCanceled,
	paseo.ErrStdoutLimit,
	paseo.ErrStderrLimit,
	paseo.ErrEmptyOutput,
	paseo.ErrTruncatedJSON,
	paseo.ErrUnexpectedVersionOutput,
	paseo.ErrIncompatibleCLIVersion,
	paseo.ErrInconsistentCLIVersion,
	paseo.ErrIncompatibleDaemonVersion,
	paseo.ErrDaemonNotLocal,
	paseo.ErrDaemonUnavailable,
	paseo.ErrDaemonOwnerMismatch,
	paseo.ErrInvalidServerID,
	paseo.ErrCurrentIdentity,
	paseo.ErrInvalidDirectoryQuery,
	paseo.ErrInvalidWorkingDirectory,
	paseo.ErrInvalidSessionSettings,
	paseo.ErrProviderNotFound,
	paseo.ErrUnsupportedProvider,
	paseo.ErrProviderUnavailable,
	paseo.ErrModelNotFound,
	paseo.ErrReasoningNotFound,
	paseo.ErrInvalidInitialPrompt,
	paseo.ErrRunOutcomeUnknown,
	paseo.ErrSessionStillRunning,
	paseo.ErrArchiveNotConfirmed,
	paseo.ErrArchiveOutcomeUnknown,
	paseo.ErrCorruptSessionOwnership,
	paseo.ErrSessionIdentityMismatch,
	paseo.ErrSessionWorkingDirectoryMismatch,
	paseo.ErrForeignSessionParent,
	paseo.ErrInvalidWaitSessionID,
	paseo.ErrWaitSessionIdentityMismatch,
	paseo.ErrInvalidReconcileGateway,
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
	case orchestrator.SessionPermissionRequested:
		return "Paseo запросил разрешение"
	default:
		return "причина не распознана"
	}
}

func interventionReason(reason notify.Reason) string {
	switch reason {
	case notify.ReasonTurnFinished:
		return "ход агента завершён, но Git остаётся изменённым"
	case notify.ReasonAgentError:
		return "агент сообщил об ошибке"
	case notify.ReasonPermissionRequested:
		return "Paseo запросил разрешение"
	default:
		return "причина не распознана"
	}
}
