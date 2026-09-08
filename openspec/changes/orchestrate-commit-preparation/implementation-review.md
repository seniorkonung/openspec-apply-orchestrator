# OpenSpec Implementation Review: orchestrate-commit-preparation

## Assessment

**Format version:** 1
**Result:** Changes needed
**Coverage status:** Complete
**Summary:** Сквозное доказательство задачи 2.8 не охватывает production-путь после перезапуска daemon и неопределённого `run`, а отдельные утверждения об отсутствии мутаций на чистом Git и `inspect`-polling выражены недостаточно строго.

## Review target

- **Baseline ref:** origin/main
- **Base commit:** 3fc6f70d96a833a7cfd6d978134c8d9454016575
- **Reviewed head:** a143f1e96ac3572e86521286e5a338437ec6da1c
- **Target commits:** ["726802a382905485d173298b06508e4bfea693a9", "a143f1e96ac3572e86521286e5a338437ec6da1c"]
- **Reviewable paths:** ["cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go", "docs/adr/0002-go-core-with-paseo-protocol-adapter.md", "docs/adr/README.md", "docs/development/paseo-compatibility.md", "internal/paseo/integration_creation.go", "internal/paseo/settings.go", "internal/testpaseo/cmd/paseoproxy/main.go", "internal/testpaseo/cmd/reconcile/main.go", "internal/testpaseo/daemon.go", "internal/testpaseo/provider.go", "openspec/changes/orchestrate-commit-preparation/adr.md", "openspec/changes/orchestrate-commit-preparation/design.md", "openspec/changes/orchestrate-commit-preparation/plan.md", "openspec/changes/orchestrate-commit-preparation/tasks.md"]
- **OpenSpec change:** orchestrate-commit-preparation
- **OpenSpec schema:** intent-driven
- **Target scope:** Complete pre-push range
- **Baseline freshness:** Local ref state; no fetch performed
- **Planning evidence paths:** ["docs/adr/0002-go-core-with-paseo-protocol-adapter.md", "docs/adr/README.md", "openspec/changes/orchestrate-commit-preparation/adr.md", "openspec/changes/orchestrate-commit-preparation/design.md", "openspec/changes/orchestrate-commit-preparation/plan.md", "openspec/changes/orchestrate-commit-preparation/tasks.md"]
- **Excluded worktree state:** ["openspec/changes/orchestrate-commit-preparation/review.md — появился после обнаружения цели и не использован как committed-доказательство"]

## Reviewed increment

### U1 · Сквозное доказательство подготовки коммитов и восстановления

- **Work items:** ["2.8 Доказать подготовку коммитов и восстановление через реальные границы процессов"]
- **Requirements and scenarios:** ["commit-preparation: проверка среды и выбранного change", "commit-preparation: настройки агента и поставка промптов", "commit-preparation: одно ограниченное поручение подготовки коммитов", "commit-preparation: проверяемое завершение подготовки коммитов", "managed-paseo-sessions: восстановление видимой активной сессии", "managed-paseo-sessions: прямая отправка через Paseo CLI и граница восстановления", "managed-paseo-sessions: решения принимаются по актуальным источникам", "intervention-reporting: передача затруднения в текущую сессию"]
- **Affected boundary:** Собранная production-команда и её взаимодействие через отдельные процессы с временным Git-репозиторием, OpenSpec CLI, Paseo CLI/локальным daemon и управляемым тестовым провайдером.
- **Implementation target:** ["cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go", "docs/development/paseo-compatibility.md", "internal/paseo/integration_creation.go", "internal/paseo/settings.go", "internal/testpaseo/cmd/paseoproxy/main.go", "internal/testpaseo/cmd/reconcile/main.go", "internal/testpaseo/daemon.go", "internal/testpaseo/provider.go"]
- **Applicable constraints and non-goals:** Linux и локальный daemon того же пользователя; взаимодействие с Paseo только через CLI и JSON; обычная production-сборка не содержит тестовый provider или тестовый unrestricted-режим; доставка уведомлений, длительное участие человека, изменение production-workflow и доказательство содержательной корректности коммитов исключены.
- **Excluded change scope:** Будущие задачи 2.15–2.22 по углублению и миграции активного Paseo-контракта, а также задача 2.9 и Phase 3.

## Pass coverage

| Pass | Status | Evidence or limitation |
|---|---|---|
| Independent decision review | Complete | Свежий изолированный reviewer проверил полный `3fc6f70d96a833a7cfd6d978134c8d9454016575..a143f1e96ac3572e86521286e5a338437ec6da1c` по семи назначенным Go-путям без planning-артефактов и прежних review-материалов. |
| OpenSpec conformance | Complete | На чистом recorded head прошли `openspec validate orchestrate-commit-preparation --json`, `go test ./...`, `go test -race ./...` и точная команда задачи 2.8 `go test -p=1 -count=1 -tags=paseo_integration ./internal/paseo/... ./internal/orchestrator/... ./cmd/openspec-apply-orchestrator/...`; completion claim задачи 2.8 сверена в обе стороны и дала F1–F3. |
| Code quality | Complete | По восьми delivery/test/documentation-путям проверены correctness, readability, architecture, security, performance и доказательства; прошли `go vet ./...`, обычная production-сборка, `gofmt -d`, `git diff --check`, Go workspace diagnostics и Go vulncheck. Дополнительных findings сверх F1–F3 не подтверждено. |

