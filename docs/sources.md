# Источники и происхождение

## Зависимость, используемая в archcheck

- [colbymchenry/codegraph](https://github.com/colbymchenry/codegraph) — локальный структурный граф, CLI и MCP. Именно этот проект используется как индексатор.
- [Исходники версии 1.6.2](https://github.com/colbymchenry/codegraph/tree/v1.6.2) — проверенный upstream адаптера.
- [SQLite-схема 1.6.2](https://github.com/colbymchenry/codegraph/blob/v1.6.2/src/db/schema.sql) — таблицы и поля, которые читает Go-адаптер.
- [Типы узлов и связей](https://github.com/colbymchenry/codegraph/blob/v1.6.2/src/types.ts) — значения kind и language.
- [Извлечение и SHA-256](https://github.com/colbymchenry/codegraph/blob/v1.6.2/src/extraction/index.ts) — происхождение content_hash и выбор файлов индексатором.
- [Resolver и синтетические переходы интерфейсов](https://github.com/colbymchenry/codegraph/blob/v1.6.2/src/resolution/callback-synthesizer.ts#L1015) — происхождение `metadata.synthesizedBy=interface-impl`; эти переходы не считаются прямыми вызовами.
- [Поддержка языков](https://colbymchenry.github.io/codegraph/reference/languages/) — справка upstream; маркетинговое «Full support» не заменяет проверку разрешения конкретных вызовов.
- [Телеметрия CodeGraph](https://github.com/colbymchenry/codegraph/blob/v1.6.2/TELEMETRY.md) — документированные DO_NOT_TRACK и CODEGRAPH_TELEMETRY overrides.

## Парсер и терминология

- [Tree-sitter](https://tree-sitter.github.io/tree-sitter/) — генератор парсеров и библиотека инкрементального разбора; строит concrete syntax tree отдельного файла.
- [CPG в документации Joern](https://docs.joern.io/code-property-graph/) — объединение представлений синтаксиса, управления и потоков данных. Структурный граф archcheck не заявляется полноценным CPG.

## Продуктовый референс

- [Repo Health / medrenta.ru](https://medrenta.ru/) — исходная идея BYOA: собственный агент готовит правила, отдельный движок выполняет проверку.
- [Документация архитектурного контроля CodeGraph / GoCPG](https://codegraph.ru/docs/ru/guides/ARCHITECTURE_CONTROL_ADOPTION.html) — референс контрактов архитектурных правил и полноты анализа. Это другой проект; его движок не используется в archcheck.

Ссылки на upstream привязаны к версии там, где от них зависит адаптер. Код чужих
проектов и материалы приватных обсуждений в репозиторий не переносятся.
