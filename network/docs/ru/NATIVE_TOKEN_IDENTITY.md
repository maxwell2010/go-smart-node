# Нативная монета MintCandy (MSP)

19 ноября 2026 года основатель выбрал имя **MintCandy** и тикер **MSP**
для нативной монеты MintCandy Network. Это отдельный актив новой сети.
DEL Decimal и обеспеченный им voucher моста остаются другими активами.

| Параметр | Значение |
| --- | --- |
| Имя | MintCandy |
| Тикер / symbol | MSP |
| Отображаемая единица bank metadata | MSP |
| Знаков после запятой | 18 |
| Внутренний bank denom | `del` |
| Пересчёт | `1 MSP = 1000000000000000000 del` |
| ERC-20 обёртка в новом genesis | MintCandy Wrapped MSP (`WMSP`), 18 знаков |

В проекте genesis `coin.params.base_title` задан как `MintCandy`, а
`bank.denom_metadata` содержит `name=MintCandy`, `symbol=MSP`, `display=MSP`,
`base=del` и две единицы: `del` с exponent 0, `MSP` с exponent 18.
Утверждены название, тикер, начальный выпуск и схема распределения.
Для итогового genesis ещё нужны заявки операторов и окно запуска.

## Совместимость учёта

`del` остаётся техническим идентификатором в bank/EVM/staking/mint/validator,
комиссиях и суммах genesis. Это не означает использование актива Decimal.
Строки `wrapped_del`, ключ реестра `wdel`, `delegateDEL`, `enable_del_delegation`,
`initial_del_delegations` и `atoms_per_del` сохраняют значение идентификаторов
текущей реализации. Новый сборщик genesis записывает в ERC-20 metadata
имя `MintCandy Wrapped MSP` и тикер `WMSP`. Каждый WMSP обеспечен одним
нативным MSP. Опубликованные genesis и офлайн-комплект v1.1.3 используют
прежний тикер `WDEL` для той же обёртки MSP.

В v1.1.4 команда `mcnd init` ещё создаёт пустой шаблон с отображаемой
единицей `DEL`. В текущем исходнике пустой шаблон и сборщик используют
одни нативные метаданные MintCandy / MSP. Пустой шаблон нужен для
инициализации каталога. `mcnd-genesis build` формирует стартовое
состояние с нативным тикером `MSP` и обёрткой `WMSP`.
Изолированный прогон v1.1.4 проверил
`name()` и `symbol()` на работающих EVM-нодах.
[Результаты прогона](https://github.com/maxwell2010/go-smart-node/actions/runs/45117195355).

В старых CI-отчётах, подписанном тестовом genesis и опубликованных файлах
релиза 1.0.0 обозначение DEL относится к единице того проверенного стенда.
Эти доказательства, хеши и бинарники сохраняются без изменений. Текущая
публичная документация использует MSP для будущего нативного актива.

В будущих wallet/Explorer/Console metadata используйте MintCandy / MSP / 18.
Native currency для EVM-кошелька настраивается отдельно от bank metadata;
публичные chain ID и реальные RPC ещё должны пройти согласование запуска.

[Проект genesis](public-genesis.draft.json),
[распределение](public-launch-allocation.draft.json),
[оставшиеся решения](LAUNCH_APPROVAL.md).
