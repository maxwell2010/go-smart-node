# Какие транзакции доступны для пользовательских токенов и NFT

Сверка экспериментальной ветки MintCandy для tMSP. В таблицах перечислены все изменяющие состояние методы двух фабрик и шести семейств зарегистрированных активов, включая перегрузки. Один метод в двух семействах учитывается дважды. Проверку каталога можно повторять без RPC.

## Как читать результат

- **Кошелёк** означает, что в ноде есть путь обработки прямой подписанной транзакции. Применяются права контракта, флаги актива, правила ноды и параметры конкретной сети.
- **Тестнет** означает новый рынок в профиле tMSP. **Активация** означает возврат нативного резерва владельцу через burn после активации сетевого обновления.
- **Закрыто** означает, что для этой сигнатуры нет отдельного публичного пути от кошелька. **Служебная** означает инициализацию, управление контрактом, штраф или изоляцию стейка. Включение таких вызовов в универсальный кошелёк нарушило бы границу ролей.
- **SDK** сверялся с локальным каталогом `NATIVE_EVM_SIGNATURES`. **Профиль** в колонке консоли означает, что форма зависит от разрешений конкретного профиля и проверки кошелька. **Скрыта** означает явный запрет в общей форме. **Возврат** использует отдельную форму и проверку активации в поддерживаемом клиентском профиле.
- Строки описывают допуск по коду. Подтверждённые прогоны и ограничения указаны отдельно. Торговля между двумя пользователями, NFT-маркетплейс, IBC и стейкинг используют другие контракты или маршруты. Обычный перевод NFT сам по себе не обеспечивает встречную оплату.

## Что важно для приложений

1. **DRC20 с резервом.** В торговой ветке кошелёк передаёт точную входную сумму и минимальный выход. Два метода точного выхода и альтернативные имена из Solidity напрямую закрыты. Изменение пределов выпуска, обновление identity и сжигание без продажи также закрыты. Для новых сделок нужны версия SDK и профиль консоли, поддерживающие этот рынок. [ABI, котировки, события и коды отказа](DRC20_RESERVE_TRADES_TMSP.md)
2. **DRC20 без резерва.** Дополнительный выпуск требует владельца и флага mintable. Сжигание требует burnable. Расход чужих средств требует достаточного allowance. Действия по allowance доступны в SDK, общая форма консоли скрывает transferFrom для DRC20 и burnFrom. Встроенной продажи за резерв у этого семейства нет.
3. **Коллекции NFT.** Фабрика создаёт четыре вида коллекций. При выпуске с нативным обеспечением имя метода в ABI остаётся mintByDEL, а единица платежа определяется профилем сети. Выпуск без обеспечения принимает нулевую сумму и нулевой адрес резервного токена. Для DRC1155 за один выпуск допускается 1-128 единиц.
4. **Переводы и разрешения NFT.** DRC721 поддерживает approve для одного ID и setApprovalForAll для коллекции. DRC1155 использует второе. Кошельки могут отозвать разрешение. Прямая передача поддерживает обычного получателя и пустое поле данных. Нода отклоняет произвольные данные и callback контракта. Общая форма консоли для перевода NFT проверяет принадлежность выбранному кошельку.
5. **Резервы и сжигание NFT.** Для возврата требуется активированный маршрут, возвращаемый нативный резерв и единицы исходного NFT на кошельке подписанта. За один возврат DRC1155 можно погасить до 128 единиц. Погашение квитанции стейкинга отдельно использует redeemPosition. Прямые пополнения резерва, пакетные переводы DRC1155, подпись permit и сжигание безрезервных NFT не имеют публичного маршрута.
6. **За пределами фабричного ABI.** Загрузка и восстановление файлов аватаров, вложение сделки в пакет стейкинга, межсетевой перевод и получение цены требуют своих интерфейсов. MsgBuyCoin и MsgSellCoin в protobuf сами по себе не предоставляют обработчик публичной сделки. Плату за регистрацию, газ и взнос в резерв учитывают отдельно. [Тариф выпуска и уникальность тикера](DRC20_ISSUANCE_TMSP.md)

## Совместимость с нативными сообщениями Decimal

В обоих модулях MintCandy регистрирует API чтения. Объявленные в protobuf сообщения перечислены ниже, чтобы клиенты не предлагали подписать их как доступные транзакции.

| Модуль | Сообщения исходной схемы | Работающий путь для клиента |
|---|---|---|
| coin | CreateCoin, SendCoin, BuyCoin, SellCoin, BurnCoin | Фабрика или конкретный DRC20 согласно таблицам и профилю сети. Нативную монету кошелёк переводит отдельно. |
| coin | UpdateCoin, MultiSendCoin, SellAllCoin, RedeemCheck | Прямой обработчик этих сообщений не зарегистрирован. Продажа всего остатка через рынок подчиняется минимумам резерва и выпуска. |
| nft | MintToken, SendToken, BurnToken | Фабричные EVM-активы используют только маршруты из таблиц. |
| nft | UpdateToken, UpdateReserve | Для этих сообщений нет публичного нативного обработчика. В частности, выпуск с URI не даёт владельцу операции последующей замены URI. |

