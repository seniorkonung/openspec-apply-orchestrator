## Phase 1: Видимая собственная сессия продолжается по Paseo

- [x] 1.1 Ввести типизированное состояние собственной сессии в новом Go-модуле
  - **Acceptance criteria:**
    - Создан модуль по design; ключи change, workspace и сессии различаются типами, а внешние данные проходят проверку до создания доверенного наблюдения.
    - Взаимоисключающие варианты представляют отсутствие активной собственной сессии, одну работающую, одну ожидающую действия, закрытие наблюдаемой сессии и неоднозначность; структурированный результат агента и состояние будущего запроса отсутствуют.
    - Проверены некорректные метки, противоречивые состояния и неизвестная версия; используются русские имена и описания тестов.
  - **Verification:** `go test ./internal/orchestrator/...`; `go vet ./internal/orchestrator/...`; Go MCP diagnostics для изменённых файлов. Сверить термины с `CONTEXT.md` и ADR 0001.
  - **Dependencies:** Нет.
  - **Files likely touched:** Новые `go.mod`, `internal/orchestrator/session.go`, `internal/orchestrator/session_test.go`.
  - **Estimated scope:** S.

- [x] 1.2 Безопасно вызывать совместимый локальный Paseo CLI и проверять его JSON
  - **Acceptance criteria:**
    - Адаптер запускает найденный в `PATH` исполняемый файл `paseo` через `exec.CommandContext` без shell; до мутаций проверяет версии CLI и daemon, локальность, владельца и `serverId` по `--version` и `status --json`.
    - Каждая поддерживаемая команда имеет отдельную типизированную схему результата; ненулевой код, тайм-аут, отмена, пустой, обрезанный и неожиданный JSON возвращаются как различимые ошибки, а stdout и stderr ограничены по размеру.
    - Чтение можно повторять, изменяющие команды автоматически не повторяются; сигнал отменяет только дочерний процесс и ожидание оркестратора, а диагностика не раскрывает промпт, токены или содержимое репозитория.
  - **Verification:** `go test ./internal/paseo/...` с подменным исполняемым файлом для совместимых и несовместимых версий, повреждённого JSON, тайм-аута, переполнения вывода и отмены; Go MCP diagnostics для изменённых файлов.
  - **Dependencies:** 1.1.
  - **Files likely touched:** Новые `internal/paseo/command.go`, `internal/paseo/command_test.go`, `internal/paseo/json.go`, `internal/paseo/errors.go`.
  - **Estimated scope:** M.

- [x] 1.3 Находить активный workspace и собственные сессии по проверяемым признакам CLI
  - **Acceptance criteria:**
    - `paseo workspace ls --json` используется только для активных workspace; служебное имя из digest ключа change и канонический cwd дают отсутствие или один workspace, а несколько совпадений возвращают неоднозначность без чтения архивных workspace.
    - Активные кандидаты запрашиваются через `paseo ls --global`: широкий набор содержит только метки владельца и change, а точный дополнительно содержит версию, тип и `oa.workspace=<полный-id>`; разница наборов означает повреждённую принадлежность, а `inspect` подтверждает полный ID, cwd, состояние, архивирование, запросы разрешений и отсутствие чужого родителя.
    - Проверка уже реализованных типов расширена связью с workspace; ручные сессии, внутренние субагенты, архивированная история и собственные сессии других change или workspace не принимаются за активное поручение, а две точные сессии дают неоднозначность независимо от названий.
  - **Verification:** `go test ./internal/paseo/... ./internal/orchestrator/...` с JSON-фикстурами активных workspace, широких и точных фильтров, повреждённых меток, двух сессий, архивированной истории, ручных сессий и поручений других change; сверить сценарии `managed-paseo-sessions`.
  - **Dependencies:** 1.1, 1.2.
  - **Files likely touched:** Новые `internal/paseo/directory.go`, `internal/paseo/directory_test.go`; существующие `internal/orchestrator/session.go`, `internal/orchestrator/session_test.go`.
  - **Estimated scope:** M.

