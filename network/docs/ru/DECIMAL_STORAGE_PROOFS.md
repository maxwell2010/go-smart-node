# Доказательства storage Decimal для моста без комитета

Исследование от 18 ноября 2026 года. Эта записка описывает проверенный по исходникам и публичным запросам путь доказательства. Рабочий bridge-verifier в рамках исследования не внедрялся, контракты не развёртывались, транзакции и подписи не отправлялись.

## Результат

Storage Decimal можно доказывать через Cosmos ICS-23, без Ethereum MPT. Точный путь:

```text
Solidity slot32
  -> key = 0x02 || contractAddress20 || slot32
  -> IAVL store "evm"
  -> multistore AppHash состояния H
  -> AppHash в Tendermint header H+1
  -> проверенный light client состояния Decimal
```

Наличие двух Merkle-доказательств не подтверждает самостоятельно доверенность AppHash. До приёма депозита нужно проверить Tendermint-заголовок и его связь с доверенным checkpoint, набором валидаторов и подписями. Код и идентичность escrow также должны быть зафиксированы. Отсутствующий IBC transfer-модуль Decimal этому низкоуровневому доказательству storage не мешает.

## Исследованные версии и источники

Публичный master Decimal `a46cb48d7965c1a64a495cebb22e735dfe4b9da3` использует `decimalteam/ethermint v0.22.13`, Cosmos SDK `v0.46.16`, `confio/ics23/go v0.9.0`; Tendermint заменён CometBFT `v0.34.29`. Наш отдельный модуль использует IBC-Go `v10.7.0` и `cosmos/ics23/go v0.11.0`.

Источники:

