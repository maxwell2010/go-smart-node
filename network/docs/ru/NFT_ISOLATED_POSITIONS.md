# Изолированное обеспечение NFT-позиций

Статус: утверждённая модель новой сети и проект API. Реализация, интеграция и
проверки этой модели идут отдельными группами. Исторические контракты и mainnet
на эту модель автоматически не переводятся.

Пользователь выбрал отдельный резерв каждой позиции. Для исходных NFT1155 одного
`contract + tokenId` одинаковые единицы остаются взаимозаменяемыми только до
изоляции. Делегирование создаёт новый NFT721 receipt, представляющий конкретное
количество исходных единиц и конкретное обеспечение. Штраф этой позиции меняет
только её резерв. При выходе возвращается receipt; автоматического возврата в
общий исходный NFT1155 ID нет.

## Идентичность и владение

Vault и контракт NFT721 receipt - один контракт/прокси. `positionId` имеет тип
`uint256`, начинается с 1, монотонно увеличивается и никогда не используется
повторно. ID 0 обозначает отсутствие позиции. В native-состоянии ID хранится как
полные 32 байта вместе с адресом vault и domain сети.

| Ссылка | Идентичность | Количество |
| --- | --- | --- |
| OriginRef | Исходные contract20/tokenId32 и точные native subID | N исходных единиц |
| CustodyRef | Vault20/positionId32 и native ID receipt | 1 NFT721 receipt |
| Parent | Предыдущий positionId32 при split | Отдельная lineage-ссылка |

NFT-модуль хранит единственный ledger идентичностей и lineage. При изоляции
исходные активные единицы погашаются и их точные native subID архивируются с
ссылкой на receipt. Для receipt создаётся самостоятельная внешняя/native
привязка. Validator bindings ссылаются на обе группы явных ID и не создают второй
NFT identity ledger. Количества OriginRef и CustodyRef независимы.

Физический владелец receipt определяется `ownerOf`. Во время делегирования это
UnifiedDelegation. Beneficiary и validator хранятся в UnifiedDelegation, поэтому
vault не содержит параллельного staking ledger. Передача всей свободной позиции
меняет владельца NFT721; reassignment делегированной позиции выполняется
авторизованным путём UnifiedDelegation с проверкой конечного состояния.

## Денежные инварианты

1. Для исходного fungible ID declared reserve равен `N * unitReserve` в одной
   фиксированной currency/kind. Начальный резерв кратен N; дальнейший mint вносит
   ровно `q * unitReserve` той же валюты. Существующий минимум начального
   обеспечения сохраняется. `addReserve` распределяет добавление равномерно
   среди оставшихся исходных единиц; некратное добавление отклоняется.
2. При отсутствии исходных единиц сохраняются currency и последняя цена единицы,
   но новый mint обязан внести полное обеспечение. Деление на нулевой supply
   не используется. Добавление к пустому исходному пулу требует отдельного
   определённого действия и не создаёт права на случайный surplus.
3. Прямое увеличение bank/ERC20 balance не меняет declared backing. Surplus
   учитывается отдельно от обязательств и не назначается позиции автоматически.
4. При изоляции q исходных единиц сжигаются как исходные NFT и переносят
   `q * unitReserve` в новый vault receipt. Изоляция сохраняет сумму обеспечения,
   токенов валюты и bank supply. Перенос сам по себе ничего не чеканит в bank.
5. При split позиции `(N, R)` дочерняя позиция получает
   `childR = floor(R * q / N)`. Родитель сохраняет `R - childR` и `N - q`.
   Для `q = N` передаётся весь остаток и исходный receipt закрывается. Сумма R
   сохраняется точно; остаток не распределяется между чужими позициями.
6. Штраф p процентов позиции A сжигает
   `lossA = floor(reserveA * p / 100)` и оставляет `reserveA - lossA`.
   Currency, reserve и права позиции B от этого не меняются. Внешняя оценка
   самой валюты - отдельная величина.
7. Для DEL и WDEL реальное уничтожение выполняется через утверждённый native
   burn backend с обязательным подтверждением. Bank supply уменьшается на
   уничтоженный native amount; WDEL supply и backing уменьшаются согласованно.
   Для других разрешённых ERC20 сохраняется явная модель burn этой валюты.
8. Нулевой reserve после авторизованного split или штрафа сохраняет backed-kind
   DEL/DRC20. Он не превращается в `ReserveLess` или `None`. В закреплённом ABI
   `INFTReserve.ReserveType`: `None=0`, `DEL=1`, `DRC20=2`.
9. В vault нет произвольного admin mint или sweep. Для ERC20 vault сам забирает
   согласованный amount у origin и проверяет точное увеличение balance; ранее
   присланный surplus не может оплатить новую позицию.
10. Ошибка позднего шага откатывает origin burn, перемещение обеспечения,
    mint/split receipt, все EVM/SDK записи и события. Комиссия транзакции
    проверяется отдельно по фактически учтённому gas.

## Проект ABI

Новая системная роль registry: `nft-position-vault`. Её установка происходит в
проверяемом bootstrap с финальной передачей управления SDK governance.