- [x] 1.4 Создавать workspace и собственную сессию и подтверждать её архивирование через CLI
  - **Acceptance criteria:**
    - При необходимости нового поручения отсутствующий workspace создаётся через `paseo workspace create --json` со служебным именем и каноническим cwd; проверенный полный ID используется во всех последующих командах и в метке сессии.
    - Один `paseo run --background --json` передаёт полный ID workspace, настройки, встроенный первоначальный промпт и метки `oa.owner`, `oa.version`, `oa.change`, `oa.kind`, `oa.workspace`; неопределённый исход не ставит повтор в очередь и не вводит резервирование.
    - `paseo archive <полный-id> --json` вызывается без `--force` только после подтверждённого завершения хода; архивирование подтверждается `inspect`, а потеря ответа требует чтения состояния вместо слепого повтора или признания закрытия.
  - **Verification:** `go test ./internal/paseo/...` с фиксацией точных аргументов команд, JSON-результатов, потерей ответа на `run` и `archive`, работающей сессией и повторным `inspect`; сопоставить поведение с Paseo CLI 0.7.2.
  - **Dependencies:** 1.2, 1.3.
  - **Files likely touched:** Новые `internal/paseo/mutations.go`, `internal/paseo/mutations_test.go`; `internal/paseo/workspace.go`, `internal/paseo/session.go`, `internal/orchestrator/ownership.go`.
  - **Estimated scope:** M.

- [x] 1.5 Исключать второго локального владельца выбранного change без файлов состояния
  - **Acceptance criteria:**
    - Реализован неблокирующий advisory lock открытого существующего корня выбранного change на Linux; канонически эквивалентные пути к тому же change не обходят ограничение, а разные change не используют общий lock только из-за одного Git common dir.
    - Не-Linux среда, файловая система вне явного списка поддерживаемых локальных типов и невозможность установить lock возвращают объясняющую ошибку до изменяющих команд Paseo; второй процесс того же change получает отдельное объяснение занятости.
    - Обычная, сигнальная и аварийная остановка владельца освобождает lock без очистки PID- или lock-файлов; lock не служит свидетельством состояния сессии Paseo.
  - **Verification:** `go test ./internal/ownership/...` с двумя процессами одного change, канонически эквивалентными путями, разными change в одном Git-репозитории, классификацией файловых систем и принудительной остановкой владельца; проверить сборку неподдерживаемой платформы с её объясняющей заглушкой.
  - **Dependencies:** 1.1.
  - **Files likely touched:** Новые `internal/ownership/lock_linux.go`, `internal/ownership/lock_unsupported.go`, `internal/ownership/environment.go`, `internal/ownership/lock_test.go`, `internal/ownership/environment_test.go`.
  - **Estimated scope:** M.

- [x] 1.6 Продолжать одну видимую активную сессию по свежему наблюдению CLI
  - **Acceptance criteria:**
    - Ядро выбирает относящееся к фазе действие для отсутствующей, работающей, ожидающей действия, закрытой в текущем наблюдении и неоднозначной собственной сессии; каждое воздействие требует свежих JSON-результатов активного workspace, фильтров сессий и `inspect`.
    - Повторное создание ядра с пустой памятью находит ту же видимую активную сессию по меткам change и workspace и не отправляет первоначальное поручение повторно; архивированная сессия не запрашивается при новом выборе и не блокирует отдельную будущую попытку.
    - Отмена ожидания оставляет агент в Paseo, а ручная сессия и сессия другого change не наблюдаются и не архивируются этим сопровождением.
  - **Verification:** `go test ./internal/orchestrator/... ./internal/paseo/...` с подменяемыми часами, сменой результатов CLI между чтением и действием, исчезновением наблюдаемой сессии после архивирования и повторным созданием ядра.
  - **Dependencies:** 1.3, 1.4, 1.5.
  - **Files likely touched:** Новые `internal/orchestrator/reconcile.go`, `internal/orchestrator/reconcile_test.go`; `internal/paseo/session.go`, `internal/paseo/mutations.go`.
  - **Estimated scope:** M.

- [x] 1.7 Доказать продолжение видимой сессии через реальные границы процесса
  - **Acceptance criteria:**
    - Интеграционный стенд запускает совместимые Paseo CLI и локальный daemon с отдельным хранилищем и управляемым тестовым провайдером; производственный адаптер подтверждает версии, владельца и `serverId`, а первоначальный промпт и метки обнаруживаются после штатного перезапуска daemon.
    - Проверены остановки после подтверждённого `run`, во время работы, при ожидании действия и до или после `archive`: новый процесс через `workspace ls`, фильтрованный `ls` и `inspect` находит ту же видимую активную сессию либо корректно решает по отсутствию активной сессии.
    - Неопределённый `run` не повторяется внутри процесса; неподдерживаемая среда блокируется до мутаций, прямой протокол не используется, а стенд и документация не заявляют устранение принятого окна между созданием и видимостью сессии. Ручные сессии и поручения другого change остаются вне управления.
  - **Verification:** Создать и выполнить `go test -tags=paseo_integration ./internal/paseo/... ./internal/orchestrator/...` против отдельного локального daemon через установленный Paseo CLI; зафиксировать совместимые версии, ID workspace и сессии и результаты фильтров после каждого контролируемого прерывания.
  - **Dependencies:** 1.4, 1.6.
  - **Files likely touched:** Новые `internal/paseo/recovery_integration_test.go`, `internal/orchestrator/recovery_integration_test.go`, `internal/testpaseo/daemon.go`, `internal/testpaseo/provider.go`, `docs/development/paseo-compatibility.md`.
  - **Estimated scope:** M.

