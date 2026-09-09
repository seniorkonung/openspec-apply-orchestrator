package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestПолнаяКонфигурацияСохраняетНепроверенныеНастройкиБезЗначенийПоУмолчанию(t *testing.T) {
	root := newRepositoryRoot(t)
	writeConfig(t, root, `{
  "version": 1,
  "sessions": {
    "commit-preparation": {
      "provider": "codex",
      "model": "gpt-6-astra",
      "reasoning": "high"
    }
  },
  "notifications": {
    "intervention": {
      "type": "ntfy",
      "url": "https://ntfy.example.invalid/project-topic",
      "tokenEnv": "PROJECT_NTFY_TOKEN"
    }
  }
}`)

	loaded, err := Read(root)
	if err != nil {
		t.Fatalf("прочитать конфигурацию: %v", err)
	}
	if loaded.Version() != 1 {
		t.Fatalf("неожиданная версия: %d", loaded.Version())
	}
	settings := loaded.CommitPreparation()
	if settings.Provider() != "codex" || settings.Model() != "gpt-6-astra" {
		t.Fatalf("неожиданные настройки агента: provider=%q model=%q", settings.Provider(), settings.Model())
	}
	if reasoning, present := settings.Reasoning(); !present || reasoning != "high" {
		t.Fatalf("неожиданный reasoning: %q, present=%v", reasoning, present)
	}
	channel := loaded.InterventionChannel()
	if channel.Type() != InterventionChannelTypeNtfy {
		t.Fatalf("неожиданный тип канала: %q", channel.Type())
	}
	if channel.URL() != "https://ntfy.example.invalid/project-topic" {
		t.Fatalf("неожиданный URL ntfy: %q", channel.URL())
	}
	if tokenEnv, present := channel.TokenEnvironment(); !present || tokenEnv != "PROJECT_NTFY_TOKEN" {
		t.Fatalf("неожиданное имя переменной токена: %q, present=%v", tokenEnv, present)
	}
}

func TestНеобязательныеReasoningИТокенНеПодменяютсяDefault(t *testing.T) {
	root := newRepositoryRoot(t)
	writeConfig(t, root, `{
  "version": 1,
  "sessions": {
    "commit-preparation": {
      "provider": "codex",
      "model": "gpt-6-astra"
    }
  },
  "notifications": {
    "intervention": {
      "type": "ntfy",
      "url": "https://ntfy.example.invalid/project-topic"
    }
  }
}`)

	loaded, err := Read(root)
	if err != nil {
		t.Fatalf("прочитать минимальную конфигурацию: %v", err)
	}
	if reasoning, present := loaded.CommitPreparation().Reasoning(); present || reasoning != "" {
		t.Fatalf("reasoning не должен получать default: %q, present=%v", reasoning, present)
	}
	if tokenEnvironment, present := loaded.InterventionChannel().TokenEnvironment(); present || tokenEnvironment != "" {
		t.Fatalf("tokenEnv не должен появляться по умолчанию: %q, present=%v", tokenEnvironment, present)
	}
}

func TestПроизвольныйПровайдерОстаётсяНепровереннымЗначениемДляБудущегоКаталога(t *testing.T) {
	root := newRepositoryRoot(t)
	writeConfig(t, root, `{"version":1,"sessions":{"commit-preparation":{"provider":"user-profile","model":"unknown-model"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic"}}}`)

	loaded, err := Read(root)
	if err != nil {
		t.Fatalf("прочитать синтаксически допустимые сырые настройки: %v", err)
	}
	settings := loaded.CommitPreparation()
	if settings.Provider() != "user-profile" || settings.Model() != "unknown-model" {
		t.Fatalf("сырые настройки неожиданно преобразованы: provider=%q model=%q", settings.Provider(), settings.Model())
	}
}

func TestНеизвестныеПоляОтклоняютсяСТочнымПутём(t *testing.T) {
	tests := []struct {
		name string
		json string
		path string
	}{
		{
			name: "пользовательский режим разрешений",
			json: `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6","mode":"full-access"}}}`,
			path: "sessions.commit-preparation.mode",
		},
		{
			name: "параметры провайдера",
			json: `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6","options":{}}}}`,
			path: "sessions.commit-preparation.options",
		},
		{
			name: "prompt в конфигурации",
			json: `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6","prompt":"commit"}}}`,
			path: "sessions.commit-preparation.prompt",
		},
		{
			name: "прогресс поручения",
			json: `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"progress":1}`,
			path: "progress",
		},
		{
			name: "прежний верхнеуровневый ntfy",
			json: `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"ntfy":{"url":"https://ntfy.example.invalid/topic"}}`,
			path: "ntfy",
		},
		{
			name: "литеральный секрет ntfy",
			json: `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic","token":"secret"}}}`,
			path: "notifications.intervention.token",
		},
		{
			name: "произвольное поле канала",
			json: `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic","headers":{}}}}`,
			path: "notifications.intervention.headers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepositoryRoot(t)
			writeConfig(t, root, tt.json)

			_, err := Read(root)
			assertFieldError(t, err, ErrUnknownField, tt.path)
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("ошибка раскрыла значение неизвестного поля: %v", err)
			}
		})
	}
}