- [Manifest Decimal](https://github.com/decimalteam/go-smart-node/blob/a46cb48d7965c1a64a495cebb22e735dfe4b9da3/go.mod).
- [Ethermint StateKey](https://github.com/decimalteam/ethermint/blob/v0.22.13/x/evm/types/key.go#L39-L77).
- [Ethermint GetState, SetState и code storage](https://github.com/decimalteam/ethermint/blob/v0.22.13/x/evm/keeper/statedb.go).
- [Ethermint GetProof](https://github.com/decimalteam/ethermint/blob/v0.22.13/rpc/backend/account_info.go#L54-L132).
- [ABCI proof query](https://github.com/decimalteam/ethermint/blob/v0.22.13/rpc/types/query_client.go#L51-L78).
- [SDK IAVL query](https://github.com/cosmos/cosmos-sdk/blob/v0.46.16/store/iavl/store.go#L293-L359).
- [SDK multistore query](https://github.com/cosmos/cosmos-sdk/blob/v0.46.16/store/rootmulti/store.go#L648-L694).

Точную сборку бинарника публичного RPC эти версии сами по себе не удостоверяют. Поэтому ниже отдельно приведены наблюдения реального API и отдельно остающиеся проверки совместимости.

## 1. Solidity slot и ключ Cosmos

В Ethermint `prefixCode = 1`, `prefixStorage = 2`, `StoreKey = "evm"`. `AddressStoragePrefix(address)` возвращает `0x02 || address20`; `StateKey(address, key)` дописывает байты slot.

Для уже вычисленного 32-байтового slot:

```text
stateKey = [0x02] + address.Bytes() + slot.Bytes()
len(stateKey) = 1 + 20 + 32 = 53 байта
```

Никакого дополнительного keccak(address) или keccak(slot) на уровне Ethermint нет. Solidity самостоятельно вычисляет slot до передачи в EVM. Для `mapping(bytes32 => bytes32)` в позиции `p`:

```text
slot = keccak256(messageID32 || uint256(p) как 32 байта big endian)
```

Для другой структуры mapping, вложенных mapping, packed-полей и массивов формула отличается. Позицию `p` нельзя угадать по ABI: её нужно закрепить по `storageLayout` конкретной компиляции escrow, вместе с bytecode и версией компилятора. Общие правила описаны в [Solidity storage layout](https://docs.soliditylang.org/en/v0.8.20/internals/layout_in_storage.html).

При delegatecall storage принадлежит proxy-адресу. Адрес implementation в ключе вместо proxy докажет другой набор данных.

## 2. Точный read-only RPC

Рекомендуемый основной API сохраняет полные типы и ключи proof operations:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "abci_query",
  "params": {
    "path": "/store/evm/key",
    "data": "0x<53 bytes StateKey>",
    "height": "<H>",
    "prove": true
  }
}
```

Запрос направляется на Tendermint RPC, например `https://node.decimalchain.com/rpc`. Аналогичный GET использует URL-encoded строку `path="/store/evm/key"`, `data=0x...`, `height=H`, `prove=true`. Нужно проверить `response.code == 0`, `response.height == H`, точное совпадение `response.key`, а также наличие обеих proof operations. JSON-поля key/value/data кодируются base64; это не hex-строки.

Альтернативный Web3 API:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "eth_getProof",
  "params": ["0x<address20>", ["0x<slot32>"], "0x<explicit H>"]
}
```

В Ethermint каждый элемент `storageProof[].proof[]` содержит hex от protobuf `CommitmentProof`, то есть `ProofOp.Data`. `GetHexProofs` не сохраняет `ProofOp.Type` и `ProofOp.Key`. Поэтому raw ABCI удобнее для строгого контроля формата. `storageHash` в исходниках явно выставлен в нулевой `common.Hash{}`: это не Ethereum storage trie root. Поле `value` Web3 возвращает число, тогда как Merkle verifier должен получать исходные байты `ABCI response.value`.

Не использовать `latest` в протоколе исполнения: сборщик фиксирует H и подтверждает его заголовком H+1. Явный H также исключает смену latest между несколькими запросами.

## 3. Proof operations и порядок

SDK IAVL создаёт первую операцию, затем root multistore добавляет вторую:

| Позиция | Type | Key | Что доказывает |
|---|---|---|---|
| 0 | `ics23:iavl` | `0x02 || address20 || slot32` | Значение или отсутствие ключа в IAVL дерева evm |
| 1 | `ics23:simple` | ASCII `evm`, hex `65766d` | Корень evm store в общем AppHash |

Каждая `Data` - сериализованный protobuf `ics23.CommitmentProof`. Для первой операции допустимы два разных случая:

- Ключ есть: membership над точными байтами значения.
- Ключ отсутствует: non-membership; чтение EVM при этом возвращает нулевой word.

Нулевое числовое значение из `eth_getStorageAt` само по себе не выбирает вид доказательства. Нужно смотреть реальный ABCI value и CommitmentProof. `StateDB.Commit` передаёт в keeper `value.Bytes()`, а keeper удаляет ключ только при пустом byte slice; поэтому нельзя считать любой нулевой word непременно отсутствующим ключом. Для разрешения mint по депозиту разумно требовать положительный, непустой commitment, а не использовать отсутствие/ноль как подтверждение операции.

Источники: [proof op types](https://github.com/cosmos/cosmos-sdk/blob/v0.46.16/store/types/proof.go), [StateDB.Commit](https://github.com/decimalteam/ethermint/blob/v0.22.13/x/evm/statedb/statedb.go#L491-L497).

## 4. Высота AppHash: H и H+1

Явный ABCI-запрос с `height=H` читает IAVL version H. Результирующий root состояния после H появляется в поле AppHash заголовка H+1. Это прямо отмечено в SDK IAVL query: `header height = data height + 1`.

В `rpc/types/query_client.go` остался комментарий про запрос на высоте ниже заданной, но фактический код устанавливает `RequestQuery.Height = clientCtx.Height`, без вычитания единицы. `client.Context.queryABCI` тоже передаёт явную высоту без вычитания. Реальный ответ, приведённый ниже, подтверждает `response.height = requested H`.

Для `eth_getProof(..., H)` доказательство относится к состоянию H; доверенный root нужно брать из проверенного заголовка H+1. Если proof передаётся через light-client API, высота consensus state для root будет H+1. Нельзя назвать высоту state H и высоту подписанного header H+1 одним полем без явного соглашения протокола.

Если сеть остановилась сразу после H и заголовок H+1 не подтверждён, доверять state H только по `abci_info` или локальному RPC нельзя. Мост ждёт доступного подтверждённого root. Это особенно существенно на последнем блоке остановленной сети Decimal.

Источники: [SDK getHeight и Query](https://github.com/cosmos/cosmos-sdk/blob/v0.46.16/store/iavl/store.go#L293-L325), [SDK client.queryABCI](https://github.com/cosmos/cosmos-sdk/blob/v0.46.16/client/query.go#L80-L100).

## 5. Совместимость с IBC-Go 10: что подтверждено

`ibc-go/v10.7.0` содержит:

```text
commitmenttypes.ConvertProofs(*Comet crypto.ProofOps) (MerkleProof, error)
commitmenttypes.GetSDKSpecs() -> [ics23.IavlSpec, ics23.TendermintSpec]
commitmenttypes.NewMerklePath(keys ...[]byte)
MerkleProof.VerifyMembership(specs, root, path, value)
MerkleProof.VerifyNonMembership(specs, root, path)
```

`NewMerklePath` в этой версии возвращает path v2 с сырыми byte keys. Порядок path - от root к leaf, а proofs - от leaf к root. Поэтому требуемая конструкция:

```text
proofs = ConvertProofs(ABCI proofOps)
path = NewMerklePath([]byte("evm"), exactStateKey53)
root = NewMerkleRoot(authenticatedHeaderHPlus1.AppHash)
VerifyMembership(GetSDKSpecs(), root, path, exactABCIValue)
```

Это проверенный по сигнатурам исходников конструктор API, не внедрённый bridge-код. Для отсутствующего ключа используется `VerifyNonMembership` с тем же path и root. Нельзя подставлять hex-текст ключа вместо 53 исходных байтов, добавлять prefix `ibc` или делать один строковый путь `/evm/<hex>`.

Перед `ConvertProofs` нужно самостоятельно проверить число операций, `Type`, `Key`, размеры и допустимый вид CommitmentProof. Сам `ConvertProofs` только декодирует `op.Data`; он не проверяет `Type` и `Key`. Merkle verifier проверяет leaf keys и значение по ожидаемому path, но это не заменяет явное ограничение формата входного пакета.

В версиях ICS-23 Go `v0.9.0` и `v0.11.0` используемые `IavlSpec` и `TendermintSpec` имеют одинаковые параметры:

| Spec | Leaf | Inner |
|---|---|---|
| IAVL | SHA256, key NO_HASH, value SHA256, VAR_PROTO length, prefix 00 | children [0,1], size 33, prefix min 4/max 12, SHA256 |
| Tendermint | SHA256, key NO_HASH, value SHA256, VAR_PROTO length, prefix 00 | children [0,1], size 32, prefix min/max 1, SHA256 |

Это подтверждает совпадение структуры используемых proof specs, а не выполнение conformance-теста всех доказательств Decimal новым verifier. В новой версии есть дополнительные проверки входов. До интеграции нужен golden test с реально полученными существующим и отсутствующим ключами, неверными root/path/value/height, а также подменой типа операции.

Источники:

- [IBC-Go 10 ConvertProofs](https://github.com/lfdt-ibc/ibc-go/blob/v10.7.0/modules/core/23-commitment/types/utils.go).
- [IBC-Go 10 MerkleProof, GetSDKSpecs](https://github.com/lfdt-ibc/ibc-go/blob/v10.7.0/modules/core/23-commitment/types/merkle.go).
- [IBC-Go 10 raw-byte path constructor](https://github.com/lfdt-ibc/ibc-go/blob/v10.7.0/modules/core/23-commitment/types/v2/merkle.go).
- [ICS-23 v0.9.0 specs](https://github.com/cosmos/ics23/blob/go/v0.9.0/go/proof.go#L9-L44).
- [ICS-23 v0.11.0 specs](https://github.com/cosmos/ics23/blob/go/v0.11.0/go/proof.go#L9-L44).

Проверка storage вне `ibc` store не создаёт автоматически стандартный ICS-20 channel. Нашему bridge-модулю нужна отдельная привязка к аутентифицированному consensus state клиента Decimal, ограничениям escrow и статусу client. Один вызов `MerkleProof.VerifyMembership` с root, присланным пользователем, небезопасен.

## 6. Доверие к коду escrow и proxy

Доказанный storage word имеет смысл только для известного контракта с известной логикой.

1. В store `acc` account key имеет вид `0x01 || address20`. Его value - protobuf Any аккаунта, содержащий Ethermint EthAccount. `EthAccount.code_hash` нужно извлекать из доказанного account value, а не доверять внешнему JSON-полю `eth_getProof.codeHash`.
2. Code storage в `evm` имеет key `0x01 || codeHash32`. Проверка аккаунта закрепляет ожидаемый code hash. Полученный bytecode нужно сопоставить с этим hash и разрешённой сборкой.
3. Для proxy дополнительно требуется доказательство implementation slot по proxy-адресу в той же версии состояния. Для EIP-1967 это `0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc`. Нужны также правила admin/beacon/upgrades и доказанная логика implementation. Сам EIP-1967 layout не подтверждает, что любой контракт с word в этом slot действительно является разрешённым proxy.
4. Для нового самостоятельного escrow проще рассмотреть неизменяемый контракт без proxy и без административной возможности произвольной записи commitment или изъятия обеспечения. Это предложение архитектуры, а не утверждённый дизайн релиза.
5. Нужно фиксировать происхождение: chain ID, address, message domain/version, nonce, актив, amount и адрес назначения. Proof другого контракта или другой сети не должен удовлетворять этому домену.

Источники: [SDK account keys](https://github.com/cosmos/cosmos-sdk/blob/v0.46.16/x/auth/types/keys.go#L21-L35), [EthAccount schema](https://github.com/decimalteam/ethermint/blob/v0.22.13/proto/ethermint/types/v1/account.proto), [EIP-1967](https://eips.ethereum.org/EIPS/eip-1967).

## 7. Публичная проверка API без транзакций

В этой проверке выполнены только запросы чтения к `node.decimalchain.com`, explicit state height **34 604 902**. Контракт для наблюдения формата - существующий Decimal ContractCenter `0xc108715a06f76caa96fa2c943ebf05159c29a87d`. Это не адрес нашего будущего escrow.

Первый запрос - slot 0:

```text
ABCI code = 0
response.height = 34604902
response.value = empty
proof[0].type = ics23:iavl
proof[0].key = 02 || c108715a06f76caa96fa2c943ebf05159c29a87d || 32 zero bytes
proof[0].Data length = 2286 bytes
proof[1].type = ics23:simple
proof[1].key = 65766d
proof[1].Data length = 254 bytes
```

`eth_getProof` для того же адреса, slot и explicit H вернул value `0x0`, нулевой storageHash и побайтно те же два `Data`, представленные hex. Это подтверждает соответствие Web3 proof wire data и raw ABCI proof data на данном запросе.

Второй запрос - EIP-1967 implementation slot:

```text
ABCI code = 0
response.height = 34604902
response.value = 000000000000000000000000642b69a1adb72ab26b8d911887811f0e2ba11490
proof[0].type = ics23:iavl
proof[0].Data length = 1229 bytes
proof[1].type = ics23:simple
proof[1].Data length = 254 bytes
```

Это пример существующего 32-байтового значения, а не проверка доверенности implementation. Оба запроса имеют ожидаемую структуру доказательств.

Из заголовка **34 604 903** прочитаны:

```text
chain_id = decimal_75-1
app_hash = D061D5E0B4F404FE219B0FEFA2194E103EF2309F4F431DC459562BA138A9F131
```

В рамках этой записки не запускалась криптографическая проверка этих proof blobs новым ICS-23 verifier и не аутентифицировалась цепочка заголовков Decimal. Поэтому приведённый AppHash остаётся наблюдением публичного RPC, не готовым trusted checkpoint.

## 8. Следующий изолированный тест

До кода, принимающего депозит, нужен GitHub CI на точной новой dependency family:

1. Получить публичный fixture с exact H, full state key, raw value и обеими proof ops; закрепить SHA256 fixture.
2. Декодировать старые protobuf CommitmentProof новым ICS-23 и проверить membership/non-membership против ожидаемого root в header H+1.
3. Отрицательные случаи: неверные slot/address/store/root/value, лишний/переставленный proof op, подмена op type/key, H вместо H+1, пустое доказательство, zero-word вместо отсутствия.
4. Независимо проверить Tendermint signed header, соответствие набора валидаторов и связь с доверенным checkpoint. Если выполняется только шаг 2, явно обозначать его как проверку Merkle-совместимости без light-client аутентификации.
5. Проверить proof code hash, immutable/proxy policy и точный commitment layout escrow.
6. Только после этого подключать сохранённый nonce и атомарный mint/release. При ошибке proof не менять ни nonce, ни supply, ни состояние выплаты.

Тяжёлые сборки, реальные ноды и этот conformance-тест локально не запускались. Работающие ноды не изменялись.
