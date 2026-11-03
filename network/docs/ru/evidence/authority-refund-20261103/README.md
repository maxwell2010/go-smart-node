# Учёт ресурсов внутренних Gov-вызовов

Обычный regression использует настоящий Master в owned App. Из одного
предварительно подготовленного состояния `setRewardPerBlock(0)` очищает
один storage slot, а `clearRewardPerBlock()` - два. Результаты EVM и стоимость
SDK commit отдельно измеряются в отбрасываемых кешах. Суммы в `result.json`
относятся к ресурсному бюджету внутренних вызовов.

В исходной реализации `8cf619d` учитывался post-refund `GasUsed`. После
вычитания настоящей EVM-работы и SDK commit оставшиеся нативные затраты
ошибочно зависели от возврата storage gas: 442159 против 437359. Контроль
завершается FAIL. Исправление `8e13e4b` учитывает
`max(MaxUsedGas, GasUsed)` с проверкой фактического лимита. Тот же контроль
даёт одинаковые 446959 и проходит.

Полные исходные логи сохранены локально; их SHA256 включены в JSON.
Численные результаты не заменяют общий финансовый тест. Последующий общий
прогон пакетов проверяет также штатный Gateway, реальное сжигание, NFT и
валидаторные компоненты. Нативные финансовые маршруты в App этим исправлением
не активируются.

Тест находится внутри `TestOwnedAppSignedRegistryBootstrap`:

```sh
go test -mod=readonly -p 2 -count=1 -v ./systemcontracts -run 'TestOwnedAppSignedRegistryBootstrap/atomic_constructor_handoff_to_governance/canonical_governance_authority_execution/internal_work_charges_pre-refund_EVM_execution'
```