Регистрацию служб можно проверить в исходниках модулей [coin](../../decimal/coin/module/module.go) и [nft](../../decimal/nft/module/module.go).

## Что исполнено на изолированной сети

В прогоне торговой ветки покупка прошла на высоте 145, продажа на 149. Из двух покупок на остаток выпуска в блоке 181 прошла одна. Проверялись кошельки с учётом газа и наград, контракт, нативный реестр и состояние после отказов. Полные хеши всех 11 заявок и причины отказов есть в отчёте. [Квитанции](evidence/drc20-reserve-trades-45667636127.json) · [GitHub Actions](https://github.com/maxwell2010/go-smart-node/actions/runs/45667636127)

Оба семейства DRC20 проверены при создании для всех длин тикера, с распределением 80% платы в тестовую казну и сжиганием 20%. [Прогон выпуска](https://github.com/maxwell2010/go-smart-node/actions/runs/45663532103)

Для NFT доступны сценарии подписанных транзакций через приложение с фиксацией блоков и перезапуском. Сценарий создания, выпуска и перевода охватывает четыре семейства. Возврат резерва проверяется в отдельном сценарии. Эти проверки не являются квитанциями публичной сети. [Жизненный цикл NFT](../../systemcontracts/user_nft_native_app_test.go) · [Возврат резерва](../../systemcontracts/native_user_nft_refund_app_test.go) · [Права и allowance DRC20](../../systemcontracts/reserveless_native_app_test.go)

## Проверка каталога

Из корня репозитория запустите `python network/tools/check_asset_operations.py`. При добавлении или удалении сигнатуры проверка сообщит точный контракт и метод. Флаг `--sdk <путь-к-system-abis.generated.ts>` дополнительно сверяет колонку SDK с копией исходников клиента. Матрица не использует данные кошельков и не подключается к сети.

### token-center

| Сигнатура | Нода | SDK | Общая форма консоли |
|---|---|---|---|
| `convert(address,address,uint256,uint256,address)` | закрыто | нет | нет |
| `convert(address,address,uint256,uint256,address,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `createToken((uint256,uint256,uint256,address,uint8,string,string,string))` | кошелёк | да | профиль |
| `createTokenReserveless(string,string,bool,bool,uint256,uint256,string)` | кошелёк | да | профиль |
| `initialize()` | служебная | нет | нет |
| `initializeFresh(address)` | служебная | нет | нет |
| `pause()` | служебная | нет | нет |
| `renounceOwnership()` | служебная | нет | нет |
| `setContractCenter(address)` | служебная | нет | нет |
| `transferOwnership(address)` | служебная | нет | нет |
| `unpause()` | служебная | нет | нет |
| `upgrade(address,bytes)` | служебная | нет | нет |
| `upgradeToken(address)` | служебная | нет | нет |
| `upgradeTokenReserveless(address)` | служебная | нет | нет |

### nft-center

| Сигнатура | Нода | SDK | Общая форма консоли |
|---|---|---|---|
| `createDRC1155((address,string,string,string,bool,bool))` | кошелёк | да | профиль |
| `createDRC1155Reserveless((address,string,string,string,bool,bool))` | кошелёк | да | профиль |
| `createDRC721((address,string,string,string,bool,bool))` | кошелёк | да | профиль |
| `createDRC721Reserveless((address,string,string,string,bool,bool))` | кошелёк | да | профиль |
| `initialize()` | служебная | нет | нет |
| `initializeFresh(address)` | служебная | нет | нет |
| `pause()` | служебная | нет | нет |
| `renounceOwnership()` | служебная | нет | нет |
| `setContractCenter(address)` | служебная | нет | нет |
| `transferOwnership(address)` | служебная | нет | нет |
| `unpause()` | служебная | нет | нет |
| `upgrade(address,bytes)` | служебная | нет | нет |
| `upgradeNFT(address,uint8,bool)` | служебная | нет | нет |

### drc20-backed

| Сигнатура | Нода | SDK | Общая форма консоли |
|---|---|---|---|
| `approve(address,uint256)` | кошелёк | да | профиль |
| `burn(uint256)` | закрыто | нет | нет |
| `buy(uint256,address)` | тестнет | нет | нет |
| `buyExactTokenForDEL(uint256,address)` | закрыто | нет | нет |
| `buyTokenForExactDEL(uint256,address)` | закрыто | нет | нет |
| `initialize(string,string,address,uint8,uint256,uint256,uint256,string)` | служебная | нет | нет |
| `permit(address,address,uint256,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `sell(uint256,uint256,address)` | тестнет | нет | нет |
| `sellExactTokensForDEL(uint256,uint256,address)` | закрыто | нет | нет |
| `sellTokensForExactDEL(uint256,uint256,address)` | закрыто | нет | нет |
| `transfer(address,uint256)` | кошелёк | да | профиль |
| `transferFrom(address,address,uint256)` | кошелёк | да | скрыта |
| `updateIdentity(string)` | закрыто | нет | нет |
| `updateMaxTotalSupply(uint256)` | закрыто | нет | нет |
| `updateMinTotalSupply(uint256)` | закрыто | нет | нет |

### drc20-reserveless

| Сигнатура | Нода | SDK | Общая форма консоли |
|---|---|---|---|
| `approve(address,uint256)` | кошелёк | да | профиль |
| `burn(uint256)` | кошелёк | да | профиль |
| `burnFrom(address,uint256)` | кошелёк | да | скрыта |
| `initialize(string,string,address,bool,bool,uint256,uint256,string)` | служебная | нет | нет |
| `mint(address,uint256)` | кошелёк | да | профиль |
| `permit(address,address,uint256,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `renounceOwnership()` | служебная | нет | нет |
| `transfer(address,uint256)` | кошелёк | да | профиль |
| `transferFrom(address,address,uint256)` | кошелёк | да | скрыта |
| `transferOwnership(address)` | служебная | нет | нет |

### drc721-backed

| Сигнатура | Нода | SDK | Общая форма консоли |
|---|---|---|---|
| `addReserve(uint256,uint256)` | закрыто | нет | нет |
| `addReserveByDEL(uint256)` | закрыто | нет | нет |
| `addReserveByPermit(uint256,uint256,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `approve(address,uint256)` | кошелёк | да | профиль |
| `burn(uint256)` | активация | возврат | возврат |
| `disableMint()` | закрыто | нет | нет |
| `initialize(string,string,string,address,bool,bool)` | служебная | нет | нет |
| `mint(address,string,uint256,address)` | закрыто | нет | нет |
| `mintByDEL(address,string)` | кошелёк | да | профиль |
| `mintByPermit(address,string,uint256,address,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `penalty(uint256,uint16)` | служебная | нет | нет |
| `permit(address,uint256,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `safeTransferFrom(address,address,uint256)` | кошелёк | да | профиль |
| `safeTransferFrom(address,address,uint256,bytes)` | кошелёк | да | профиль |
| `setApprovalForAll(address,bool)` | кошелёк | да | профиль |
| `transferFrom(address,address,uint256)` | кошелёк | да | профиль |

### drc721-reserveless

| Сигнатура | Нода | SDK | Общая форма консоли |
|---|---|---|---|
| `approve(address,uint256)` | кошелёк | да | профиль |
| `burn(uint256)` | закрыто | нет | нет |
| `disableMint()` | закрыто | нет | нет |
| `initialize(string,string,string,address,bool,bool)` | служебная | нет | нет |
| `mint(address,string,uint256,address)` | кошелёк | да | профиль |
| `permit(address,uint256,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `safeTransferFrom(address,address,uint256)` | кошелёк | да | профиль |
| `safeTransferFrom(address,address,uint256,bytes)` | кошелёк | да | профиль |
| `setApprovalForAll(address,bool)` | кошелёк | да | профиль |
| `transferFrom(address,address,uint256)` | кошелёк | да | профиль |

### drc1155-backed

| Сигнатура | Нода | SDK | Общая форма консоли |
|---|---|---|---|
| `addReserve(uint256,uint256)` | закрыто | нет | нет |
| `addReserveByDEL(uint256)` | закрыто | нет | нет |
| `addReserveByPermit(uint256,uint256,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `burn(uint256,uint256)` | активация | возврат | возврат |
| `disableMint()` | закрыто | нет | нет |
| `initialize(string,string,string,address,bool,bool)` | служебная | нет | нет |
| `isolate(uint256,uint256,address)` | служебная | нет | нет |
| `isolateForDelegation(uint256,uint256,address,uint256,uint256)` | служебная | нет | нет |
| `mint(address,uint256,uint256,string,uint256,address)` | закрыто | нет | нет |
| `mintByDEL(address,uint256,uint256,string)` | кошелёк | да | профиль |
| `mintByPermit(address,uint256,uint256,string,uint256,address,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `permit(address,address,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `safeBatchTransferFrom(address,address,uint256[],uint256[],bytes)` | закрыто | нет | нет |
| `safeTransferFrom(address,address,uint256,uint256,bytes)` | кошелёк | да | профиль |
| `setApprovalForAll(address,bool)` | кошелёк | да | профиль |

### drc1155-reserveless

| Сигнатура | Нода | SDK | Общая форма консоли |
|---|---|---|---|
| `burn(uint256,uint256)` | закрыто | нет | нет |
| `disableMint()` | закрыто | нет | нет |
| `initialize(string,string,string,address,bool,bool)` | служебная | нет | нет |
| `mint(address,uint256,uint256,string,uint256,address)` | кошелёк | да | профиль |
| `permit(address,address,uint256,uint8,bytes32,bytes32)` | закрыто | нет | нет |
| `safeBatchTransferFrom(address,address,uint256[],uint256[],bytes)` | закрыто | нет | нет |
| `safeTransferFrom(address,address,uint256,uint256,bytes)` | кошелёк | да | профиль |
| `setApprovalForAll(address,bool)` | кошелёк | да | профиль |
