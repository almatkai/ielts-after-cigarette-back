# Аудит Academic readiness: actual backend planner / student coverage

Дата: 2026-10-24. Независимый research sub-agent; дочерние агенты не запускались. Область: фактические `makePlan`/`fillFuture`, распределение L/R/W/S и пределы адаптации.

## Вердикт

**Как ограниченный планировщик практики — условно можно запускать пилот**, после явного уведомления о непокрытых компонентах и устранения вытеснения секций старыми ошибками. Лимиты, календарь и честное представление результатов реализованы функционально. Это не безусловный production sign-off.

**Как полноценный IELTS Academic курс — готовность не подтверждена, запуск с таким обещанием не рекомендован.** Нет гарантий покрытия четырёх секций и замкнутого цикла самостоятельного исправления/переноса; production-контент и педагогический эффект здесь не проверялись. `band`, `practiceBand`, `overallBand=null` — правильная защита от неподтверждённых оценок, а не дефект, который нужно заполнить цифрами.

Три главных блокера: (1) Writing исчезает при 30/45 мин и не обеспечен недельный минимум контактов; (2) старая ошибка способна вытеснить все остальные секции; (3) просмотр разбора не связан с самостоятельным исправлением и новой отложенной проверкой.

## Границы доказательств

- BE: `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan`; FE: `/Users/almat/Desktop/code/ielts/ielts-after-cigarette-adaptive-plan/iac-web`. Обе ветки `feat/adaptive-study-plan`. Чужие checkout не исследовались. Существующие незакоммиченные изменения не изменялись. BE HEAD `4e9df61fc7b1bc1d07957615f0629b5ddeb2fb3d`; реализация частично незакоммичена, поэтому HEAD недостаточно для идентификации объекта аудита.
- Прочитаны AGENTS, XERJ skill, research, API contract, test report, package README. XERJ autoindex остановился на занятом workspace state; один ограниченный поиск `makePlan fillFuture` с префиксом `ax-2d841c8b54ad` сработал. Выводы сверены с текущими файлами через `rg`/`nl`.
- FE fixtures `study-plan-live-response.json` и `study-plan-live-mock-response.json` прочитаны как снимки настоящего локального API с синтетическим банком. Они **не доказывают наличие или качество production-банка**.
- `.env`, БД, серверные часы и внешние AI не читались/не использовались. Изменения кода, миграций и FE не выполнялись. Сохранены только этот отчёт и [JSON фактических счётчиков](./codex-planner-counts.json).
- [Предыдущий ACCEPT](../ADAPTIVE_STUDY_PLAN_TEST_REPORT.md) означает работоспособность среза, не педагогическую валидацию. Его DB/browser проверки в этом аудите не повторялись.

## Метод измерения

Временный Go overlay добавил один observer test в пакет `studyplan`, без физического изменения исходников пакета:

```go
settings := Settings{DailyMinutes: budget, StudyDays: []int{1,2,3,4,5,6}}
p := makePlan("2026-10-26", settings, 165)
fillFuture(&p, settings, p.StartDate, p.StartDate, bank, results, false)
```

Горизонт: 26 октября — 24 ноября 2026, **30 календарных / 26 учебных дней Mon–Sat**, четыре воскресенья отдыха. Нет назначенных mocks, пропусков, начатой работы или reentry. Контакт = исполнимое назначение материала. `BLOCKED` и отдых не считаются. Это **назначенные**, а не реально выполненные контакты/минуты; повторы включены, независимой диагностикой не объявляются.

Основной банк: ровно шесть синтетических материалов W60, S11, L10, L40, R20 (passage), R60. Проверка достаточности банка: по 40 разных ID каждой категории, всего 240. Длительности — условия сценария, не свидетельство полноты секции IELTS.

