package paseo

import (
	"errors"
	"fmt"
)

var (
	ErrExecutableNotFound  = errors.New("исполняемый файл paseo не найден")
	ErrInvalidRunnerConfig = errors.New("некорректная конфигурация исполнителя paseo")
	ErrCommandStart        = errors.New("не удалось запустить команду paseo")
	ErrCommandExit         = errors.New("команда paseo завершилась неуспешно")
	ErrCommandTimeout      = errors.New("истёк срок выполнения команды paseo")
	ErrCommandCanceled     = errors.New("выполнение команды paseo отменено")
	ErrStdoutLimit         = errors.New("stdout команды paseo превысил предел")
	ErrStderrLimit         = errors.New("stderr команды paseo превысил предел")
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
