**Независимый аудит IELTS Academic: adversarial learning design**
24 октября 2026 · Автор: исследовательский sub-agent Astra · Только чтение кода и анализ; исправлений нет.

**Вердикт для координатора.** Как ограниченный **планировщик практики** с сохранением попыток, просмотров и расписания — допустим контролируемый запуск при выполнении гейта G0 ниже. Как автономный адаптивный план, обеспечивающий сбалансированную подготовку к цели, — пока не принят: выявлено вытеснение разделов. Как **полноценный курс IELTS Academic — запуск не обоснован**. Функционально работающий календарь не подтверждает обучение. `band`, `practiceBand`, `overallBand = null` в новом API — правильное ограничение; требовать выдуманные числа для выпуска нельзя.

Три главных блокера курса:

1. Нет серверной цепочки «конкретный feedback → собственное исправление → новое отложенное задание → проверенный результат». Просмотр сохранён, собственное исправление и перенос не связаны с планом.
2. Адаптация ограничена эвристиками раздела; дата экзамена, цель, давность evidence и критерии W/S не управляют содержанием. Приоритет исторических ошибок способен полностью вытеснить поддерживающую практику других разделов.
3. Нет предъявленных доказательств пригодности реального банка, качества AI-feedback и учебного эффекта. Production-контент **не аудирован**, а не «отсутствует». Синтетические материалы и успешные HTTP/UI-тесты не закрывают этот гейт.

**Область и способ проверки.**

- BE: `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan`, ветка `feat/adaptive-study-plan`, HEAD `4e9df61fc7b1bc1d07957615f0629b5ddeb2fb3d`.
- FE: `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web`; Git root на уровень выше, ветка `feat/adaptive-study-plan`, HEAD `f4bf6669e4176079b7ca4899b1d77a6db12bc41c`.
- Исследован **текущий незакоммиченный срез**, а не только эти коммиты. Имеющиеся изменения сохранены. Прочитаны применимые AGENTS, XERJ skill, исследование, API contract, test report, README, планировщик/сервис/репозиторий/модели/тесты и связанный FE review flow.
- Выполнен ограниченный read-only поиск XERJ с отдельными префиксами worktree. Поиск не дал достаточного покрытия нового BE-пакета; дальнейшие выводы проверены через `rg` и чтение актуальных файлов. Индекс не перестраивался из-за ограничения на записи. Известный memory breaker не использован как основание для повторных попыток восстановления.
- `.env`, production/shared DB не читались; БД, сервер приложения и внешние AI не вызывались. Дочерние агенты не запускались. Тесты Go/Playwright заново не запускались: это аудит исходников, контрактов и сохранённых ответов, а не новый integration PASS. Дополнительно выполнены только чтение JSON и явно обозначенная ниже модель сортировки в памяти Python без записи файлов.
- Сохранён единственный собственный артефакт — этот отчёт. Прежний **ACCEPT** из [test report:3][B-report-accept] относится к функциональному срезу; в настоящем выводе не является педагогической приёмкой.

Для идентификации проверенного среза SHA-256: `planner.go` — `499d09bf139318d5407c45cc18a5355e19412e1fa58677f56186e4f69c31cc2c`; `service.go` — `bc90a4fc8aa0cb00bcbe3f99c9f8dadb06c330e64b12d11302791153fcecc5a1`; `repository.go` — `cf7ec95f57d5be7291792025c0d45e368b3d37b832b419a41c3d900d7d069706`.

**Оценка по семи осям.** Шкала: 0 — отсутствует, 1 — частично, 2 — реализовано и функционально проверено, 3 — подтверждено педагогическим пилотом. Оценки не суммируются и не превращаются в процент готовности. «Непроверено» не означает 0.