Базовая история `results=nil`. Дополнительная: по одной старой SUBMITTED работе всех четырёх секций от 2026-09-01; только Reading имеет `Wrong=1`. Старые ID не входят в новый банк; `Reviewable=false`, чтобы отделить приоритет практики от назначения обзоров. Всего **16 комбинаций** в JSON. Это снимки месячной проекции при фиксированных входах, не симуляция 30 дней обучения. Новые результаты могут менять предварительные будущие недели: [service.go:255](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:255).

Команда observer:

```sh
env -u TEST_DATABASE_URL GOPROXY=off GOTOOLCHAIN=local go test -count=1 \
  -overlay docs/academic-readiness/.planner-overlay.json \
  -run '^TestAcademicReadinessObserver$' ./internal/studyplan
```

PASS. Временные Go-источник и overlay удалены после запуска. Входы описаны выше; полный вывод по дням и неделям сохранён в JSON. Также PASS **11 существующих чистых unit tests**, выбранных явным `-run`: planner budgets/rest, replan preservation, calendar/reentry, evidence, mock, small bank, wrong-count adaptation, review acknowledgement/reassignment, no-band gate, skip policy. DB/integration tests не запускались. Источники: [planner_test.go:12](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner_test.go:12), [regression_test.go:12](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/regression_test.go:12), [regression_test.go:102](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/regression_test.go:102).

## Распределение минут и контактов

В ячейках секций: **минуты / контакты**. Процент означает только долю запланированного бюджета, **не готовность курса**.

### Bank: 1-per-class

| Budget/day | L min/contacts | R min/contacts | W min/contacts | S min/contacts | Planned/available | Unused min | BLOCKED days | Budget used |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| 30 | 50 / 5 | 80 / 4 | 0 / 0 | 55 / 5 | 185 / 780 | 595 | 12 | 23.7% |
| 45 | 210 / 9 | 80 / 4 | 0 / 0 | 55 / 5 | 345 / 1170 | 825 | 8 | 29.5% |
| 60 | 210 / 9 | 320 / 8 | 240 / 4 | 55 / 5 | 825 / 1560 | 735 | 0 | 52.9% |
| 90 | 210 / 9 | 320 / 8 | 240 / 4 | 55 / 5 | 825 / 2340 | 1515 | 0 | 35.3% |

### Bank: 40-per-class

| Budget/day | L min/contacts | R min/contacts | W min/contacts | S min/contacts | Planned/available | Unused min | BLOCKED days | Budget used |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| 30 | 90 / 9 | 160 / 8 | 0 / 0 | 99 / 9 | 349 / 780 | 431 | 0 | 44.7% |
| 45 | 90 / 9 | 160 / 8 | 0 / 0 | 99 / 9 | 349 / 1170 | 821 | 0 | 29.8% |
| 60 | 70 / 7 | 120 / 6 | 360 / 6 | 77 / 7 | 627 / 1560 | 933 | 0 | 40.2% |
| 90 | 70 / 7 | 120 / 6 | 360 / 6 | 77 / 7 | 627 / 2340 | 1713 | 0 | 26.8% |

Для банка из шести материалов шаблон Mon–Sat одинаков в неделях 1–4 (дни 1–6, 8–13, 15–20, 22–27). B = BLOCKED, 0 мин. Воскресенья — отдых. Дни 29–30 при любом бюджете: L10, S11.

| Бюджет | Mon | Tue | Wed | Thu | Fri | Sat | За неделю, мин/контакты L; R; W; S |
|---|---|---|---|---|---|---|---|
| 30 | L10 | S11 | R20 | B | B | B | 10/1; 20/1; 0/0; 11/1 |
| 45 | L10 | S11 | R20 | L40 | B | B | 50/2; 20/1; 0/0; 11/1 |
| 60 | L10 | S11 | R20 | W60 | L40 | R60 | 50/2; 80/2; 60/1; 11/1 |
| 90 | L10 | S11 | R20 | W60 | L40 | R60 | 50/2; 80/2; 60/1; 11/1 |

Недельные контакты большого банка, порядок L/R/W/S:

