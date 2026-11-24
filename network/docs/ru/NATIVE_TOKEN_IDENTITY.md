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
| Внутренний bank denom | `msp` |
| Пересчёт | `1 MSP = 1000000000000000000 msp` |
| ERC-20 обёртка в новом genesis | MintCandy Wrapped MSP (`WMSP`), 18 знаков |

В genesis майнета `coin.params.base_title` задан как `MintCandy`, а
`bank.denom_metadata` содержит `name=MintCandy`, `symbol=MSP`, `display=MSP`,
`base=msp` и две единицы: `msp` с exponent 0, `MSP` с exponent 18.
Итоговый genesis и подписанный манифест входят в выпуск `v1.2.0`.
[Публичный запуск](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.2.0#публичный-запуск).

## Сетевые профили

`msp` используется в расчётах bank/EVM/staking/mint/validator и комиссиях
для `mintcandy_9905401-1` (EVM `9905401`). Новый тестовый профиль
`mintcandy_990098-1` (EVM `990098`) использует `tmsp`, `tMSP` и `WtMSP`.
Майнет отклоняет входящие ICS-20 ваучеры с базовым denom тестовых активов,
включая пути через промежуточные каналы. Опубликованные сборки до разделения
профилей и нагрузочный стенд `mintcandy_990097-1` используют `del`.
[Параметры подготовки ноды](OPERATOR_LAUNCH.md#подготовка-для-майнета-msp).

## Совместимость контрактов

Строки `wrapped_del`, ключ реестра `wdel`, `delegateDEL`, `enable_del_delegation`,
`initial_del_delegations` сохраняют значения идентификаторов API и контрактов.
Новый сборщик genesis записывает в ERC-20 metadata
имя `MintCandy Wrapped MSP` и тикер `WMSP`. Каждый WMSP обеспечен одним
нативным MSP. Опубликованные genesis и офлайн-комплект v1.1.3 используют
прежний тикер `WDEL` для той же обёртки MSP.

В v1.1.4 команда `mcnd init` ещё создаёт пустой шаблон с отображаемой
единицей `DEL`. В v1.1.5 пустой шаблон и сборщик используют
одни нативные метаданные MintCandy / MSP. Пустой шаблон нужен для
инициализации каталога. `mcnd-genesis build` формирует стартовое
состояние с нативным тикером `MSP` и обёрткой `WMSP`.
Изолированный прогон v1.1.4 проверил
`name()` и `symbol()` на работающих EVM-нодах.
[Результаты прогона](https://github.com/maxwell2010/go-smart-node/actions/runs/45117195355).

В старых CI-отчётах, подписанном тестовом genesis и опубликованных файлах
релиза 1.0.0 обозначение DEL относится к единице того проверенного стенда.
Эти отчёты привязаны к исходным хешам и бинарникам проверенных стендов.

Для майнета в Wallet, Explorer и Console используйте MintCandy / MSP / 18
и EVM ID `9905401`. Native currency EVM-кошелька настраивается отдельно
от bank metadata. Публичный EVM RPC майнета - `https://node4.mintcandy.pro/rpc/evm/`.
[Метаданные bank](https://node4.mintcandy.pro/api/cosmos/bank/v1beta1/denoms_metadata/msp).

[Исторический проект genesis с `del`](public-genesis.draft.json),
[исторический проект распределения](public-launch-allocation.draft.json),
[оставшиеся решения](LAUNCH_APPROVAL.md).
