# Native NFT state staging

This storage layer imports validated complete genesis records atomically and
keeps token metadata, active sub-tokens and ownership indexes separately. It
retains the difference between an inherited reserve and an explicit zero
reserve. Transfers leave reserve values and minted/burnt counters unchanged.
Storage errors, a later invalid item and event conversion failures discard the
entire owner/index/event batch. Tests use actual SDK stores and CacheContext.

This is a new-network layout with a 64 KiB bound per metadata record. Collection
and token reads are shallow; never pass those records to a full supply checker.
`ExportGenesis` hydrates counters and active sub-tokens, verifies secondary
indexes and rejects orphan records before running the complete supply checker.
The explicit external binding layout preserves network domain, EVM chain ID,
20-byte collection address and every byte of the uint256 external token ID.
Legacy native IDs stay unchanged; new IDs use the versioned domain-separated
`FreshExternalTokenID` recipe. Primary/native and reverse/external indexes must
agree. `NextSubTokenID` is lifetime minted count plus one, with terminal `2^32`
cursor supported; previously burned unit IDs are never reused. The domain-aware
constructor requires the matching SDK context chain ID; the legacy constructor
rejects bound genesis.

`ListActiveSubTokenIDs` pages ownership by ascending unit ID, at most 256 per
request, using an exclusive uint32 cursor. Collection-wide ERC1155 URIs use an
explicit multimap. `ListTokenIDsByURI` pages by stored ID hash; `GetTokenByURI`
returns an ambiguity error if multiple native tokens share the URI. Legacy
unbound URI uniqueness remains enforced. Complete export checks both mappings,
all URI and owner indexes, counters and collection membership before returning
genesis. Export/reimport retains explicit reserve-less metadata and display
labels as well as bindings.

Reserve-less is an explicit profile requiring an assigned external collection
and token identity, with exactly zero DEL default backing. A slashed explicit
zero on a reserve-backed unit remains a separate state. A binding is identity,
not proof of emitter/code authentication or financial backing. Actual receipt
admission still needs independently approved factory, family and code hashes.
No mint/burn/reserve pipeline is enabled by these Go APIs. ERC1155 financial
projection remains sealed pending actual contract claim reconciliation: the
contract uses floor(pool/supply), with no invented remainder allocation.

Mint/burn/reserve transitions, bank backing reconciliation, authenticated EVM
receipt admission and public transaction routes remain separate gates. No
methods here authenticate a transaction signer or system contract. The owned
application must continue rejecting nonempty collections or bindings until
those gates are satisfied; empty module/query mounting does not activate them.

## Проверенная модель происхождения isolated receipt

Genesis хранит `isolated_vaults` (полный domain, vault, монотонный uint256
`next_position_id`, коллекция квитанций) и `isolated_positions` (живые квитанции).
`GetIsolatedVaultState` и `GetIsolatedLineage` возвращают отдельные копии записей.
У каждой живой квитанции сохраняются origin contract/token ID, явный native ID
и точный отсортированный набор сожжённых origin sub-ID, включая состояние в
обычном кошельке после вывода из делегации. Parent ID информационный: активный
ребёнок не требует записи закрытого родителя. Единственный реестр происхождения
находится в NFT-модуле; validator bindings ссылаются на него.

`IsolatedReserveSnapshot` сохраняет фактический declared reserve контракта как
kind/token/uint256 amount. Нулевой резерв сохраняет backed kind DEL или ERC20.
Он не изменяет default reserve исторического Token и не является автоматически
подтверждённым голосующим весом после отложенных штрафов. Подлинность контрактов,
обеспечение и финансовая проекция требуют отдельного authenticated admission.
Текущие методы создания этих записей доступны только внутреннему genesis import;
публичные Msg и автоматическая активация по отдельному событию отсутствуют.
App обязан отклонять непустое новое состояние до установки такого admission.

Импорт и полный экспорт проверяют domain, полный uint256 ID, receipt721 qty1,
существующий origin1155, отсутствие архивных origin units в живой популяции,
непересекающиеся наборы между квитанциями, high-watermark и оба обратных индекса.
Все записи и индексы импортируются в одном cache context. Проверяются перенос
квитанции между кошельками, export/import, нулевой backed reserve, независимый
ребёнок, отказ на повреждённом/лишнем индексе и rollback поздней ошибки записи.
Это foundation состояния: runtime compaction закрытых receipt и повторное
открытие должны быть реализованы в authenticated close planner отдельно.

Предел равен `types.MaxIsolatedOriginUnits = 1024`. В тестовом native state
protobuf-запись с 1024 origin IDs занимает 2221 байт. Полный checked getter этой
позиции расходует 4 182 560 SDK gas на обычном тестовом store; 1025 отклоняется
до записи. Getter не сканирует другие позиции и проверяет не более 1024 её
origin индексов. Это отдельный primitive benchmark, не оценка полного signed
EVM + NFT + validator pipeline. Production gas budgets не увеличены.


Для новой квитанции с изначально нулевым резервом Token содержит явный профиль
`isolated_receipt=true`, `reserve_less=false` и настоящий нулевой default reserve.
Положительный фиктивный default не создаётся. Флаг разрешён только для backed
ERC721, связанного с известным vault и живой exact lineage; generic NFT не может
активировать этот профиль. Обычный getter проверяет bounded raw profile records,
не вызывая рекурсивно полный getter lineage. Пропавшие записи у известной
квитанции считаются повреждением состояния. Все текущие writers проходят полную
проверку genesis; runtime admission остаётся закрытым до authenticated planner.

Обычный тест сохраняет zero-backed child с информационным закрытым parent ID,
переносит квитанцию в другой кошелёк, выполняет commit настоящего IAVL/LevelDB,
закрывает и повторно открывает отдельную тестовую БД. Полный protobuf-export,
kind1, нулевой default reserve, флаг профиля и владелец сохраняются. Это проверка
сохранности состояния, не разрешение пропускать подтверждение EVM обеспечения.