| Бюджет | Неделя 1 | Неделя 2 | Неделя 3 | Неделя 4 | Дни 29–30 |
|---|---|---|---|---|---|
| 30 | 2/2/0/2 | 2/2/0/2 | 2/2/0/2 | 2/2/0/2 | 1/0/0/1 |
| 45 | 2/2/0/2 | 2/2/0/2 | 2/2/0/2 | 2/2/0/2 | 1/0/0/1 |
| 60 | 2/1/1/2 | 1/2/2/1 | 2/1/1/2 | 1/2/2/1 | 1/0/0/1 |
| 90 | 2/1/1/2 | 1/2/2/1 | 2/1/1/2 | 1/2/2/1 | 1/0/0/1 |

Ни один базовый сценарий не обеспечивает ≥2 контактов с каждым компонентом на каждой полной неделе. Без истории максимум шесть основных контактов за шесть дней: восемь контактов математически недостижимы при одном материале в день. Review требует подходящей старой работы и не помогает cold start. Ограничение явно задано в [planner.go:202](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:202) и закреплено в [regression_test.go:83](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/regression_test.go:83).

Порог ≥2/неделю — запрошенный benchmark. В [research:175](/Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:175) это продуктовая гипотеза для часового режима, не правило IELTS; для малого бюджета там выбран двухнедельный горизонт. Даже на двух неделях W в режимах 30/45 остаётся отсутствующим.

Бюджет — потолок: неполное использование само по себе **не code bug**. Но 60→90 не меняет ни одного назначения в этих сценариях. В большом банке короткие L10/R20 выигрывают у L40/R60, поэтому суммарная нагрузка даже ниже, чем у малого банка: [planner.go:197](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:197). Это ограничение политики относительно обещанного объёма курса.

## Находки и гейты

### P1-1. Непокрытый Writing скрывается внутри исполнимого плана

**Есть:** целые материалы не обрезаются, бюджет соблюдён. **Отсутствует:** отдельное предупреждение о непокрытой секции, если другие доступны.

W60 отсекается при 30/45 условием `Duration <= budget`: [planner.go:218](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:218). BLOCKED создаётся только для полностью пустого дня: [planner.go:299](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:299). Большой банк даёт **26 исполнимых дней, BLOCKED=0, W=0**. Текст «нет завершённых работ Writing» сообщает о результатах, но не о невозможности назначения: [planner.go:418](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:418).

Тип: функциональный дефект полноты/прозрачности четырёхсекционного плана. В узком планировщике материалов фильтр времени работает правильно; нельзя исправлять его фиктивным дроблением W60.

**Гейт:** coverage-check на каждом бюджете явно показывает отсутствующую секцию и предлагает реально доступное короткое упражнение либо согласованное длинное окно. Для курса — ≥2 контакта/компонент/неделю в 60/90 или иное утверждённое методистом правило; для 30/45 — утверждённый двухнедельный минимум с обязательным W. Проверить cold start, малый/большой банк, нехватку одной секции. Просмотр не должен заменять полноценную практику.

### P1-2. Старая ошибка вытесняет поддержку остальных секций

**Есть:** адаптация к сохранённым ошибкам. **Дефект алгоритма:** `wrong` суммирует все SUBMITTED результаты без окна давности: [planner.go:94](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:94). Булев приоритет `wrong>0` стоит раньше счётчика использования: [planner.go:188](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:188). Баланс не может переиграть даже одну старую ошибку.

Observer на большом банке: все четыре секции имеют старый SUBMITTED, одна ошибка только в R. **При любом бюджете R=520 минут/26 контактов, L=W=S=0, BLOCKED=0.** Это фиксированная проекция, не наблюдение реального ученика; новые результаты способны изменить входы. Но последующие правильные работы сами не удаляют старую ошибку из суммы. Контракт требует актуальные ошибки и поддержание навыков: [API:80](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/docs/ADAPTIVE_STUDY_PLAN_API.md:80).

