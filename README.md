# MintCandy Network

Нативная монета майнета **MintCandy (MSP)** имеет 18 знаков и базовый denom `msp`.
Chain ID майнета - `mintcandy_9905401-1`, EVM chain ID - `9905401`.
[Название, metadata и техническая совместимость](network/docs/ru/NATIVE_TOKEN_IDENTITY.md).

Независимый интеграционный форк Decimal `go-smart-node`. Основная разработка
ведётся в [отдельном модуле `network/`](network/README.md): нода `mcnd`,
собственная сеть с новым genesis, EVM, нативный консенсус и IBC.

Четыре валидатора подписали первый блок майнета. Для установки нужны
Linux-бинарник и подписанный genesis. Для запуска с версии 1.2.1 соблюдайте
высоту обновления, утверждённую голосованием. Версия 1.2.3 сохраняет правила
обработки блоков версии 1.2.2. [Выпуск v1.2.3](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.2.3) ·
[Подписанный genesis v1.2.0](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.2.0) ·
[Порядок обновления и синхронизации](network/docs/ru/GOVERNED_UPGRADE_1_2_2.md).

[Однострочник установки](https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.3/INSTALL_RU.md) ·
[Проверка издателя](network/docs/ru/SOFTWARE_RELEASE_VERIFICATION.md) ·
[Офлайн-комплект](network/docs/ru/INSTALL_LAUNCH_TOOLS.md).

![MintCandy Network](network/docs/ru/media/mintcandy-network-release-cover.png)

## Две отдельные сборки

| Исходники | Назначение |
| --- | --- |
| [`network/`](network/README.md), `mcnd` | Новая MintCandy Network на SDK 0.53 / Cosmos EVM / IBC Go 10. В обычную сборку входят IBC/transfer; разрешённые нативные финансовые маршруты включаются проверенным профилем сети. |
| Корневой Go-модуль, `dscd` | Legacy-совместимость, исправления надёжности и отдельный исторический replay Decimal. Его экспериментальный IBC защищён тегом `experimental_ibc` и изолированными chain IDs. |

Утверждение «обычная сборка не подключает transfer» относится только к
legacy-модулю. Оно не описывает `mcnd`. Базы, genesis, зависимости и результаты
проверок этих сборок нельзя смешивать. Работающие Decimal-ноды не заменяйте
экспериментальным бинарником и не переносите их DB в новую сеть.

## Что поддерживает нода

- Нативный набор валидаторов и голосующая мощность по активному MSP,
  включая учтённые hold и обеспечение NFT; комиссия без инфляционных наград.
- Изолированные NFT-позиции, общий ручной/автоматический complete,
  DRC20-конвертация обеспечения с `minOut`, порционные штрафы и восстановление.
- Массовое делегирование: по умолчанию до 1000 нативных записей на валидатора.
  Награды начисляются из комиссий и автоматически выплачиваются страницами
  после интервала 120 блоков. Это не обещание выплаты всем за один блок.
- Оплата gas DRC20 с доступной публичной конвертацией резерва в нативный MSP.
  У резервных и безрезервных токенов единый реестр заглавных латинских имён.
- ICS-20 и Hermes в изолированных сетях; голосование валидаторов и делегаторов.
  На одно предложение действует предел 256 голосующих адресов, поэтому
  1000 делегаций не означают неограниченный размер голосования.