- [x] 1.8 Подтвердить готовность сопровождения видимой сессии к подключению Git
  - **Acceptance criteria:**
    - Условие `Ready to advance` Phase 1 подтверждено результатами 1.7: одна команда переносит промпт, полный ID workspace и метки, видимая активная сессия продолжается после перезапуска, неоднозначность обнаруживается, ручные сессии не затрагиваются, а неподдерживаемая среда отклоняется до мутаций.
    - Пройдены тесты, статические проверки и проверка зависимостей; отсутствуют прямой протокол Paseo, WebSocket-зависимость, собственные записи прогресса, резервирование будущего агента и реализация возможностей последующих фаз.
    - Заголовок пакета совпадает с plan, строгая валидация change успешна; перед продолжением требуется отдельное планирование пакета Phase 2.
  - **Verification:** `go test ./...`; `go test -race ./...`; `go vet ./...`; `go test -tags=paseo_integration ./internal/paseo/... ./internal/orchestrator/...`; Go MCP diagnostics и vulncheck; `openspec validate orchestrate-commit-preparation --strict --no-interactive`; проверить отсутствие различий после `gofmt` для новых Go-файлов.
  - **Dependencies:** 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 1.7.
  - **Files likely touched:** Нет, только проверка и отметка задачи после успешного выполнения.
  - **Estimated scope:** XS.

## Phase 2: Подготовка коммитов завершается чистым Git

- [ ] 2.1 Разрешать контекст явно выбранного активного change через OpenSpec CLI
  - **Acceptance criteria:**
    - Адаптер получает planning home и корень change из проверенных JSON-ответов `openspec context` и `openspec status`, сохраняя явный `--store` на всех относящихся к нему вызовах.
    - Активный change принимается независимо от его схемы и наличия плана, задач или ревью; имя, корни и область действия преобразуются в типизированный проверенный контекст.
    - Отсутствие change, ошибка процесса, тайм-аут, превышение размера, повреждённый JSON и несогласованные корни возвращаются как различимые ошибки без построения путей по догадке.
  - **Verification:**
    - `go test ./internal/openspec/...` с подменным OpenSpec CLI для repo-local и store-контекста, другой схемы, отсутствующего change, повреждённого JSON и противоречивых корней.
    - `go vet ./internal/openspec/...`.
  - **Dependencies:** 1.8.
  - **Files likely touched:** Новые `internal/openspec/command.go`, `internal/openspec/context.go`, `internal/openspec/context_test.go`.
  - **Estimated scope:** M.

- [ ] 2.2 Классифицировать актуальное состояние рабочего Git без его изменения
  - **Acceptance criteria:**
    - Адаптер канонизирует корень рабочего дерева и представляет Git как взаимоисключающие состояния `чистый` и `грязный`; staged, tracked, конфликтные и неотслеживаемые неигнорируемые изменения считаются работой, а игнорируемые файлы — нет.
    - Git вызывается через `exec.CommandContext` без shell с `GIT_OPTIONAL_LOCKS=0`, ограниченными сроком и выводом; адаптер не выполняет изменяющих Git-команд и не пытается оценивать безопасность создания коммитов.
    - Ошибка чтения, отсутствие репозитория и смена канонического рабочего контекста отличаются от чистого дерева.
  - **Verification:**
    - `go test ./internal/gitstate/...` на временных репозиториях с чистым деревом, индексом, изменёнными, новыми, игнорируемыми и конфликтными файлами, worktree и символическими ссылками.
    - `go vet ./internal/gitstate/...`.
  - **Dependencies:** 1.8.
  - **Files likely touched:** Новые `internal/gitstate/repository.go`, `internal/gitstate/status.go`, `internal/gitstate/status_test.go`.
  - **Estimated scope:** M.