func TestОбязательныеПоляИНекорректныеЗначенияУказываютТочныйПуть(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected error
		path     string
	}{
		{
			name:     "неподдерживаемая версия",
			json:     `{"version":2,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}}}`,
			expected: ErrInvalidValue,
			path:     "version",
		},
		{
			name:     "отсутствует канал участия человека",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}}}`,
			expected: ErrMissingField,
			path:     "notifications.intervention",
		},
		{
			name:     "отсутствует вариант канала",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{}}`,
			expected: ErrMissingField,
			path:     "notifications.intervention",
		},
		{
			name:     "отсутствует тип канала",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"url":"https://ntfy.example.invalid/topic"}}}`,
			expected: ErrMissingField,
			path:     "notifications.intervention.type",
		},
		{
			name:     "тип канала не поддерживается",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"email","url":"https://ntfy.example.invalid/topic"}}}`,
			expected: ErrInvalidValue,
			path:     "notifications.intervention.type",
		},
		{
			name:     "отсутствует provider",
			json:     `{"version":1,"sessions":{"commit-preparation":{"model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic"}}}`,
			expected: ErrMissingField,
			path:     "sessions.commit-preparation.provider",
		},
		{
			name:     "отсутствует model",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic"}}}`,
			expected: ErrMissingField,
			path:     "sessions.commit-preparation.model",
		},
		{
			name:     "пустая model",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":" "}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic"}}}`,
			expected: ErrInvalidValue,
			path:     "sessions.commit-preparation.model",
		},
		{
			name:     "reasoning имеет неверный тип",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6","reasoning":null}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic"}}}`,
			expected: ErrInvalidValue,
			path:     "sessions.commit-preparation.reasoning",
		},
		{
			name:     "небезопасная ссылка ntfy",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"http://ntfy.example.invalid/topic"}}}`,
			expected: ErrInvalidValue,
			path:     "notifications.intervention.url",
		},
		{
			name:     "некорректное имя переменной токена",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic","tokenEnv":"TOKEN-NAME"}}}`,
			expected: ErrInvalidValue,
			path:     "notifications.intervention.tokenEnv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepositoryRoot(t)
			writeConfig(t, root, tt.json)

			_, err := Read(root)
			assertFieldError(t, err, tt.expected, tt.path)
		})
	}
}

func TestПовторяющеесяПолеПовреждённыйJSONИБольшойФайлОтклоняются(t *testing.T) {
	t.Run("повторяющееся поле", func(t *testing.T) {
		root := newRepositoryRoot(t)
		writeConfig(t, root, `{"version":1,"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}}}`)

		_, err := Read(root)
		assertFieldError(t, err, ErrDuplicateField, "version")
	})

	t.Run("повреждённый JSON", func(t *testing.T) {
		root := newRepositoryRoot(t)
		writeConfig(t, root, `{"version":`)

		_, err := Read(root)
		if !errors.Is(err, ErrInvalidJSON) {
			t.Fatalf("ожидался повреждённый JSON, получено %v", err)
		}
	})

	t.Run("некорректный UTF-8", func(t *testing.T) {
		root := newRepositoryRoot(t)
		writeConfig(t, root, "{\"version\":1,\"sessions\":{\"commit-preparation\":{\"provider\":\"\xff\",\"model\":\"gpt-6\"}}}")

		_, err := Read(root)
		if !errors.Is(err, ErrInvalidJSON) {
			t.Fatalf("ожидался некорректный JSON, получено %v", err)
		}
	})

	t.Run("слишком большой файл", func(t *testing.T) {
		root := newRepositoryRoot(t)
		writeConfig(t, root, strings.Repeat(" ", maximumConfigBytes+1))

		_, err := Read(root)
		if !errors.Is(err, ErrConfigTooLarge) {
			t.Fatalf("ожидалось превышение размера, получено %v", err)
		}
	})
}

