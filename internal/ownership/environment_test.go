//go:build linux

package ownership

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestЛокальнаяСредаКанонизируетКорни(t *testing.T) {
	base := t.TempDir()
	workingRoot := filepath.Join(base, "working")
	changeRoot := filepath.Join(workingRoot, "openspec", "changes", "change-a")
	if err := os.MkdirAll(changeRoot, 0o755); err != nil {
		t.Fatalf("создать корень change: %v", err)
	}

	workingAlias := filepath.Join(base, "working-alias")
	changeAlias := filepath.Join(base, "change-alias")
	if err := os.Symlink(workingRoot, workingAlias); err != nil {
		t.Fatalf("создать ссылку на рабочий корень: %v", err)
	}
	if err := os.Symlink(changeRoot, changeAlias); err != nil {
		t.Fatalf("создать ссылку на корень change: %v", err)
	}

	environment, err := CheckLocalEnvironment(workingAlias, changeAlias)
	if err != nil {
		t.Fatalf("проверить локальную среду: %v", err)
	}
	if environment.workingRoot != workingRoot {
		t.Fatalf("неожиданный рабочий корень: %q", environment.workingRoot)
	}
	if environment.changeRoot != changeRoot {
		t.Fatalf("неожиданный корень change: %q", environment.changeRoot)
	}
}

func TestЛокальнаяСредаОтклоняетНекорректныеКорни(t *testing.T) {
	workingRoot := t.TempDir()
	regularFile := filepath.Join(workingRoot, "file")
	if err := os.WriteFile(regularFile, []byte("data"), 0o600); err != nil {
		t.Fatalf("создать обычный файл: %v", err)
	}

	tests := []struct {
		name        string
		workingRoot string
		changeRoot  string
	}{
		{name: "пустой рабочий корень", workingRoot: "", changeRoot: workingRoot},
		{name: "отсутствующий корень change", workingRoot: workingRoot, changeRoot: filepath.Join(workingRoot, "missing")},
		{name: "корень change не является каталогом", workingRoot: workingRoot, changeRoot: regularFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CheckLocalEnvironment(tt.workingRoot, tt.changeRoot)
			if !errors.Is(err, ErrInvalidRoot) {
				t.Fatalf("ожидалась ошибка корня, получено %v", err)
			}
		})
	}
}

func TestПоддерживаютсяТолькоЯвноПеречисленныеЛокальныеФайловыеСистемы(t *testing.T) {
	supported := []filesystemMagic{
		extFilesystemMagic,
		xfsFilesystemMagic,
		btrfsFilesystemMagic,
		f2fsFilesystemMagic,
		nilfsFilesystemMagic,
		bcachefsFilesystemMagic,
		zfsFilesystemMagic,
		tmpfsFilesystemMagic,
		overlayFilesystemMagic,
	}
	for _, magic := range supported {
		if _, supported := filesystemName(magic); !supported {
			t.Errorf("локальная файловая система 0x%x должна поддерживаться", magic)
		}
	}

	unsupported := []filesystemMagic{
		nfsFilesystemMagic,
		cifsFilesystemMagic,
		smb2FilesystemMagic,
		v9fsFilesystemMagic,
		cephFilesystemMagic,
		fuseFilesystemMagic,
		0x7fff_ffff,
	}
	for _, magic := range unsupported {
		if _, supported := filesystemName(magic); supported {
			t.Errorf("файловая система 0x%x не должна поддерживаться", magic)
		}
	}
}

func TestСетеваяФайловаяСистемаОтклоняетсяСОбъяснением(t *testing.T) {
	err := validateFilesystem("корень change", "/path/to/change", nfsFilesystemMagic)
	if !errors.Is(err, ErrUnsupportedFilesystem) {
		t.Fatalf("ожидалась ошибка файловой системы, получено %v", err)
	}
	if got := err.Error(); got == "" || !containsAll(got, "корень change", "/path/to/change", "nfs") {
		t.Fatalf("ошибка не объясняет источник отказа: %q", got)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
