package paseo

import (
	"errors"
	"fmt"
)

var (
	ErrExecutableNotFound              = errors.New("исполняемый файл paseo не найден")
	ErrInvalidRunnerConfig             = errors.New("некорректная конфигурация исполнителя paseo")
	ErrCommandStart                    = errors.New("не удалось запустить команду paseo")
	ErrCommandExit                     = errors.New("команда paseo завершилась неуспешно")
	ErrCommandTimeout                  = errors.New("истёк срок выполнения команды paseo")
	ErrCommandCanceled                 = errors.New("выполнение команды paseo отменено")
	ErrStdoutLimit                     = errors.New("stdout команды paseo превысил предел")
	ErrStderrLimit                     = errors.New("stderr команды paseo превысил предел")
	ErrEmptyOutput                     = errors.New("команда paseo вернула пустой вывод")
	ErrTruncatedJSON                   = errors.New("команда paseo вернула обрезанный JSON")
	ErrUnexpectedJSON                  = errors.New("команда paseo вернула неожиданный JSON")
	ErrUnexpectedVersionOutput         = errors.New("команда paseo вернула неожиданный вывод версии")
	ErrIncompatibleCLIVersion          = errors.New("версия paseo CLI несовместима")
	ErrInconsistentCLIVersion          = errors.New("версии paseo CLI противоречат друг другу")
	ErrIncompatibleDaemonVersion       = errors.New("версия paseo daemon несовместима")
	ErrDaemonNotLocal                  = errors.New("локальный paseo daemon не работает")
	ErrDaemonUnavailable               = errors.New("paseo daemon недоступен")
	ErrDaemonOwnerMismatch             = errors.New("paseo daemon принадлежит другому пользователю")
	ErrInvalidServerID                 = errors.New("paseo status не содержит допустимый serverId")
	ErrCurrentIdentity                 = errors.New("не удалось определить локального владельца paseo daemon")
	ErrInvalidDirectoryQuery           = errors.New("некорректный запрос к каталогу Paseo")
	ErrInvalidWorkingDirectory         = errors.New("некорректный рабочий каталог")
	ErrInvalidSessionSettings          = errors.New("некорректные настройки сессии Paseo")
	ErrInvalidInitialPrompt            = errors.New("некорректный первоначальный промпт")
	ErrRunOutcomeUnknown               = errors.New("исход создания сессии Paseo не определён")
	ErrSessionStillRunning             = errors.New("ход сессии Paseo ещё не завершён")
	ErrArchiveNotConfirmed             = errors.New("архивирование сессии Paseo не подтверждено")
	ErrArchiveOutcomeUnknown           = errors.New("исход архивирования сессии Paseo не определён")
	ErrCorruptSessionOwnership         = errors.New("признаки принадлежности собственной сессии повреждены")
	ErrSessionIdentityMismatch         = errors.New("inspect вернул другую сессию")
	ErrSessionWorkingDirectoryMismatch = errors.New("собственная сессия относится к другому рабочему каталогу")
	ErrForeignSessionParent            = errors.New("сессия имеет чужого родителя")
)

type CommandExitError struct {
	Command     string
	ExitCode    int
	StdoutBytes int64
	StderrBytes int64
}

func (err *CommandExitError) Error() string {
	return fmt.Sprintf(
		"%s: команда %s, код %d, stdout %d байт, stderr %d байт",
		ErrCommandExit,
		err.Command,
		err.ExitCode,
		err.StdoutBytes,
		err.StderrBytes,
	)
}

func (err *CommandExitError) Unwrap() error {
	return ErrCommandExit
}