func TestКонфигурацияЧитаетсяИзКаноническогоКорня(t *testing.T) {
	realDirectory := t.TempDir()
	link := filepath.Join(t.TempDir(), "repository-link")
	if err := os.Symlink(realDirectory, link); err != nil {
		t.Fatalf("создать символическую ссылку: %v", err)
	}
	root, err := NewRepositoryRoot(link)
	if err != nil {
		t.Fatalf("создать канонический корень: %v", err)
	}
	if root.String() != realDirectory {
		t.Fatalf("неожиданный канонический корень: %q", root.String())
	}
	writeConfig(t, root, `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic"}}}`)

	if _, err := Read(root); err != nil {
		t.Fatalf("прочитать конфигурацию по каноническому корню: %v", err)
	}
}

func TestПарсерКаналаНеЧитаетЗначениеПеременнойОкружения(t *testing.T) {
	const tokenEnvironment = "MISSING_NTFY_TOKEN"
	t.Setenv(tokenEnvironment, "")
	root := newRepositoryRoot(t)
	writeConfig(t, root, `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic","tokenEnv":"MISSING_NTFY_TOKEN"}}}`)

	loaded, err := Read(root)
	if err != nil {
		t.Fatalf("прочитать конфигурацию без значения переменной токена: %v", err)
	}
	if name, present := loaded.InterventionChannel().TokenEnvironment(); !present || name != tokenEnvironment {
		t.Fatalf("ожидалось только имя переменной %q, получено %q, present=%v", tokenEnvironment, name, present)
	}
}

func TestСнимокНеПеречитываетИзменившийсяФайл(t *testing.T) {
	t.Setenv("ORIGINAL_NTFY_TOKEN", "")
	root := newRepositoryRoot(t)
	writeConfig(t, root, `{
  "version": 1,
  "sessions": {
    "commit-preparation": {
      "provider": "codex",
      "model": "gpt-6-astra",
      "reasoning": "high"
    }
  },
  "notifications": {
    "intervention": {
      "type": "ntfy",
      "url": "https://ntfy.example.invalid/original-topic",
      "tokenEnv": "ORIGINAL_NTFY_TOKEN"
    }
  }
}`)

	snapshot := ReadSnapshot(root)
	writeConfig(t, root, `{
  "version": 1,
  "sessions": {
    "commit-preparation": {
      "provider": "changed-provider",
      "model": "changed-model"
    }
  },
  "notifications": {
    "intervention": {
      "type": "ntfy",
      "url": "https://ntfy.example.invalid/changed-topic"
    }
  }
}`)

	settings, err := snapshot.CommitPreparation()
	if err != nil {
		t.Fatalf("получить настройки будущей сессии из снимка: %v", err)
	}
	if settings.Provider() != "codex" || settings.Model() != "gpt-6-astra" {
		t.Fatalf("снимок подменил настройки после изменения файла: provider=%q model=%q", settings.Provider(), settings.Model())
	}
	if reasoning, present := settings.Reasoning(); !present || reasoning != "high" {
		t.Fatalf("снимок подменил reasoning после изменения файла: %q, present=%v", reasoning, present)
	}

	channel, err := snapshot.InterventionChannel()
	if err != nil {
		t.Fatalf("получить канал из снимка: %v", err)
	}
	if channel.URL() != "https://ntfy.example.invalid/original-topic" {
		t.Fatalf("снимок подменил URL после изменения файла: %q", channel.URL())
	}
	if tokenEnvironment, present := channel.TokenEnvironment(); !present || tokenEnvironment != "ORIGINAL_NTFY_TOKEN" {
		t.Fatalf("снимок подменил tokenEnv после изменения файла: %q, present=%v", tokenEnvironment, present)
	}
}

