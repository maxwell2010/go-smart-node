# MintCandy Network - отдельная сеть `mcnd`

Майнет `mintcandy_9905401-1` (EVM `9905401`) использует **MintCandy (MSP)**, 18 знаков и базовый denom `msp`.
В отдельной сборке тестнета используются `tmsp` и тикер `tMSP`.
[Название, metadata и техническая совместимость](docs/ru/NATIVE_TOKEN_IDENTITY.md).

[Русская инструкция: установка, зависимости, сервер, синхронизация, пиры и хранение истории](docs/ru/README.md).

Этот вложенный Go-модуль собирает приложение новой сети с собственными
genesis и chain IDs. Версионный пакет и подпись его происхождения проверяйте
по [инструкции software release](docs/ru/SOFTWARE_RELEASE_VERIFICATION.md).
Корневой модуль и `dscd` остаются отдельной legacy/replay-сборкой Decimal.
Тег `experimental_ibc` относится к ней; в обычную сборку `mcnd` входят
IBC/transfer, а нативные функции активирует проверенный профиль сети.

В нативном профиле подключены набор валидаторов по активному MSP-обеспечению,
hold и изолированные NFT, ручной/автоматический complete, конвертация DRC20
с `minOut`, порционные штрафы, голосование и начисление комиссий без эмиссии.
Награды начисляются в BeginBlock H по комиссиям H-1 и позициям на конец H-1.
Защищаются уже начисленные права, а не отдельные снимки каждой транзакции.
После интервала 120 блоков открывается автоматический проход выплат:
до 64 получателей/4 млн SDK gas, до 2 млн на попытку. Он может занимать
несколько интервалов. По умолчанию допускается 1000 нативных записей
делегирования на валидатора; это не измерение TPS или минимального сервера.
[Правила массового делегирования и наград](docs/ru/MASS_DELEGATION_AND_REWARDS.md).

## Границы текущего профиля

| Область | Реализованная граница |
| --- | --- |
| EVM-вызовы | Нативный финансовый профиль принимает разрешённые системные маршруты и утверждённые NFT/token-операции. Это не permissionless-развёртывание произвольных dApp. |
| Перевод нативной монеты | SDK bank `MsgSend` допускается. Обычный Ethereum value CALL на EOA в этом профиле отклоняется; нельзя обещать стандартный перевод native через любую кнопку MetaMask. |
| Ключи и кошельки | EVM-ключ и адрес пользователя могут использоваться в новой сети, но balances, история и chain IDs отдельные. `eth_sendRawTransaction` применим к поддержанным маршрутам, а не ко всем возможным EVM-вызовам. |
| Создание токенов | Через TokenCenter пользователи создают безрезервные токены (`createTokenReserveless`) и резервные DRC20 (`createToken`). Для оплаты газа резервным токеном нужны доступная продажа через канонический резерв и подпись владельца на точный расход. [Комиссии и единицы возврата](docs/ru/TOKEN_AND_FEE_MODEL.md). |
| Управление | На предложение до 256 голосующих адресов и 128 депозитных адресов. Предел 1000 делегаций не отменяет этих ограничений. |
| Decimal bridge | Внутренний прототип; обратный release и вложенный mint bridge-controller не являются готовыми разрешёнными пользовательскими маршрутами. |

DEX, инвестиционный vesting, собственный stablecoin, DePIN и MEV-сервис
не входят в первый согласованный выпуск. Для подключения к майнету используйте
подписанный genesis и опубликованный bootstrap-пир. [Параметры запуска](docs/ru/LAUNCH_APPROVAL.md) ·
[Установка 1.2.3 и порядок перехода](docs/ru/INSTALL_1_2_3_RU.md).

## Отдельные подтверждённые проверки