| Ось | Оценка | Есть | Отсутствует / непроверено |
|---|---:|---|---|
| Academic alignment | 1 | Фильтрация опубликованных материалов по типу экзамена; четыре раздела; целые длительности; Writing review отображает критерии. [B-catalog][F-writing] | Нет curricular blueprint по Task 1/2, типам R/L и протоколу S на уровне назначения. `tasks/parts > 0` не доказывает полноту формы. Редакторская пригодность материалов не проверена. |
| Баланс четырёх секций / нагрузка | 1 | Жёсткий потолок минут, выбранные дни, одно основное задание, не более одного 10-минутного review; нет долга за пропуски. [B-sort][B-review][B-budget-test] | Нет обязательного покрытия четырёх разделов на горизонте недели/двух недель. Приоритеты могут вытеснять секции; малый бюджет может исключить целые W/L. Бюджет соблюдён ≠ курс сбалансирован. |
| Диагностика / адаптация | 1 | Приоритет секции без SUBMITTED, признаки ошибок, история материалов, reentry после паузы; пересчёт будущих дней при изменении inputs. [B-inputs][B-sort][B-refresh] | SUBMITTED не равен пригодной диагностике. Цель/дедлайн/минимумы компонентов/устаревание/критерии W/S не задают учебный маршрут. |
| Feedback → correction → transfer | 1 | Сохранённый feedback, просмотр REVIEW в БД, повторное решение старой ошибки L/R в браузере. [B-ack][F-retry][F-local] | Полный persisted learning cycle — **0**: серверного артефакта собственного исправления, зависимости transfer и результата переноса нет в проверенном пути. |
| Контентная готовность | 1* | Есть интеграция каталога и честный BLOCKED при отсутствии подходящих кандидатов. [B-catalog][B-blocked] | *1 относится к техническому подключению банка. Объём, права, качество, покрытие и резерв production-банка **непроверены**. Нельзя приписывать ему ни готовность, ни отсутствие. |
| Оценивание / доказательства | 1 | Raw L/R, даты, sampleCount, pending/repeat/stale, скрытие неквалифицированных bands. Этот защитный подсрез имеет функциональные тесты уровня 2. [B-evidence][B-score-test] | Не представлены калибровка, валидированный AI-feedback, независимая оценка переноса или учебный пилот. Старые W/S review по-прежнему показывают AI-числа; null в study-plan не означает сквозного скрытия во всём продукте. [F-review-route][F-writing] |
| Устойчивость продукта | 2** | Версии, ownership, транзакции, уникальная связь attempt, идемпотентность, реальные статусы, сохранение истории; согласуются код, тесты и зафиксированные API-ответы. [B-persist][B-schema][B-ack-test][B-report-tests] | **Функциональная проверка опирается на проверенные исходники тестов и существующий отчёт о прогонах, не повторена здесь. Нет приёмки production-операций/нагрузки. Нет отмены сохранённого mock-окна и UI архива; это отдельные продуктовые ограничения. [B-mock][B-history] |

Ни одна ось не получает 3. Доказательства педагогического пилота в предоставленных материалах отсутствуют; это не утверждение, что никакого внешнего исследования никогда не проводилось.

**Что действительно меняет назначения.**

Вход `fillFuture` — план, settings, даты календаря, материалы, результаты и флаг reentry. `Goal` в него не передаётся. Репозиторий загружает цель, но использует `examType` для банка; дата и band возвращаются в ответе. При изменении цели меняется fingerprint, поэтому возможны новая revision, новые ID назначений и сообщение NEW_EVIDENCE — это ещё не содержательная адаптация. [B-plan][B-catalog][B-fingerprint][B-refresh]