func TestПредставленияСнимкаРазрешаютсяНезависимо(t *testing.T) {
	t.Run("ошибка канала не блокирует настройки будущей сессии", func(t *testing.T) {
		root := newRepositoryRoot(t)
		writeConfig(t, root, `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"email","url":"https://ntfy.example.invalid/topic"}}}`)

		snapshot := ReadSnapshot(root)
		settings, err := snapshot.CommitPreparation()
		if err != nil {
			t.Fatalf("получить независимые настройки будущей сессии: %v", err)
		}
		if settings.Provider() != "codex" || settings.Model() != "gpt-6" {
			t.Fatalf("неожиданные настройки будущей сессии: provider=%q model=%q", settings.Provider(), settings.Model())
		}
		_, err = snapshot.InterventionChannel()
		assertFieldError(t, err, ErrInvalidValue, "notifications.intervention.type")
	})

	t.Run("ошибка настроек будущей сессии не блокирует канал", func(t *testing.T) {
		root := newRepositoryRoot(t)
		writeConfig(t, root, `{"version":1,"sessions":{"commit-preparation":{"provider":"codex"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic"}}}`)

		snapshot := ReadSnapshot(root)
		channel, err := snapshot.InterventionChannel()
		if err != nil {
			t.Fatalf("получить независимый канал: %v", err)
		}
		if channel.Type() != InterventionChannelTypeNtfy {
			t.Fatalf("неожиданный тип канала: %q", channel.Type())
		}
		_, err = snapshot.CommitPreparation()
		assertFieldError(t, err, ErrMissingField, "sessions.commit-preparation.model")
	})
}

func TestСнимокСохраняетТипизированныеОшибкиЧтенияИФормата(t *testing.T) {
	t.Run("файл отсутствует", func(t *testing.T) {
		snapshot := ReadSnapshot(newRepositoryRoot(t))

		if _, err := snapshot.CommitPreparation(); !errors.Is(err, ErrConfigNotFound) {
			t.Fatalf("ожидалась ошибка отсутствующего файла для настроек, получено %v", err)
		}
		if _, err := snapshot.InterventionChannel(); !errors.Is(err, ErrConfigNotFound) {
			t.Fatalf("ожидалась ошибка отсутствующего файла для канала, получено %v", err)
		}
	})

	t.Run("JSON повреждён", func(t *testing.T) {
		root := newRepositoryRoot(t)
		writeConfig(t, root, `{"version":`)
		snapshot := ReadSnapshot(root)

		if _, err := snapshot.CommitPreparation(); !errors.Is(err, ErrInvalidJSON) {
			t.Fatalf("ожидалась ошибка JSON для настроек, получено %v", err)
		}
		if _, err := snapshot.InterventionChannel(); !errors.Is(err, ErrInvalidJSON) {
			t.Fatalf("ожидалась ошибка JSON для канала, получено %v", err)
		}
	})

	tests := []struct {
		name     string
		json     string
		expected error
		path     string
	}{
		{
			name:     "канал отсутствует",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}}}`,
			expected: ErrMissingField,
			path:     "notifications.intervention",
		},
		{
			name:     "тип канала повреждён",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":7,"url":"https://ntfy.example.invalid/topic"}}}`,
			expected: ErrInvalidValue,
			path:     "notifications.intervention.type",
		},
		{
			name:     "URL канала неподдерживаемый",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"http://ntfy.example.invalid/topic"}}}`,
			expected: ErrInvalidValue,
			path:     "notifications.intervention.url",
		},
		{
			name:     "имя переменной токена повреждено",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic","tokenEnv":"TOKEN-NAME"}}}`,
			expected: ErrInvalidValue,
			path:     "notifications.intervention.tokenEnv",
		},
		{
			name:     "неизвестное поле канала",
			json:     `{"version":1,"sessions":{"commit-preparation":{"provider":"codex","model":"gpt-6"}},"notifications":{"intervention":{"type":"ntfy","url":"https://ntfy.example.invalid/topic","token":"secret"}}}`,
			expected: ErrUnknownField,
			path:     "notifications.intervention.token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRepositoryRoot(t)
			writeConfig(t, root, tt.json)

			_, err := ReadSnapshot(root).InterventionChannel()
			assertFieldError(t, err, tt.expected, tt.path)
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("ошибка раскрыла значение неизвестного поля: %v", err)
			}
		})
	}
}

func newRepositoryRoot(t *testing.T) RepositoryRoot {
	t.Helper()
	root, err := NewRepositoryRoot(t.TempDir())
	if err != nil {
		t.Fatalf("создать корень репозитория: %v", err)
	}
	return root
}

func writeConfig(t *testing.T, root RepositoryRoot, content string) {
	t.Helper()
	path := filepath.Join(root.String(), FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("записать конфигурацию: %v", err)
	}
}

func assertFieldError(t *testing.T, err, expected error, expectedPath string) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("ожидалась ошибка %v, получено %v", expected, err)
	}
	var fieldError *FieldError
	if !errors.As(err, &fieldError) {
		t.Fatalf("ожидалась ошибка поля, получено %T: %v", err, err)
	}
	if fieldError.Path != expectedPath {
		t.Fatalf("ожидался путь %q, получен %q", expectedPath, fieldError.Path)
	}
}
