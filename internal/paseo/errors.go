package paseo

import (
	"errors"

	"github.com/seniorkonung/openspec-apply-orchestrator/internal/paseo/internal/paseocli"
)

var (
	ErrExecutableNotFound              = paseocli.ErrExecutableNotFound
	ErrInvalidRunnerConfig             = paseocli.ErrInvalidRunnerConfig
	ErrCommandStart                    = paseocli.ErrCommandStart
	ErrCommandExit                     = paseocli.ErrCommandExit
	ErrCommandTimeout                  = paseocli.ErrCommandTimeout
	ErrCommandCanceled                 = paseocli.ErrCommandCanceled
	ErrStdoutLimit                     = paseocli.ErrStdoutLimit
	ErrStderrLimit                     = paseocli.ErrStderrLimit
	ErrEmptyOutput                     = paseocli.ErrEmptyOutput
	ErrTruncatedJSON                   = paseocli.ErrTruncatedJSON
	ErrUnexpectedJSON                  = paseocli.ErrUnexpectedJSON
	ErrUnexpectedVersionOutput         = paseocli.ErrUnexpectedVersionOutput
	ErrIncompatibleCLIVersion          = paseocli.ErrIncompatibleCLIVersion
	ErrInconsistentCLIVersion          = paseocli.ErrInconsistentCLIVersion
	ErrIncompatibleDaemonVersion       = paseocli.ErrIncompatibleDaemonVersion
	ErrDaemonNotLocal                  = paseocli.ErrDaemonNotLocal
	ErrDaemonUnavailable               = paseocli.ErrDaemonUnavailable
	ErrDaemonOwnerMismatch             = paseocli.ErrDaemonOwnerMismatch
	ErrInvalidServerID                 = paseocli.ErrInvalidServerID
	ErrCurrentIdentity                 = errors.New("не удалось определить локального владельца paseo daemon")
	ErrInvalidDirectoryQuery           = errors.New("некорректный запрос к каталогу Paseo")
	ErrInvalidWorkingDirectory         = errors.New("некорректный рабочий каталог")
	ErrInvalidSessionSettings          = errors.New("некорректные настройки сессии Paseo")
	ErrProviderNotFound                = errors.New("провайдер отсутствует в каталоге Paseo")
	ErrUnsupportedProvider             = errors.New("провайдер не имеет проверенного режима полного доступа")
	ErrProviderUnavailable             = errors.New("провайдер Paseo недоступен")
	ErrModelNotFound                   = errors.New("модель отсутствует в каталоге Paseo")
	ErrReasoningNotFound               = errors.New("reasoning отсутствует в каталоге Paseo")
	ErrInvalidInitialPrompt            = errors.New("некорректный первоначальный промпт")
	ErrRunOutcomeUnknown               = errors.New("исход создания сессии Paseo не определён")
	ErrSessionStillRunning             = errors.New("ход сессии Paseo ещё не завершён")
	ErrArchiveNotConfirmed             = errors.New("архивирование сессии Paseo не подтверждено")
	ErrArchiveOutcomeUnknown           = errors.New("исход архивирования сессии Paseo не определён")
	ErrCorruptSessionOwnership         = errors.New("признаки принадлежности собственной сессии повреждены")
	ErrSessionIdentityMismatch         = paseocli.ErrSessionIdentityMismatch
	ErrSessionWorkingDirectoryMismatch = errors.New("собственная сессия относится к другому рабочему каталогу")
	ErrForeignSessionParent            = errors.New("сессия имеет чужого родителя")
	ErrInvalidWaitSessionID            = paseocli.ErrInvalidWaitSessionID
	ErrWaitSessionIdentityMismatch     = paseocli.ErrWaitSessionIdentityMismatch
)

type CommandExitError = paseocli.CommandExitError