| Фактор | Реальное влияние | Чего нет |
|---|---|---|
| Cold start, нет попыток | Все разделы считаются без evidence; затем выбор по меньшему числу назначений, новизне, меньшей длительности и ключу skill:ID. | Нет обязательной батареи полных L/R/W/S, проверки условий/самостоятельности или подбора диагностической сложности. Даже одна частичная SUBMITTED снимает признак «нет evidence». [B-inputs][B-sort] |
| Старый официальный результат / самооценка | Старый `currentBand` профиля не загружается планировщиком. Это правильно для защиты от неквалифицированной оценки. | Отдельный протокол использования такого результата для маршрутизации отсутствует. Полей датированного официального профиля четырёх компонентов в `Goal` нет. [B-goal][B-catalog] |
| Старая попытка в приложении | Её даты реально влияют на семидневную доступность повтора и двухдневную задержку review. В evidence результат ≥28 дней получает STALE. | STALE не передаётся в выбор задач. Старая SUBMITTED по-прежнему закрывает `hasEvidence`; все исторические Wrong суммируются без срока давности или снятия после успеха. [B-inputs][B-review-candidate][B-evidence] |
| Разные уровни L/R/W/S | Различаются «нет SUBMITTED» и «есть хотя бы один исторический неверный ответ»; учитывается количество уже назначенных основных работ. | Ни `Band`, ни `Score/MaxScore`, ни критерии W/S не входят в comparator. При Wrong=1 и Wrong=30 основной приоритет одинаков (`>0`). AI improvements выбирают разбор, но не тематическое упражнение. [B-sort][B-review-candidate] |
| Экзамен через 7 дней | Отображаются дата и countdown; профиль может вызвать технический replan. | Всё равно создаются 30 дат. Нет гарантии диагностики/проверки до экзамена, режима последней недели или прекращения «подготовки к этому экзамену» после него. [B-plan][F-goal] |
| Экзамен через 30 дней | Тот же 30-дневный цикл. | Совпадение горизонтов случайное: дни/контрольные точки не рассчитаны от examDate. |
| Экзамен через 90 дней | Тот же текущий 30-дневный цикл; следующий цикл создаётся явно после истечения. | Нет трёхмесячной последовательности prerequisites, резервирования новых контрольных семейств или обратного планирования. [B-history] |
| 30 / 45 / 60 / 90 минут | Влияют на eligibility целого материала, возможный review и BLOCKED. Дни недели реально исключают даты. | 90 минут не означают больше учебных блоков, чем 60: предел — одна primary + один review. Это осознанный потолок, не ошибка сам по себе. Нет гарантии коротких W/L-блоков или необходимых контактов четырёх секций. [B-sort][B-review] |
| Неделя 1 / 2 / 3 / 4 / 5 | Последний учебный день может стать CHECKPOINT, меняется preliminary и строка цели. | Фразы «исправление» и «отложенная практика» не выбирают разные методики: во всех неделях тот же алгоритм. CHECKPOINT означает ранее не использованный material ID на нужной дате, не валидированную контрольную форму или перенос того же микронавыка. [B-kind][B-weekly] |
| Пауза ≥7 дней | Реально ограничивает reentry 30 минутами; выбирается короткая практика либо review, без долга. | Просмотр старого разбора не проверяет восстановление навыка, хотя может быть достаточным для мягкого возвращения в продукт. [B-sort][B-refresh][B-ack] |

Сопоставление с исследованием: оно требует входов «минимумы компонентов, дата, проверочные работы», актуализации старого профиля, самостоятельного исправления, новых отложенных материалов и поддержания всех компонентов. Это **проектные требования/гипотезы**, а не обязательные нормы IELTS. Реализация сознательно уже этого объёма. [R-inputs][R-priorities][R-cycle]

**Контрольный расчёт для 30–90 минут.**

Проверен в памяти логический пример comparator, не исполнение Go/API и не production-банк. Условия: cold start, все 30 дней учебные, нет review/claims; по 30 новых различных материалов каждой секции, длительности S=15, R=20, L=40, W=60. Такой полный Listening 40 минут — длительность материала примера, а не утверждение об официальной продолжительности. Новизна достаточна, поэтому повторы/отсрочка последнего материала не влияют. Последовательность следует `planner.go:183–218,241–273`.

| Бюджет | Основные назначения за статический снимок 30 дней | Вывод |
|---:|---|---|
| 30 | S=15, R=15, L=0, W=0 | Ни одного W/L, при этом каждый день валиден по бюджету. |
| 45 | S=10, R=10, L=10, W=0 | Ни одного W; нет предупреждения о недостаточности курса как отдельного ограничения покрытия. |
| 60 | S=8, R=8, L=7, W=7 | При этих входах четыре раздела распределяются. |
| 90 | S=8, R=8, L=7, W=7 | Дополнительные минуты не меняют основные назначения при данном банке. |