## Findings

### F1 · High — Перезапуск daemon и неопределённый `run` не проверяются через production-команду

- **Evidence:** Новый `cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go` запускает собранную команду, но не вызывает `Harness.Restart` или `Harness.InterceptRunOutput`. Неизменённый `internal/paseo/recovery_integration_test.go:18` проверяет перезапуск прямыми вызовами клиента, а `internal/paseo/recovery_integration_test.go:118` после потерянного ответа запускает специальный `DriverObserve`, не production-путь `prepare-commits`. Задача 2.8 при этом отмечена выполненной в `openspec/changes/orchestrate-commit-preparation/tasks.md:226` и требует безопасного поведения при неопределённой мутации.
- **Evidence revisions:** ["a143f1e96ac3572e86521286e5a338437ec6da1c"]
- **Impact:** Регрессия в production-композиции может неверно классифицировать потерянный результат `run`, создать замену или повторить мутацию после перезапуска, сохранив зелёными более низкоуровневые integration-тесты; возможны две конкурирующие собственные сессии.
- **Required outcome:** Перезапуск daemon и потеря результата изменяющей команды должны проходить через отдельные процессы собранного `prepare-commits` и доказывать одну сессию с тем же ID, отсутствие повторного `run` и замены, а также правильные exit-коды и безопасный вывод.
- **Earliest source of truth:** task/verification
- **Affected artifacts:** ["задача 2.8", "cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go", "internal/paseo/recovery_integration_test.go"]

### F2 · Medium — Чистый сценарий не исключает побочные мутации Paseo

- **Evidence:** `cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go:30` проверяет только успешный код и отсутствие промптов, после чего сбрасывает уже включённый журнал команд. Доступный `assertNoPaseoMutations` в том же файле (`:534`) не вызывается; создание workspace не отправляет промпт и потому не нарушит текущую проверку.
- **Evidence revisions:** ["a143f1e96ac3572e86521286e5a338437ec6da1c"]
- **Impact:** Реальная команда может начать создавать workspace или выполнять другую мутацию при чистом Git, а заявленный сквозной тест продолжит проходить.
- **Required outcome:** Сценарий чистого Git должен подтверждать отсутствие всех изменяющих команд Paseo и сохранность исходного состояния и истории Git наряду с отсутствием поручения агенту.
- **Earliest source of truth:** task/verification
- **Affected artifacts:** ["задача 2.8", "cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go"]

### F3 · Medium — Журнал не доказывает отсутствие `inspect`-polling во время `wait`

- **Evidence:** `cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go:498` ищет после записи `wait` последующую последовательность `workspace`, `ls`, `inspect`, `archive` и проверяет лишь единственность `wait`. Дополнительные `inspect` между запуском `wait` и свежим `workspace ls` не отклоняются. Однако задача 2.8 (`openspec/changes/orchestrate-commit-preparation/tasks.md:230`) и руководство (`docs/development/paseo-compatibility.md:48`) утверждают, что журнал доказывает отсутствие polling.
- **Evidence revisions:** ["a143f1e96ac3572e86521286e5a338437ec6da1c"]
- **Impact:** Регрессия к запрещённому техническому опросу работающей сессии останется зелёной, увеличит нагрузку на Paseo и нарушит требуемую семантику одного блокирующего ожидания.
- **Required outcome:** Сквозная проверка должна отклонять любые технические чтения Paseo между началом блокирующего `wait` и его возвратом и отдельно подтверждать полное свежее наблюдение после события.
- **Earliest source of truth:** task/verification
- **Affected artifacts:** ["задача 2.8", "cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go", "docs/development/paseo-compatibility.md"]

## Review coverage

Проверены все изменённые production/test пути unit U1, связанный command layer и существующие неизменённые проверки адаптера и восстановления. Planning- и ADR-пути использованы только координатором для conformance и mapping. Документ `docs/development/paseo-compatibility.md` учтён как verification-доказательство, а шесть ADR/OpenSpec-путей — как planning evidence. Все проверки выполнялись на `a143f1e96ac3572e86521286e5a338437ec6da1c`; локальный `origin/main` оставался на `3fc6f70d96a833a7cfd6d978134c8d9454016575`. Tagged Go-файлы не получили целевую metadata от Go MCP, но были скомпилированы и выполнены точным integration-набором; workspace diagnostics для обычной сборки замечаний не вернули.