**Гейт:** ограничить влияние устаревших/устранённых ошибок и обеспечить maintenance-floor. Этот контрпример не должен убирать L/W/S при доступном подходящем контенте. Проверки: одна старая ошибка, повторяющиеся ошибки, последующие правильные работы, повторы материала, отсутствие секции. Период актуальности и правило устранения сигнала утверждаются явно. Это задача алгоритма, а не калибровки AI.

### P1-3. Feedback → correction → transfer не реализован как цикл

**Есть:** разбор реальной старой работы спустя ≥2 дня, десятиминутный блок и acknowledgement: [planner.go:223](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:223), [planner.go:293](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:293), [service.go:321](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:321).

**Отсутствует в модели планировщика:** самостоятельный исправленный ответ, связь с новым заданием того же микронавыка и результат переноса. Материал содержит только ID/skill/title/duration/passage; Assignment не хранит цикл или prerequisite: [models.go:139](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/models.go:139), [models.go:60](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/models.go:60).

Тип: методический и модельный пробел курса. Кнопка просмотра не сломана: код честно не приравнивает просмотр к освоению, [planner.go:376](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:376).

**Гейт:** исходная ошибка → объяснение → сохранённое самостоятельное исправление → незнакомое семейство по тому же навыку после интервала → результат переноса. Повтор и acknowledgement не считаются независимой проверкой. Затем отдельная экспертная проверка feedback и педагогический пилот. Требование: [research:295](/Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:295).

### P2-1. Target/date не меняют отбор; minimum отсутствует

**Есть:** target и exam date читаются/возвращаются; exam type фильтрует банк: [repository.go:24](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/repository.go:24), [repository.go:31](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/repository.go:31), [service.go:444](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:444).

**Отсутствует:** minimum компонентов в Goal: [models.go:23](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/models.go:23). `makePlan`/`fillFuture` вообще не принимают Goal; горизонт всегда today+29: [planner.go:45](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:45), [planner.go:66](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:66). Изменение target/date меняет fingerprint и вызывает replan, но при прочих равных не меняет отбор, кроме opaque ID: [repository.go:94](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/repository.go:94), [service.go:255](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:255). Дата экзамена не обрезает цикл. Вывод установлен по полному потоку данных, без DB-теста.

Тип: пробел целевой адаптации; это не требование вычислять выдуманный band-gap при band=null. **Гейт:** честно обозначить план практики по доступному времени либо реализовать проверяемое использование даты, минимумов и пригодной диагностики. Сравнивать отбор при изменении только одного входа, а не наличие полей JSON. Не обещать рост к дедлайну.

### P2-2. Свежие формы не предпочитаются недельным повторам

`attempted` наполняется реальной историей, но не обновляется будущими назначениями; `used` задаёт семидневную допустимость, а не приоритет новизны: [planner.go:80](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:80), [planner.go:194](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:194), [planner.go:269](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:269). Даже при 40 разных ID каждой категории снова используются первые 1–2 ID после семи дней, остальные остаются незнакомыми; см. JSON `days[].items`.

Повторы честно помечены и не называются новым checkpoint: [planner.go:252](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:252). Это не подмена evidence, но методический пробел новизны/переноса. **Гейт:** раздельный отбор повторов для практики и незнакомых семейств для диагностики/переноса. Нового ID без family/version metadata недостаточно.

P0 в проверенной области не установлен. Непроверенность production-банка или пилота не объявляется найденной поломкой.

## Семь осей готовности

0 — нет; 1 — частично; 2 — реализовано и проверено функционально; 3 — подтверждено педагогическим пилотом. Оценки относятся к проверенной области backend planner, не заменяют аудит всего курса. Не усредняются.