Нативный финансовый профиль принимает разрешённые системные EVM-маршруты,
а не произвольные dApp-вызовы. SDK bank `MsgSend` поддерживается; обычный
Ethereum value CALL на EOA сейчас отклоняется. Совпадение ключа/адреса
кошелька не гарантирует стандартную кнопку native-перевода MetaMask.
[Точные границы кошельков, фабрики токенов и EVM](network/README.md#границы-текущего-профиля).

Мост к Decimal остаётся отдельным прототипом и не заявлен готовым для
реальных средств. DEX, инвестиционный vesting, собственный stablecoin,
DePIN и торговый MEV-сервис не входят в первый согласованный выпуск.

## Подтверждённые отдельные проверки

| Проверка | Ревизия и результат |
| --- | --- |
| 1.2.3, оплата DRC20, возврат в MSP и tMSP, systemd, снапшот и Hermes | [Проверка обоих бинарников](https://github.com/maxwell2010/go-smart-node/actions/runs/45371010838) |
| 1.2.3, чистая установка двух публичных пакетов по однострочникам | [Прогон CI](https://github.com/maxwell2010/go-smart-node/actions/runs/45377377447) |
| 1.2.2, миграция с пакетом ICS-20 на 1 MSP, восстановление наблюдателя и обратный перевод | [Прогон CI](https://github.com/maxwell2010/go-smart-node/actions/runs/45351481993) |
| 1.2.2, подключение новой ноды из genesis и снапшота после миграции | [Прогон CI](https://github.com/maxwell2010/go-smart-node/actions/runs/45346495072) |
| v1.1.5: MSP в новом `mcnd init`, WMSP в genesis и 10 сценариев Hermes | [CI 45124076770](https://github.com/maxwell2010/go-smart-node/actions/runs/45124076770), success |
| v1.1.4: WMSP через EVM, проверка RPC перед снапшотом и 10 сценариев Hermes | [CI 45117195355](https://github.com/maxwell2010/go-smart-node/actions/runs/45117195355), success |
| v1.1.3: кворум, синхронизация, systemd, snapshot и 10 сценариев Hermes | [`8c8b844`, CI 45012997466](https://github.com/maxwell2010/go-smart-node/actions/runs/45012997466), success |
| v1.1.3: обрезка блоков с сохранением окна доказательств | [`8c8b844`, CI 45012994146](https://github.com/maxwell2010/go-smart-node/actions/runs/45012994146), success |
| Имена токенов, создание активов после пустого genesis, права валидатора | [`bf78808`, CI 45003565408](https://github.com/maxwell2010/go-smart-node/actions/runs/45003565408), success |
| Современный модуль и регрессии | [`2a5f4ff`, CI 44470608682](https://github.com/maxwell2010/go-smart-node/actions/runs/44470608682), success |
| Массовое делегирование 10/100/1000 | [`be7c179`, CI 44467813041](https://github.com/maxwell2010/go-smart-node/actions/runs/44467813041), success |

Полный перечень проверок с ревизиями и ожидающими решениями приведён в
[статусе релиза](docs/RELEASE_STATUS.md) и [плане запуска](network/docs/ru/LAUNCH_APPROVAL.md).

## Установка и документация

- [Русское руководство оператора: зависимости, сервер, установка, пиры и диск](network/docs/ru/README.md).
- [Проверка подписанного пакета и установка без локальной Go-сборки](network/docs/ru/OPERATOR_LAUNCH.md).
- [Синхронизация, snapshots, pruning и журналы](network/docs/ru/SYNCHRONIZATION_AND_STORAGE.md).
- [Массовое делегирование, штрафы и автоматические награды](network/docs/ru/MASS_DELEGATION_AND_REWARDS.md).
- [CLI самоуправления валидатора](network/docs/ru/VALIDATOR_SELF_CONTROL.md) и [тестовый грейс-период](network/docs/ru/TEST_GRACE_PERIOD.md) - доступны в [1.1.0](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.0).
- [Проверки и ограничения](network/docs/ru/VERIFICATION_LIMITS.md).
- [Legacy-установка RU](docs/INSTALL.ru.md) / [EN](docs/INSTALL.en.md),
  [история интеграции и replay](docs/integration-progress.md),
  [проверки надёжности](docs/reliability-validation.md).

Все испытания запускаются на отдельных стендах. Сохраняйте upstream LICENSE
и существующие уведомления об исходниках.