- `2a5f4ffdb967adc85fe92aaa9162d6261763f14e`:
  [module](https://github.com/maxwell2010/go-smart-node/actions/runs/44470608682),
  [token-features](https://github.com/maxwell2010/go-smart-node/actions/runs/44470617638)
  и [подписанный тестовый пакет](https://github.com/maxwell2010/go-smart-node/actions/runs/44470745629) завершились успешно.
- `be7c179d4910cb5467a33c366afe00c1dbc5b754`:
  [массовое делегирование 10/100/1000](https://github.com/maxwell2010/go-smart-node/actions/runs/44467813041) завершилось успешно.

Разные ревизии и scope не образуют общий release PASS. Финальный
process/Hermes-путь прошёл [повторный прогон](https://github.com/maxwell2010/go-smart-node/actions/runs/44472448125).
[Состояние релиза](../docs/RELEASE_STATUS.md),
[проверка пакета и установка без Go-сборки](docs/ru/OPERATOR_LAUNCH.md),
[синхронизация и хранение](docs/ru/SYNCHRONIZATION_AND_STORAGE.md),
[финансовые ограничения](docs/ru/VERIFICATION_LIMITS.md).

Работающие ноды не используются для испытаний. При ограниченном локальном
диске тяжёлые сборки выполняйте GitHub CI; для установки готового Linux-пакета
не нужны Go и GCC. Команды ниже предназначены для отдельного development-стенда
с denom `del`. Параметры майнета и установка проверенного бинарника описаны в
[руководстве оператора](docs/ru/OPERATOR_LAUNCH.md#подготовка-для-майнета-msp).

## Стек и проверки исходников

The current candidate retains the selected [2025.1 release family](https://docs.cosmos.network/sdk/latest/release-family): SDK `0.53.8`, CometBFT `0.38.26`, Cosmos EVM `0.6.3`, and IBC Go `10.7.0`. Its effective Ethereum implementation is declared owned source based on `github.com/cosmos/go-ethereum v1.16.2-cosmos-1`, with the upstream coordinate fix and explicit Pion v3 NAT adaptation. See [source backport](docs/security/owned-geth-coordinate-backport.md) and [transport migration](docs/security/maintained-pion-transport.md). `family-maintained53.json` preserves source-specific historical checks and remaining gates.

`family-check` constructs the owned application in an empty in-memory database, checks the EVM, IBC and transfer wiring, and exits. It opens no listeners or external connections. This structural check alone does not validate native financial operations, a daemon or a relayer.

Run ordinary compatibility checks from this directory with the pinned Go `1.26.11` toolchain:

```sh
GOTOOLCHAIN=local go mod download
GOTOOLCHAIN=local go mod verify
python3 scripts/verify_owned_evm.py
python3 scripts/verify_owned_geth.py
python3 scripts/verify_owned_sdk.py --compiled
python3 scripts/verify_pion_transport.py
GOTOOLCHAIN=local go test -mod=readonly -p 1 -count=1 ./identity ./decimal/... ./app ./internal/family ./internal/ibcharness ./internal/ibcreference/...
GOTOOLCHAIN=local go test -mod=readonly -p 1 -count=1 -tags=test ./app -run '^TestOwnApplicationICS20TwoChains$'
GOTOOLCHAIN=local go run -mod=readonly ./cmd/family-check
```

The application tests exercise a signed EVM transfer, exact charged fees, signing-domain rejection and bounded financial rollback. The `test` tag enables independent in-process EVM configurations solely for the two-chain ICS20 harness. Installable daemons run in separate processes. Build the mainnet and testnet binaries with `mintcandy_mainnet` and `mintcandy_testnet`, respectively. Untagged builds keep the historical `del` denomination.

Generic SDK staking does not establish Decimal feature parity. Selected native validator, delegation, fee-reward, coin and NFT routes are wired under the authenticated profile and have dedicated state/EVM tests. Signed `COINBASE`, daemon and Hermes checks prove their recorded revisions. See the release evidence for integrated process, migration, rollback and package checks; full Decimal parity is not claimed.

The older `family-manifest.json` preserves the SDK `0.54` / EVM `0.7` bootstrap snapshot. Its validation and advisory findings describe that historical graph. The SDK `0.55` source adapter is retained on a separate development branch; it is not this maintained-family candidate.

## Build and run a disposable node

The `mcnd` command uses stateless codecs and genesis defaults, then constructs one application when starting. It validates the actual home's genesis signing domains before configuration writes and application creation, including restarts. Its separate default home is `~/.mcnd`; listeners default to loopback and peer discovery is disabled.

Build the disposable compatibility lab with Go `1.26.11`, GCC and CGO enabled. Use the untagged build:

```sh
go mod download
go mod verify
python3 scripts/verify_owned_evm.py
python3 scripts/verify_owned_geth.py
python3 scripts/verify_owned_sdk.py --compiled
python3 scripts/verify_pion_transport.py
MCND_REVISION=$(git rev-parse HEAD)
MCND="$PWD/build/mcnd-$MCND_REVISION"
env -u GOFLAGS -u GOEXPERIMENT -u CGO_CFLAGS -u CGO_CPPFLAGS -u CGO_CXXFLAGS -u CGO_FFLAGS -u CGO_LDFLAGS GOENV=off GOWORK=off GOTOOLCHAIN=local python3 tools/install_mcnd.py --source .. --revision "$MCND_REVISION" --destination "$MCND"
```

The installation helper requires a clean pinned checkout and default build environment; see the Russian operator guide for source/provenance limits. No production pin is published. Isolated ERC1155 collateral positions now have authenticated Gateway/native projection, signed delegation/complete, penalty and redemption routes. Their final combined release gates still need to pass; previous shared-pool tests do not certify the isolated model.

The following is an ordinary SDK-staking compatibility lab, not a native financial-profile genesis or public-network bootstrap. It creates test funds and a validator in a separate new home. For native operators use the Russian launch guide and its attested contract/genesis procedure. The example IDs are disposable lab values. The `test` keyring backend is used only for these newly generated lab keys. `keys add` defaults to `eth_secp256k1`. Operator and governance-owner templates use `eth_secp256k1` with coin type 60, so their native `d0` address and Ethereum EOA share the same 20 bytes. IBC relayers use a separate native Cosmos `secp256k1` fixture; its RIPEMD address is not an Ethereum operator address.

```sh
set -eu
MCND="$PWD/build/mcnd-$(git rev-parse HEAD)"
NODE_HOME="$HOME/.mcnd-lab-990001"
test ! -e "$NODE_HOME" && test ! -L "$NODE_HOME"
"$MCND" init local --home "$NODE_HOME" --chain-id mintcandy_990001-1 --evm.evm-chain-id 990001
"$MCND" keys add operator --home "$NODE_HOME" --key-type eth_secp256k1 --coin-type 60 --keyring-backend test
ADDRESS=$("$MCND" keys show operator --home "$NODE_HOME" --keyring-backend test -a)
"$MCND" genesis add-genesis-account "$ADDRESS" 1000000000000000000000000del --home "$NODE_HOME"
"$MCND" genesis reconcile-native-allocation --home "$NODE_HOME"
"$MCND" genesis gentx operator 1000000000000000000del --home "$NODE_HOME" --chain-id mintcandy_990001-1 --keyring-backend test
"$MCND" genesis collect-gentxs --home "$NODE_HOME"
"$MCND" genesis validate --home "$NODE_HOME"
"$MCND" start --home "$NODE_HOME" --chain-id mintcandy_990001-1 --evm.evm-chain-id 990001
```

The bounded Linux lifecycle gate in `tools/mcnd_process_smoke.py` requires a private network namespace. It creates its own new home, exercises initialization, signed gentx, real blocks, restart and EVM chain-ID queries, then checks that a mismatched restart cannot modify that home. It never uses existing node homes or external RPC endpoints.

## Native asset module admission

The owned application mounts native `coin` and `nft` stores and their read-only
protobuf/gRPC/AutoCLI queries. Direct native coin/NFT Cosmos financial message
routes remain disabled; supported signed Gateway/token/NFT routes project
their attested EVM execution into native keepers under the enabled profile.
The native coin `Params.BaseVolume` declares the genesis allocation for the compiled denomination.
Native balances and current monetary supply remain in the SDK bank ledger. Custom coin
queries currently return validated stored metadata, not an authoritative live
EVM reserve quote. The separate bounded `coin/evmview` is required for monetary
valuation once an authenticated deployment binding is installed. There is no
silent fallback from a failed monetary view to stale metadata.

After all genesis account allocations, explicitly run
`mcnd genesis reconcile-native-allocation --home ...` before validation/start.
This fresh-home builder sums actual genesis bank balances for the compiled denomination and writes the
same amount into declared native allocation, with an exclusive backup. Startup
rejects mismatches rather than inventing or minting supply. Changing genesis
accounts again requires rebuilding/reviewing that declaration and its backup.
The builder creates no keys, transactions, App or EVM configuration singleton.

Without an authenticated profile, nonempty custom-coin/NFT genesis is rejected.
The enabled profile admits bounded, explicitly attested contract/bootstrap
records after backing and ownership reconciliation; `checks` remain empty.
Keeper-only nonempty genesis does not prove application admission. Active
financial-profile genesis export/reimport remains fail-closed: structural
records alone do not preserve pending rewards, escrow and financial queues.
Use coordinated DB backups or protocol snapshots for recovery. Nonfinancial
subset exports must include both bank and coin if either is included.

Query list pagination is key-based, limit up to50, with offset/count-total
rejected to avoid unbounded scans. Complete NFT responses cap nested records
at10000 and encoded native content at2MiB; oversized requests fail explicitly.
Collections are shallow; Collection and CollectionsByCreator include token
metadata without sub-tokens; Token resolves full active sub-tokens. Inherited
reserve is resolved in query responses while explicit zero reserve remains zero.
These response limits do not truncate genesis exports. Published historical
compatibility-candidate one-liners predate this native module genesis schema;
use the matching source/version instructions, not mixed CLI commands.