- [ ] 2.3 Получать проверенные настройки сессии и встроенное поручение подготовки коммитов
  - **Acceptance criteria:**
    - `openspec-apply-orchestrator.json` читается только из канонического корня рабочего дерева; версия `1`, обязательные provider/model и необязательные reasoning/mode проверяются строго, а неизвестные поля и некорректные значения сообщаются с путём до настройки.
    - Проверенные параметры передаются в `paseo run` без интерпретации, shell и молчаливых значений по умолчанию; конфигурация не содержит промпт, прогресс или секрет ntfy.
    - Встроенное поручение требует осмысленно закоммитить все текущие изменения и запрещает отбрасывать их, угадывать разрешение конфликтов, исправлять код ради проверки, обходить hooks, выполнять push и переписывать прежнюю историю; специальный структурированный ответ не требуется.
  - **Verification:**
    - `go test ./internal/config/... ./internal/prompts/...` для полной конфигурации, обязательных полей, неизвестных полей, неподдерживаемой версии и обязательных ограничений промпта.
    - Проверить точное преобразование настроек через `paseo.NewSessionSettings`.
  - **Dependencies:** 1.8.
  - **Files likely touched:** Новые `internal/config/config.go`, `internal/config/config_test.go`, `internal/prompts/commit_preparation.md`, `internal/prompts/prompts.go`, `internal/prompts/prompts_test.go`.
  - **Estimated scope:** M.

- [ ] 2.4 Подтвердить готовность входных адаптеров до изменения цикла сопровождения
  - **Acceptance criteria:**
    - OpenSpec, Git, конфигурация и встроенный промпт покрывают входные условия Phase 2 и возвращают типизированные данные либо объясняющие ошибки.
    - Все проверки остаются операциями чтения и не создают workspace, сессию, Git-объекты или собственное состояние восстановления.
    - Существующие проверки Phase 1 продолжают проходить без ослабления контрактов Paseo и локального владения change.
  - **Verification:**
    - `go test ./internal/openspec/... ./internal/gitstate/... ./internal/config/... ./internal/prompts/... ./internal/orchestrator/... ./internal/paseo/...`.
    - `go vet ./internal/openspec/... ./internal/gitstate/... ./internal/config/... ./internal/prompts/...`.
  - **Dependencies:** 2.1, 2.2, 2.3.
  - **Files likely touched:** Нет, только проверка.
  - **Estimated scope:** XS.

- [ ] 2.5 Сохранять идентичность известной собственной сессии при наблюдении
  - **Acceptance criteria:**
    - После обнаружения или создания сессии текущий процесс сохраняет только её типизированный ID; если она исчезает из активных фильтров во время автономного наблюдения, процесс подтверждает её закрытие через `inspect`, не принимая ошибку или противоречие за закрытую сессию.
    - Новый процесс без известного ID использует только активные фильтры и не читает архивированную историю; отсутствие активной сессии остаётся основанием для нового решения по Git.
    - Ручная сессия, внутренний субагент и сессия другого change или workspace не становятся целью наблюдения либо архивирования.
  - **Verification:**
    - `go test ./internal/paseo/... ./internal/orchestrator/...` со сценариями закрытия известной сессии во время автономного наблюдения, недоступного `inspect`, смены ID, нового процесса и чужих сессий.
  - **Dependencies:** 2.4.
  - **Files likely touched:** `internal/paseo/session_directory.go`, `internal/paseo/session_directory_test.go`, `internal/paseo/reconcile_gateway.go`, `internal/paseo/reconcile_gateway_test.go`.
  - **Estimated scope:** M.

- [ ] 2.6 Выбирать действие подготовки коммитов по свежим наблюдениям OpenSpec, Paseo и Git
  - **Acceptance criteria:**
    - При отсутствии активной собственной сессии чистый Git завершает команду без создания workspace или агента, а грязный разрешает ровно одно новое поручение после свежих проверок; существующая активная сессия всегда имеет приоритет независимо от Git.
    - Работающая сессия продолжает наблюдаться; завершившийся ход при чистом Git архивируется с подтверждением, а грязный Git, ошибка агента или запрос разрешения возвращают типизированный исход потребности в человеке с ID сессии и причиной без архивирования либо нового агента.
    - Потребность в человеке завершает цикл Phase 2 без ожидания ручного закрытия; подтверждённое закрытие до такого исхода вызывает свежую проверку Git, а отмена оставляет сессию Paseo доступной и не запускает следующий workflow.
  - **Verification:**
    - `go test ./internal/orchestrator/... ./internal/paseo/...` с таблицей всех сочетаний состояния сессии и Git, сменой источников перед воздействием, повторным запуском и отменой ожидания.
  - **Dependencies:** 2.1, 2.2, 2.3, 2.4, 2.5.
  - **Files likely touched:** `internal/orchestrator/reconcile.go`, `internal/orchestrator/reconcile_test.go`, `internal/orchestrator/session.go`, `internal/orchestrator/session_test.go`, `internal/paseo/reconcile_gateway.go`.
  - **Estimated scope:** M.

