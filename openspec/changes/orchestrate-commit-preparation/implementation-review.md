# OpenSpec Implementation Review: orchestrate-commit-preparation

## Assessment

**Format version:** 1
**Result:** Changes needed
**Coverage status:** Complete
**Summary:** Задача 3.16 не полностью подтверждена: helper-бинарники собираются повторно (F5), а безопасный отказ не доказывает неизменность Git (F3).

## Review target

- **Baseline ref:** origin/main
- **Base commit:** 326f4bcbd636b40feae36069b913d79cbac9e141
- **Reviewed head:** 07367033e9f135ee72c7201b6303dcbaa2c70a71
- **Target commits:** ["6d1381b1e30199c17cca2416ddd222e3acd7407e", "bae40894809eed5926e2290275340a426ed9533d", "01093e0254a07f1308ba1053ff39baa21ee653da", "439da2c7f12f5fd9d0f52094c0755dd484c7e1ba", "b5b89257097cc46369f25e2b9d53cfdbae1793be", "33ddc93ce73d58dad478a30b6dd1aa7c214e26b9", "54581c070845863fd622a61582090d68760dd3b8", "58b1128ebdf97f6c8dfa438d8f988273d1cb8743", "2abfb7d31bcaf9e79704f6fb8918c8952093bb23", "07367033e9f135ee72c7201b6303dcbaa2c70a71"]
- **Reviewable paths:** ["cmd/openspec-apply-orchestrator/prepare_commits_delivery_integration_test.go", "cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go", "cmd/openspec-apply-orchestrator/prepare_commits_intervention_integration_test.go", "cmd/openspec-apply-orchestrator/prepare_commits_wait_integration_test.go", "docs/adr/0004-system-tests-cover-user-journeys.md", "docs/adr/README.md", "docs/development/paseo-compatibility.md", "docs/development/paseo-upgrade.md", "docs/development/testing.md", "internal/orchestrator/recovery_integration_test.go", "internal/paseo/recovery_integration_test.go", "internal/paseo/testpaseo/cmd/reconcile/main.go", "internal/paseo/testpaseo/daemon.go", "mise.toml", "openspec/changes/orchestrate-commit-preparation/adr.md", "openspec/changes/orchestrate-commit-preparation/design.md", "openspec/changes/orchestrate-commit-preparation/plan.md", "openspec/changes/orchestrate-commit-preparation/tasks.md"]
- **OpenSpec change:** orchestrate-commit-preparation
- **OpenSpec schema:** intent-driven
- **Target scope:** Complete pre-push range
- **Baseline freshness:** Local ref state; no fetch performed
- **Planning evidence paths:** ["docs/adr/0004-system-tests-cover-user-journeys.md", "openspec/changes/orchestrate-commit-preparation/adr.md", "openspec/changes/orchestrate-commit-preparation/design.md", "openspec/changes/orchestrate-commit-preparation/plan.md", "openspec/changes/orchestrate-commit-preparation/tasks.md"]
- **Excluded worktree state:** ["openspec/changes/orchestrate-commit-preparation/implementation-review.md", "openspec/changes/orchestrate-commit-preparation/tasks.md"]

## Reviewed increment

### U1 · Послойные доказательства и конечный каталог production-сценариев

- **Work items:** ["3.15 Вернуть технические варианты процессных проверок владеющим уровням", "3.16 Свести production-стенд к конечному каталогу пользовательских сценариев"]
- **Requirements and scenarios:** ["design: доказательства принадлежат уровням, а системный стенд — пользовательским сценариям", "ADR 0004: системные тесты проверяют пользовательские сценарии"]
- **Affected boundary:** Разработчик, запускающий обычные проверки, квалификацию внешнего контракта Paseo и production-каталог через реальные Git, OpenSpec CLI, локальный Paseo daemon и HTTP-приёмники.
- **Implementation target:** ["cmd/openspec-apply-orchestrator/prepare_commits_delivery_integration_test.go", "cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go", "cmd/openspec-apply-orchestrator/prepare_commits_intervention_integration_test.go", "cmd/openspec-apply-orchestrator/prepare_commits_wait_integration_test.go", "docs/adr/README.md", "docs/development/paseo-compatibility.md", "docs/development/paseo-upgrade.md", "docs/development/testing.md", "internal/orchestrator/recovery_integration_test.go", "internal/paseo/recovery_integration_test.go", "internal/paseo/testpaseo/cmd/reconcile/main.go", "internal/paseo/testpaseo/daemon.go", "mise.toml"]
- **Applicable constraints and non-goals:** Технические матрицы остаются у нижних уровней; настоящий daemon квалифицируется одним связным потоком; production-каталог содержит только целостные пользовательские истории, один раз собирает production-бинарник, изолирует изменяемые ресурсы, выполняется последовательно, ограничивает каждый путь deadline и гарантированно освобождает принадлежащие стенду процессы. Production-поведение не меняется.
- **Excluded change scope:** Эксплуатационное руководство, ручная приёмка на пользовательском устройстве и итоговое подтверждение готовности задач 3.11–3.13.

### U2 · Восстановление доставки ntfy в том же поручении