| Метод | Назначение |
| --- | --- |
| `getPosition(uint256)` | Полный origin, исходное/оставшееся количество, Reserve, parent ID, status и физический owner |
| `getReserve(uint256)` | Точный reserve конкретного receipt для интеграции с NFT721-путём |
| `mintFromOrigin(uint256,uint256,address)` | Только проверенный origin: tokenId, quantity, receiptOwner; точный funding определяется по unit reserve origin |
| `splitPosition(uint256,uint256,address)` | Авторизованный split с новым receipt и сохранением суммы обеспечения |
| `penalty(uint256,uint16)` | Только контроллер UnifiedDelegation для receipt в его custody |
| `addReserveByDEL(uint256)` / typed ERC20 top-up | Явное пополнение одной позиции |
| `redeemPosition(uint256,address)` | Закрытие свободного receipt с соблюдением burnable/refundable политики origin |
| `nextPositionId()` | Полный monotonic high-watermark для сверки native-состояния |

На новом origin1155 нужны `isolate(uint256,uint256,address) returns(uint256)`,
`getIsolationUnitReserve(uint256) returns(INFTReserve.Reserve)` и
`getIsolationPolicy(uint256) returns(bool,bool,bool)` для признаков
initialized/refundable/burnable. Unit reserve и currency сохраняются при нулевом
оставшемся исходном supply. Vault выводит origin contract из `msg.sender`,
проверяет регистрацию и capability origin и самостоятельно вычисляет funding.
Для ERC20 он выполняет pull с проверкой точного balance delta; для DEL требует
точное `msg.value`. Mint receipt не может использовать прежний surplus.

`getPosition` хранит полный origin contract/tokenId, origin standard, quantity,
reserve kind/token/amount и parent ID. Beneficiary/validator читаются из
UnifiedDelegation. Закрытая историческая позиция имеет отдельный status;
нулевой active reserve не означает закрытие.

Основные typed события проекта:

```solidity
event PositionIsolated(
    uint256 indexed positionId, address indexed originContract,
    uint256 indexed originTokenId, address receiptOwner,
    uint256 originQuantity, uint8 reserveType, address reserveToken,
    uint256 reserveAmount, bool refundable, bool burnable
);
event PositionSplit(
    uint256 indexed parentPositionId, uint256 indexed childPositionId,
    address indexed receiptOwner, uint256 childQuantity,
    uint256 childReserve, uint256 parentQuantity, uint256 parentReserve
);
event PositionReserveUpdated(
    uint256 indexed positionId, uint8 reserveType, address reserveToken,
    uint256 reserveAmount, uint256 originQuantity
);
event PositionPenalty(
    uint256 indexed positionId, uint16 percentage,
    uint256 burnedReserve, uint256 remainingReserve
);
event PositionRedeemed(
    uint256 indexed positionId, address indexed recipient,
    uint256 reserveAmount, bool refunded
);
```

Receipt `Transfer` сохраняет стандарт NFT721.
Native planner сопоставляет их с порядком transfer-логов, заранее захваченными
stake bindings и фактическим конечным состоянием. Выбор единиц нового stake
использует detached `NFTPlan.Movements`, а не повторный независимый lowest-ID
поиск. ABI окончательно закрепляется совместно с реализацией и тестами.

## Компактное закрытие новых receipt

Только для этого строго monotonic/non-reusable vault допускается authenticated
close с удалением текущих native token/binding/owner/URI записей закрытого и
сожжённого receipt. Коллекция сохраняет полный next-ID/high-watermark. Активный
child содержит полный свой origin subset и backing и не зависит от записи
удалённого parent. Повторное открытие старого ID отклоняется.

Обычные origin NFT сохраняют lifetime cursors и не получают эту оптимизацию:
generic ID может допускать remint. Live backing и активная lineage не удаляются.
Это отдельная операция current-state compaction с export/import и атомарными
проверками индексов; pruning исторических блоков решает другую задачу. Лимиты
коллекций не повышаются автоматически.

Пока не исполнен отложенный штраф, физический reserve и эффективный claim могут
различаться. Staking projection должна учитывать pending liability отдельно;
необработанный physical balance не становится чистым voting backing.

## Обязательные обычные проверки

- A и B по 10 DEL: после штрафа A на 10% A получает 9, B получает 10 при обоих
  порядках вывода; bank supply и native burn уменьшаются ровно на 1 DEL с
  отдельным учётом остальных block mint/fee effects.
- Split сохраняет полную сумму на маленьких значениях и округлении, включая
  `q = N`, нулевого дочернего reserve и независимые последующие штрафы.
- Старый slashed receipt не может автоматически разбавить исходный fungible ID.
- Mint/top-up в другой currency, неравномерное пополнение и недостаточный funding
  отклоняются; обычный surplus не увеличивает claim.
- Ошибка позднего действия откатывает обе стороны переноса, receipt, native
  bindings, bank/ERC20 balances, supply и события.
- Полные uint256 ID, lineage, точные origin/receipt native IDs и backing
  сохраняются при export/import и перезапуске хранилища.

Shared-pool экономику старого gateway нельзя выдавать за изолированное
обеспечение. До завершения этих проверок новый mint/backing admission остаётся
закрытым, а новая модель не объявляется готовым production-релизом.
