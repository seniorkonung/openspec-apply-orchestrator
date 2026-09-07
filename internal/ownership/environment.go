package ownership

import "errors"

var (
	ErrUnsupportedPlatform   = errors.New("операционная система не поддерживается")
	ErrInvalidRoot           = errors.New("некорректный корень локальной среды")
	ErrFilesystemInspection  = errors.New("не удалось определить файловую систему")
	ErrUnsupportedFilesystem = errors.New("файловая система не поддерживается")
)

type LocalEnvironment struct {
	workingRoot string
	changeRoot  string
}