- [ ] 2.7 Собрать production-команду одного поручения `prepare-commits`
  - **Acceptance criteria:**
    - `openspec-apply-orchestrator prepare-commits --change <name>` принимает необязательный `--store`, проверяет OpenSpec, Git, конфигурацию, Linux-local среду, lock и совместимость Paseo до первой мутации.
    - Ключ change строится из канонических рабочего дерева и planning home, имени change и `serverId`; Git remote не участвует, а смена конфигурации не пересоздаёт уже видимую активную сессию.
    - Русский stdout различает проверки, отсутствие работы, создание или восстановление сессии, ожидание, потребность в человеке, архивирование и ошибки без промпта, diff и секретов; при потребности в человеке команда показывает причину и известную сессию, оставляет её активной и завершается кодом препятствия `1`, не ожидая ручного закрытия.
  - **Verification:**
    - `go test ./cmd/openspec-apply-orchestrator/...` с подменными адаптерами для порядка preflight, аргументов, store, кодов завершения, сигналов и безопасного вывода.
    - `go build ./cmd/openspec-apply-orchestrator`.
  - **Dependencies:** 2.6.
  - **Files likely touched:** Новые `cmd/openspec-apply-orchestrator/main.go`, `cmd/openspec-apply-orchestrator/prepare_commits.go`, `cmd/openspec-apply-orchestrator/prepare_commits_test.go`, `internal/orchestrator/identity.go`, `internal/orchestrator/identity_test.go`.
  - **Estimated scope:** M.

- [ ] 2.8 Доказать подготовку коммитов и восстановление через реальные границы процессов
  - **Acceptance criteria:**
    - Сквозной стенд с настоящими Git, OpenSpec CLI и изолированным Paseo подтверждает выход без агента при чистом дереве и однократную передачу настроек и встроенного поручения при staged, tracked и untracked работе.
    - Остановка CLI во время работы и после создания коммитов сохраняет одну видимую сессию; новый процесс восстанавливает её, а чистый Git приводит к подтверждённому архивированию и успеху без повторного поручения.
    - Грязный Git после завершения хода, ошибка агента и запрос разрешения дают локально видимый код препятствия, сохраняют ту же активную сессию без второго `run` и не удерживают процесс в ожидании её ручного закрытия; повторный запуск сначала находит эту сессию.
  - **Verification:**
    - `go test -tags=paseo_integration ./internal/paseo/... ./internal/orchestrator/... ./cmd/openspec-apply-orchestrator/...`.
    - Проверить журнал мутаций Paseo, доставленный промпт, ID восстановленной сессии, итоговый Git и коды процессов в каждой точке прерывания.
  - **Dependencies:** 2.7.
  - **Files likely touched:** `internal/testpaseo/daemon.go`, `internal/testpaseo/provider.go`, новые `internal/orchestrator/commit_preparation_integration_test.go`, `cmd/openspec-apply-orchestrator/prepare_commits_integration_test.go`, `docs/development/paseo-compatibility.md`.
  - **Estimated scope:** M.

- [ ] 2.9 Подтвердить готовность подготовки коммитов к подключению уведомлений
  - **Acceptance criteria:**
    - Выполнено условие `Ready to advance` Phase 2: различимы отсутствие работы, работа агента, чистый Git и локально видимая потребность в человеке при грязном Git, ошибке агента либо запросе разрешения; восстановление не создаёт повторного поручения.
    - Пройдены модульные, race, статические и интеграционные проверки; поведение соответствует спецификациям и не использует прямой протокол Paseo, собственный журнал, исходный снимок или структурированный результат агента.
    - В поставку Phase 2 не попали ntfy, длительное ожидание человека, ручное закрытие переданной сессии, повторная проверка Git после него, обсуждение задач, Apply, ревью, переход к следующей фазе или архивирование change; перед продолжением требуется отдельное планирование Phase 3.
  - **Verification:**
    - `go test ./...`; `go test -race ./...`; `go vet ./...`; `go build ./cmd/openspec-apply-orchestrator`.
    - `go test -tags=paseo_integration ./internal/paseo/... ./internal/orchestrator/... ./cmd/openspec-apply-orchestrator/...`.
    - Go MCP diagnostics и vulncheck; `openspec validate orchestrate-commit-preparation --strict --no-interactive`; проверить отсутствие различий после `gofmt`.
  - **Dependencies:** 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 2.8.
  - **Files likely touched:** Нет, только проверка и отметка задачи после успешного выполнения.
  - **Estimated scope:** XS.