- **Work items:** ["3.10 Доказать устойчивость ntfy одним пользовательским путём и послойными проверками (задача остаётся открытой)"]
- **Requirements and scenarios:** ["commit-preparation-notifications: неуспешная доставка сохраняет снимок процесса, а отдельный перезапуск применяет новый снимок без дубликата успешного неизменного эпизода"]
- **Affected boundary:** Пользователь, который после локально объяснённого сбоя доставки исправляет конфигурацию, перезапускает команду и продолжает то же поручение через уведомление той же собственной сессии.
- **Implementation target:** ["cmd/openspec-apply-orchestrator/prepare_commits_delivery_integration_test.go"]
- **Applicable constraints and non-goals:** Один сквозной путь подтверждает композицию production-процесса; матрицы ошибок конфигурации, HTTP, повторов и переходов состояния остаются у `internal/config`, `internal/notify` и `internal/orchestrator`; новый `run`, мутации Paseo, раскрытие приватных данных и повтор успешного неизменного эпизода запрещены.
- **Excluded change scope:** Документирование эксплуатации и ручная проверка уведомления принадлежат задачам 3.11–3.12.

## Pass coverage

| Pass | Status | Evidence or limitation |
|---|---|---|
| Independent decision review | Complete | Свежий изолированный reviewer без planning-артефактов, истории и прежнего отчёта проверил delivery/test/documentation-реализацию полного `326f4bcbd636b40feae36069b913d79cbac9e141..07367033e9f135ee72c7201b6303dcbaa2c70a71`; отдельно учтены каталог пользовательских путей и устойчивость доставки после перезапуска. |
| OpenSpec conformance | Complete | На recorded head прошли `go test -count=1 ./...`, целевой race-набор, `go vet ./...`, `go build ./cmd/openspec-apply-orchestrator`, `mise run test-paseo-scenarios`, отдельная квалификация `TestРеальныйPaseoКвалифицируетСозданиеИВосстановлениеСессии`, `openspec validate orchestrate-commit-preparation --strict --no-interactive --json`, проверка форматирования и `git diff --check`; состав из пяти последовательных `TestProductionПользователь...` сверён с задачами 3.10, 3.15 и 3.16. |
| Code quality | Complete | Все reviewable delivery/test/documentation-пути и неизменённые границы планировщика доставки проверены по correctness, readability, architecture, security и performance; Go MCP не получил package metadata для файлов с build tag, поэтому их диагностику закрыли сборка, полный обычный набор и два фактических tagged-прогона. |

## Findings

### F5 · Low — Каждый пользовательский сценарий заново собирает общие helper-бинарники

- **Evidence:** Общий `TestMain` один раз собирает только production-бинарник (`cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go:31-57`). Каждый из пяти пользовательских тестов затем вызывает `startProductionScenario`, который создаёт `StartIsolated` (`:195-207`), а каждый такой запуск заново собирает неизменяемые `test-provider` и `paseoproxy` (`internal/paseo/testpaseo/daemon.go:113-161`, `:730-736`). Таким образом, один каталог выполняет десять одинаковых helper-сборок, хотя изоляция обеспечивается отдельными home, socket, workspace, configuration и журналами, а не байтами бинарников.
- **Evidence revisions:** ["07367033e9f135ee72c7201b6303dcbaa2c70a71"]
- **Impact:** Каталог расходует время и ресурсы на десять лишних компиляций, расширяет поверхность случайных сбоев подготовки и делает последовательную production-проверку заметно дороже без усиления изоляции или доказательства поведения.
- **Required outcome:** Неизменяемые helper-бинарники должны подготавливаться один раз на каталог и безопасно переиспользоваться, сохраняя отдельные изменяемые ресурсы и cleanup каждого сценария.
- **Earliest source of truth:** implementation/tests
- **Affected artifacts:** ["task 3.16", "cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go", "internal/paseo/testpaseo/daemon.go"]

### F3 · Low — Безопасный отказ не доказывает неизменность Git

- **Evidence:** `TestProductionПользовательПолучаетБезопасныйОтказДоМутаций` после команды проверяет только ненулевую грязь через `git status`, отсутствие поручения и три вида Paseo-мутаций (`cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go:154-175`). Исходные HEAD, index, tracked-содержимое и untracked-набор не фиксируются и не сравниваются с состоянием после отказа.
- **Evidence revisions:** ["07367033e9f135ee72c7201b6303dcbaa2c70a71"]
- **Impact:** Частичный commit, добавление файла в index или изменение одного файла при сохранении другой незакоммиченной работы оставят `git status` непустым, поэтому сценарий останется зелёным вопреки заявленному безопасному отказу до мутаций.
- **Required outcome:** Сквозной сценарий безопасного отказа должен доказывать, что production-команда не изменила HEAD, index, tracked-содержимое, untracked-набор и принадлежащие сценарию ресурсы Paseo до возврата ошибки конфигурации.
- **Earliest source of truth:** task/verification
- **Affected artifacts:** ["task 3.16", "cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go"]

## Review coverage

Проверены все 18 reviewable paths полного локального диапазона из десяти коммитов и неизменённые границы цикла наблюдения, дедупликации доставки и proxy-событий. Обычный набор, целевой race-набор, vet, build, строгая OpenSpec-валидация и точная квалификация реального Paseo прошли; production-каталог повторно выполнил пять историй последовательно за `389.729s`, после чего процессов `oa-paseo-*` и `oa-production-scenarios-*` не осталось. Текущие правки отчёта и задачи 3.10 исключены из записанной Git-цели. Go MCP не получил package metadata для tagged-файлов, но альтернативные статические и фактические проверки этих файлов завершились успешно.