Это снимок при фиксированных results, **не прогноз поведения ученика на месяц**: реальные Submit меняют будущий план. Пример доказывает отсутствие общего инварианта покрытия, а не его нарушение для любого банка. Если production имеет пригодные короткие материалы, конкретные числа будут другими. Сокращать целый Writing до 30 минут для обхода ограничения нельзя: официальный Academic Writing включает оба задания и 60 минут; Task 2 имеет двойной вес. [Официальный формат Writing](https://ielts.org/take-a-test/test-types/ielts-academic-test/ielts-academic-format-writing)

**Три контрпримера: тесты функциональности зелёные, учебная цель не обеспечена.**

**К1. Просмотр вместо исправления — P1, отсутствующая методическая функция, не дефект endpoint `/review`.** Ученик получил неверный TFNG-ответ или замечание о недоразвитом аргументе Writing. Открывает разбор, ставит «Я просмотрел», подтверждает. Сервер сохраняет `COMPLETED`, `CompletedAt`, событие VIEWED; прогресс увеличивает `reviewedAssignments`. Никакого исправленного обоснования/абзаца/аудио запрос не содержит. Для текущего цикла этот source больше не назначается как review. [F-ack][F-api][B-ack][B-inputs]

Даже если ученик исправил старый L/R-ответ в `MistakeRetryDialog`, backend не получает этот ответ: проверка делается локально; сохраняется только `'1'` в localStorage по user/attempt/question, с fallback в память. Не сохраняются исправленный текст, объяснение, время, факт подсказки или связь с новым заданием. W/S review в проверенном пути показывает feedback, но не создаёт persisted correction. [F-retry][F-local][F-writing-task][F-review-route]

Почему зелёные тесты совместимы с провалом: BE-тест прямо утверждает, что просмотр не меняет evidence и не является mastery; FE-тест проверяет checkbox, URL и счётчик. Они корректно тестируют контракт просмотра. Ни один из этих assert не требует собственного исправления или правильного ответа на новом материале. [B-ack-test][F-ack-test] Исследование определяет единицу обучения иначе. [R-cycle]

**К2. Одна старая ошибка вытесняет остальные секции — P1, дефект правил распределения относительно требования поддерживать четыре раздела.** Пусть у ученика есть SUBMITTED по всем четырём секциям, одна старая ошибка Reading и более новые верные Reading. В Writing экспертно выявлена проблема аргументации (или есть сохранённый AI improvement как повод для review, без признания его валидным диагнозом). Есть достаточно новых подходящих R/L/W/S, бюджет 60 минут. `wrong[reading]` остаётся положительным: код суммирует всю историю; новые правильные ответы ничего не вычитают. Boolean `wrong>0` сравнивается **до** `skillUse`.

При 30 доступных новых Reading по 20 минут получаются 30 основных Reading и ноль основных L/W/S в статическом 30-дневном плане. Этот результат подтверждён логическим расчётом comparator в памяти; из кода следует, что новые верные Reading сами не снимают приоритет старой ошибки. Review другой секции возможен, но просмотр не заменяет её самостоятельную практику. При большом банке это не проблема дефицита контента. [B-inputs][B-sort] Для неполного профиля аналогично действует абсолютный приоритет `!hasEvidence`: наличие одной старой частичной работы может вытеснить секцию в пользу ещё не завершённых.

Почему тесты зелёные: `TestPracticeAdaptsToPersistedWrongCountsWithoutInventedTags` проверяет только назначение первого дня между R/L. Budget-тест проверяет целостность материалов, интервалы, детерминизм и наличие хоть одного материала, но не минимум четырёх разделов. [B-priority-test][B-budget-test] В исследовании прямо требуется поддерживать остальные компоненты и проверять покрытие малого бюджета на двухнедельном горизонте. [R-priorities]

Реальный локальный API-снимок согласуется с риском, но не доказывает его единственную причину: в `study-plan-live-response.json` Reading имеет результат 3/5, а за весь снимок Reading встречается только как два REVIEW (один MISSED и один PLANNED); R PRACTICE/CHECKPOINT/REENTRY — ноль. Другие секции имеют основные задания. Банк целиком в ответе не передаётся, поэтому из этого fixture нельзя заключить, какие R-материалы были доступны. [F-live-review][F-live-evidence] В `study-plan-live-mock-response.json` представлены основные задания всех четырёх секций; это подтверждает вариативность локальных сценариев, а не инвариант баланса. Оба файла — ответы локального API с синтетическими материалами. [F-fixture-origin][B-report-limits]

**К3. Дедлайн и профиль меняются, учебный маршрут остаётся прежним — P1 для курса, методический пробел относительно исследования; не нарушение узкого 30-дневного API-контракта.** Два пользователя имеют одинаковые material/attempt inputs, расписание и сегодня, но у первого экзамен через 7 дней и срочно требуется обновить старый W/S, у второго через 90. Изменение targetBand или examDate способно пересоздать будущие ID, но содержательный набор выбирается той же функцией без этих аргументов. План первого продолжает назначать подготовку после экзамена; план второго не содержит девяностодневной последовательности. [B-plan][B-fingerprint][B-refresh][F-goal]

Если у обоих все разделы SUBMITTED, замена результата 8-дневной давности на 35-дневный при прочих равных, старых material ID вне текущего банка и без review-кандидатов не меняет сортировку practice: семидневный cooldown уже истёк, а STALE рассчитывается отдельно. Замена численных результатов при неизменных Wrong-флагах тоже не меняет приоритет. Нужна качественная маршрутизация и обновление данных, **не** использование невалидированных bands. [B-inputs][B-sort][B-evidence]

Почему тесты зелёные: shape-тест требует 30 дат, stale-тест проверяет отображение evidence, а integration-create проверяет наличие goal в ответе. Это правильные технические условия, но они не проверяют, что задачи помещаются в срок до экзамена или адресуют различный профиль L/R/W/S. [B-budget-test][B-evidence-test][B-create-test]

**Точная граница persisted learning loop.**

1. **Исходная попытка / feedback — есть.** SQL загружает сохранённые ответы/неверные ответы и `writing_evaluations` / `speaking_evaluations`; никаких новых AI-вызовов планировщику не требуется. Provenance «сохранено» не доказывает правильность замечания. [B-results]
2. **Назначение review — есть.** Оно связано с `sourceAttemptId`; по умолчанию задержка ≥2 дней от Submit. Это задержка просмотра feedback, а не интервал после самостоятельного исправления. [B-review-candidate][B-review]
3. **Подтверждение просмотра — есть, серверное.** Меняются состояние и счётчик, band не меняется. Начало review само по себе просмотром не считается. [B-ack][B-progress][B-ack-test]
4. **Собственное исправление — частично в UI L/R, отсутствует в серверной модели цикла.** Старая ошибка может быть повторно решена, но сохранён лишь локальный флаг. Нельзя доказать ни содержание исправления, ни самостоятельность, ни синхронизацию на другом устройстве. Полезную локальную практику нельзя объявлять отсутствующей; нельзя и засчитывать её как persisted correction. [F-retry][F-local]
5. **Новый transfer — отсутствует как связанная функция.** `Assignment` содержит material/attempt/sourceAttempt, но не error/correction/transfer dependency; SourceAttemptID назначается review. Новизна отслеживается по material ID; нет family/skill/rubric mapping в `material`. Семидневный повтор того же материала и weekly CHECKPOINT не равны переносу. [B-assignment][B-material][B-kind]
6. **Доказанное устранение ошибки / изменение маршрута по переносу — отсутствует.** Старые Wrong остаются в сумме. `progress` считает attempts/reviewed/skipped, а не исправления, качество новых ответов или замкнутые циклы. `weeklyGoal` лишь описывает фазы. [B-inputs][B-progress][B-weekly]

**Приоритеты и классификация замечаний.** P0 — препятствие безопасной эксплуатации/критически ложная учебная интерпретация, P1 — блокер заявленного полноценного курса либо серьёзный дефект маршрута, P2 — ограничение качества/удобства. Это приоритеты данного аудита, а не перенос статуса прежнего code review.

| ID | Приоритет и тип | Состояние и основание | Приёмка |
|---|---|---|---|
| LL-1 | P1 · методический пробел | Нет server-persisted correction→transfer; просмотр реализован правильно. [B-ack][B-assignment][F-local][R-cycle] | G2: сохраняемые артефакты, самостоятельность, новая семья, отложенное выполнение, отдельный результат. |
| LL-2 | P1 · алгоритмический дефект баланса | Исторические ошибки / отсутствие SUBMITTED выше `skillUse`; нет ограничителя вытеснения. [B-sort][B-inputs][R-priorities] | G1: контрпример К2, покрытие каждой секции и прекращение приоритета по актуальным основаниям. |
| LL-3 | P1 · методический пробел | Нет маршрутизации по сроку/компонентным минимумам/пригодной текущей диагностике; это за пределами узкого контракта, но внутри обещания курса. [B-goal][B-plan][B-evidence][R-inputs] | G1: матрица 7/30/90, cold/stale/неравномерный профиль; объяснимые разные действия либо явное ограничение услуги. |
| LL-4 | P1 · недостающая верификация | Production content readiness, редакторская карта и независимость форм не проверены. SQL доказывает доступность записей, не качество курса. [B-catalog][B-material][B-report-limits][R-inventory] | G3: подписанный инвентарь и независимая проверка экземпляров/прав/ключей/рубрик. |
| LL-5 | P1 · недостающая педагогическая валидация | Нет предъявленной проверки AI-feedback и эффекта на новых заданиях. Скрытие bands не валидирует текст. [B-recommendations][B-report-limits][R-feedback][R-pilot] | G3/G4: отдельная слепая проверка feedback и сравнение с активной альтернативой. |
| LL-6 | P2 · семантика / продукт | Строка «Практика и исправление» не подкреплена заданиями исправления; цель/дата рядом с календарём могут восприниматься как действующие параметры персонализации. [B-weekly][F-goal] | До G1/G2 явно описать фактическую адаптацию и границы, не обещать завершённые циклы. |
| LL-7 | P2 · продуктовый lifecycle | Сохранённое mock-окно нельзя отменить/перенести, обычные задания заменены; архив хранится приватно без API/UI. Это известно и не означает потери архива. [B-mock][B-mock-replace][B-history] | Отдельная отмена/перенос с явной политикой восстановления будущего плана; чтение архивного цикла без его изменения. |
| LL-8 | P2 · сквозная согласованность evidence | Null нового API не распространяется на старые W/S review: путь из плана тоже вызывает WritingReview, который выводит AI overall/criteria; для S аналогично. Это существующий контур, не новая ошибка формулы study-plan. [F-review-route][F-writing][F-speaking] | Проверить все маршруты из плана; согласовать маркировку/скрытие невалидированных чисел, сохраняя feedback и квалифицированные оценки. |

**P0 в проверенной области не установлен.** Это не security/production sign-off. Существующие bands/overall=null не являются P0/P1-дефектом и не должны заменяться числами без оснований. Официальный overall строится из четырёх section bands; наличие произвольных четырёх учебных результатов не даёт подтверждённую шкалу. [IELTS scoring](https://ielts.org/take-a-test/your-results/ielts-scoring-in-detail)

**Конкретные гейты приёмки.**

- **G0 — ограниченный планировщик.** Публичное обещание: организация доступной практики, не доказанное достижение IELTS-цели. Указать, что дата/target сейчас информационные, коррекция/перенос не измерены; сохранить null и маркировку AI. Подтвердить доступность материалов именно для разрешённой группы запуска и выбранных бюджетов; где покрытия нет — явно назвать недостающие разделы и следующий шаг, а не показывать неполный план как полный курс. Повторить технический smoke на предназначенном для релиза срезе в разрешённой изолированной среде; этот отчёт его не заменяет. При принятом риске LL-2 возможен только наблюдаемый пилот планировщика с контролем назначения секций; автоматическую «сбалансированную подготовку» до G1 не заявлять. [B-contract][B-report-limits][B-sort]
- **G1 — диагностическое назначение и баланс.** На полном подходящем тестовом банке проверить cold start / актуальные / старые / частичные / повторные / pending результаты × четыре профиля с разным слабым разделом × 7/30/90 дней × 30/45/60/90 минут. После нормализации UUID сравнивать содержание назначения, а не revision. Для каждого режима утвердить методистом минимум контактов и диагностическое покрытие, проверить rolling 7/14-day окна и сохранение поддержки всех секций. Стартовая гипотеза исследования: для часа два контакта с каждой секцией в неделю, для малого бюджета покрытие на двухнедельном горизонте; при невозможности — честное состояние неполноты. К2 обязан перестать давать R=30/L=W=S=0 при доступном банке. Старый/частичный результат должен порождать нужное обновление; W/S-gap может задаваться проверенной рубрикой/специалистом без автоматического band. Дедлайн не разрешает превышение бюджета и не гарантирует темп роста. [R-priorities][R-inputs][B-sort]
- **G2 — функциональный учебный цикл.** Для каждой секции показать сохранённую исходную ошибку, основание feedback, собственный correction-артефакт, условия подсказки/помощи, назначенную проверку того же навыка на незнакомом семействе, её due date и независимый результат. После перезагрузки/второго устройства/пропуска/нового цикла связи сохраняются. Один checkbox или повтор той же задачи не закрывает transfer. Плохое исправление требует нового действия, просмотр не снимает учебную проблему, самостоятельный перенос может изменить следующий план. Это уровень 2, ещё не доказательство эффективности. [R-cycle][R-data]
- **G3 — содержательная пригодность.** Владелец банка предъявляет разрешённый read-only экспорт/инвентарь published-материалов: Academic subtype, обе Writing tasks и наборы графиков, протокол Speaking и пригодное аудио, R/L-типы, микронавык, family, длительность, prerequisites, ключ/рубрика/объяснение, права и редакторская приёмка. Провести walkthrough реальных циклов, включая 30-минутный режим и второй месяц. Числа исследования — 3 полные новые формы на компонент и 8 коротких единиц на микронавык — стартовый бюджет, а не магический критерий достаточности или правило IELTS. Отдельно проверить feedback вслепую по §11 исследования: корректность, охват важных проблем, вредные советы, ошибки ASR; принять заранее согласованные пороги и неопределённость. Отсутствие AI-калибровки допускает режим без bands, но не освобождает от проверки качества учебных замечаний. [R-inventory][R-feedback]
- **G4 — педагогический пилот.** До начала определить основную метрику на новых отложенных заданиях, сроки, минимально значимый эффект, расчёт мощности, активную альтернативу с тем же бюджетом и feedback, слепое оценивание W/S, анализ пропусков и ухода. Показать улучшение/перенос с неопределённостью и нагрузкой, а не только completion/retention. До результата не присваивать 3 и не утверждать доказанную эффективность курса. Числовые IELTS estimates, если понадобятся, проходят отдельный гейт шкалы/оценщиков; для проверки учебной пользы их изобретать не требуется. [R-pilot][R-score]

**Тесты учебного результата, которых не заменяет нынешний regression suite.** Это спецификация будущей приёмки; здесь они не запускались и не объявляются пройденными.

1. **Reading — перенос TFNG.** После исходной ошибки получить собственное исправленное обоснование с указанием свидетельства; через заранее выбранный интервал дать незнакомый текст того же микронавыка без показанного ответа. Проверять правильность решения и объяснения, отдельно от повторного старого вопроса. Негативный контроль: только просмотр не создаёт «исправлено». [R-cycle]
2. **Listening — новый аудиоконтекст.** После ошибки распознавания/дистрактора проверить новую запись и новые ответы; отдельно хранить повтор аудио/подсказки. Улучшение на запомненном фрагменте не считать переносом. Рубрику и ключ подтверждает редактор. [R-content][R-eligibility]
3. **Writing — новый график/тема.** Сохранить самостоятельный новый overview или развитый аргумент, а затем проверить этот критерий на другой теме вслепую. Копирование model paragraph не закрывает цикл. Полная проверка Writing охватывает оба задания; короткая коррекция остаётся короткой коррекцией. [R-cycle][R-eligibility]; [официальный Writing](https://ielts.org/take-a-test/test-types/ielts-academic-test/ielts-academic-format-writing).
4. **Speaking — новый вопрос и аудио.** Сохранить собственную повторную запись; отложенно дать другой вопрос и оценить развитие/связность, а произношение — по пригодному аудио. ASR-ошибку не превращать в языковой дефект. Старый заученный ответ не равен переносу. [R-cycle][R-ai]
5. **Неудачное исправление.** Ученик просмотрел разбор, но не понял/исправил неверно. Состояние задачи, маршрут и последующая проверка должны отличаться от успешного самостоятельного исправления; оригинальная оценка не переписывается. [R-data]
6. **Успешный новый transfer.** После нескольких заранее определённых независимых свидетельств снизить конкретный актуальный приоритет; старая ошибка остаётся в истории, но не вытесняет остальные секции. Проверить обратный сценарий — повторение проблемы на новых заданиях. [R-priorities]
7. **Ретенция эффекта.** Раздельно измерить немедленную коррекцию и отложенное новое выполнение, например через неделю; интервал — продуктовая гипотеза, не гарантированный закон обучения. Проверять и учащихся, которые пропускали дни. [R-pilot]
8. **Доступность курса при реальном бюджете.** Логировать фактическую нагрузку с согласованным способом измерения, полноту контактов всех секций и выполнение до дедлайна. `plannedMinutes ≤ budget` не является замером времени ученика. [R-progress][R-priorities]
9. **Ложная уверенность и понимание evidence.** Наблюдением проверить, понимает ли ученик, что просмотр, локальное «Разобрано», новый правильный ответ и квалифицированная оценка — разные события; попросить объяснить следующее действие и источник цифры. [R-pilot]
10. **Сравнительный пилот.** При одинаковых времени и материалах сравнить адаптивный цикл с фиксированным планом. Основной результат — независимое новое выполнение, не количество зелёных назначений или показанных рекомендаций. [R-pilot]

**Источники и границы их интерпретации.**

Первичный продуктовый ориентир — [исследование от 24.10.2026][R-root]; оно само различает правила IELTS, внешние исследования и продуктовые гипотезы. API/README описывают более узкий реализованный контракт. Официальные страницы IELTS проверены в ходе аудита: [Academic format](https://ielts.org/take-a-test/test-types/ielts-academic-test) подтверждает четыре раздела и различия Academic R/W; [Academic Writing](https://ielts.org/take-a-test/test-types/ielts-academic-test/ielts-academic-format-writing) — две задачи и их формат; [scoring](https://ielts.org/take-a-test/your-results/ielts-scoring-in-detail) — правила составного результата. Ни эти страницы, ни указанные в исследовании общие работы об обучении не валидируют конкретный IAC-планировщик.

Ссылки ниже привязаны к файлам текущих выбранных worktree. Line anchors обозначают начало релевантного участка; выводы об отсутствии касаются проверенного пути `studyplan ↔ attempts/review`, а не возможных внешних сервисов или содержимого production-БД.

[B-contract]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/docs/ADAPTIVE_STUDY_PLAN_API.md:3
[B-report-accept]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/docs/ADAPTIVE_STUDY_PLAN_TEST_REPORT.md:3
[B-report-tests]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/docs/ADAPTIVE_STUDY_PLAN_TEST_REPORT.md:19
[B-report-limits]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/docs/ADAPTIVE_STUDY_PLAN_TEST_REPORT.md:82
[B-plan]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:45
[B-inputs]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:66
[B-sort]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:183
[B-review-candidate]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:222
[B-kind]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:241
[B-review]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:275
[B-blocked]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:428
[B-weekly]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:406
[B-catalog]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/repository.go:21
[B-results]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/repository.go:68
[B-fingerprint]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/repository.go:94
[B-goal]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/models.go:18
[B-assignment]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/models.go:60
[B-material]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/models.go:130
[B-refresh]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:215
[B-ack]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:321
[B-persist]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:419
[B-schema]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/migrations/000046_adaptive_study_plan.up.sql:3
[B-mock]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:498
[B-mock-replace]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:542
[B-history]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/README.md:11
[B-evidence]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/evidence.go:49
[B-recommendations]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/evidence.go:117
[B-progress]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/evidence.go:138
[B-budget-test]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner_test.go:12
[B-evidence-test]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner_test.go:136
[B-priority-test]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/regression_test.go:102
[B-score-test]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/regression_test.go:143
[B-ack-test]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/integration_test.go:324
[B-create-test]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/integration_test.go:131
[F-api]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/features/study-plan/api.ts:165
[F-ack]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/features/study-plan/review-acknowledgement.tsx:18
[F-retry]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/features/attempts/mistake-retry-dialog.tsx:288
[F-local]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/features/attempts/use-mistake-review-status.ts:17
[F-writing]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/features/attempts/writing-review.tsx:18
[F-writing-task]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/features/attempts/writing-task-review.tsx:7
[F-review-route]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/pages/attempts/attempt-review-page.tsx:91
[F-speaking]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/pages/attempts/attempt-review-page.tsx:289
[F-goal]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/src/pages/plan/ui/plan-page.tsx:435
[F-ack-test]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/tests/study-plan.spec.ts:767
[F-fixture-origin]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/tests/study-plan.spec.ts:11
[F-live-review]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/tests/fixtures/study-plan-live-response.json:80
[F-live-evidence]: /Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web/tests/fixtures/study-plan-live-response.json:945
[R-root]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:1
[R-cycle]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:44
[R-eligibility]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:72
[R-score]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:329
[R-progress]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:123
[R-inputs]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:148
[R-content]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:156
[R-priorities]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:167
[R-inventory]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:181
[R-ai]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:276
[R-data]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:286
[R-feedback]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:315
[R-pilot]: /Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:347