| Ось | Балл | Есть | Отсутствует / непроверено |
|---|---:|---|---|
| Academic alignment | 1 | exam_type фильтр и целые длительности | Полнота W Task 1+2, S Parts 1–3 и содержание материалов не проверены; repository.go:35 |
| Баланс 4 секций / нагрузка | 1 | Бюджеты и отдых функционально проверены | W при 30/45 отсутствует; частота не гарантирована; P1-1/P1-2 |
| Диагностика / адаптация | 1 | Реальные results, missing-section/wrong приоритеты | Нет goals/minimum/date отбора, микронавыков и диагностического протокола; P2-1 |
| feedback → correction → transfer | 1 | Отложенный просмотр старого feedback | Связанный цикл исправления и нового переноса отсутствует; P1-3 |
| Контентготовность | 1 | Интеграция опубликованного банка и базовые проверки непустоты | Production-банк, права, редактура и резерв не проверены; балл только за интеграцию, repository.go:35 |
| Оценивание / доказательства | 1 | Честные raw/null/status/date, функциональные score-gates | Пригодная диагностика и валидация оценивания не подтверждены; evidence.go:49/92 |
| Устойчивость продукта | 2 | Чистые unit tests PASS; версии, сохранение, переход дней реализованы | DB/browser повторно не проверены, педагогического пилота нет; service.go:101/154, planner.go:361 |

Для последних строк исходники: [repository.go:35](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/repository.go:35), [evidence.go:49](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/evidence.go:49), [evidence.go:92](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/evidence.go:92), [service.go:101](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:101), [service.go:154](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:154), [planner.go:361](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/planner.go:361).

Нет представленного педагогического пилота, поэтому нет оценок 3. Честность выдачи неподтверждённых чисел достигает функционального уровня 2, но это не поднимает всю ось оценивания курса до 2. «Не проверено» не означает «отсутствует».

## Итоговые гейты приёмки

**Планировщик:** закрыть P1-1 в части прозрачного отсутствия компонентов и выбора выполнимого режима; закрыть P1-2; проверить эти состояния и start/submit/review/replan на выбранном локальном API с реальными попытками. Сохранить предварительность будущих недель и null активного времени. Незаполненный бюджет допустим как потолок. Этот аудит не разрешает push/merge/deploy.

**Полный Academic курс:** дополнительно закрыть P1-3, утвердить покрытие/частоту/нагрузку и использование goals; предъявить инвентарь разрешённых редакторски проверенных материалов для четырёх бюджетов и новых контрольных форм; проверить feedback специалистами; провести пилот с новыми отложенными заданиями, независимой оценкой и анализом выпадающих учеников. Начальные проектные гейты, не нормы IELTS: [research:181](/Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:181), [research:315](/Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:315), [research:329](/Users/almat/Desktop/code/ielts/research/2026-10-24-ielts-learning-methodology.md:329). Публикация числовых bands имеет отдельный гейт происхождения/валидации; учебный режим с null допустим: [API:82](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/docs/ADAPTIVE_STUDY_PLAN_API.md:82).

Синтетический observer выявляет ограничения алгоритма, не качество production-контента. Отмена уже выбранного mock-window и archive UI не объявляются реализованными: backend хранит архив приватно ([README:11](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/README.md:11)); decline после SCHEDULED отклоняется ([service.go:520](/Users/almat/Desktop/code/ielts/ielts-after-cigarette-back-adaptive-plan/internal/studyplan/service.go:520)).

## Версия исходников

SHA-256 на момент observer:

```text
499d09bf139318d5407c45cc18a5355e19412e1fa58677f56186e4f69c31cc2c  internal/studyplan/planner.go
a4af04906df12bbe8002958b77ccf6c40590feb1feaa491e36a57ba4eec358ca  internal/studyplan/models.go
cf7ec95f57d5be7291792025c0d45e368b3d37b832b419a41c3d900d7d069706  internal/studyplan/repository.go
bc90a4fc8aa0cb00bcbe3f99c9f8dadb06c330e64b12d11302791153fcecc5a1  internal/studyplan/service.go
0a64489f4653137e269c03aaa7e113f06396498cf85b96a1a1ef01875d112ae8  internal/studyplan/evidence.go
```
