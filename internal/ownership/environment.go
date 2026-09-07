package ownership

import (
	"errors"
	"os"
)

var (
	ErrUnsupportedPlatform   = errors.New("операционная система не поддерживается")
	ErrInvalidRoot           = errors.New("некорректный корень локальной среды")
	ErrFilesystemInspection  = errors.New("не удалось определить файловую систему")
	ErrUnsupportedFilesystem = errors.New("файловая система не поддерживается")
	ErrChangeBusy            = errors.New("выбранный change уже сопровождается другим процессом")
	ErrChangeLock            = errors.New("не удалось установить локальное владение change")
	ErrChangeRootChanged     = errors.New("корень change изменился после проверки")
)

type LocalEnvironment struct {
	workingRoot    string
	changeRoot     string
	changeRootInfo os.FileInfo
}
