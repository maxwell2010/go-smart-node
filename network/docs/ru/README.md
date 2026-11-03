# Установка и эксплуатация отдельной сети MintCandy

Статус: DEVELOPMENT. Полноценный релиз пока не опубликован. Здесь описаны
`network/`, бинарник `mcnd` и новая сеть с отдельным genesis. Корневой модуль
и `dscd` относятся к отдельной legacy-сборке/replay. Нельзя заменять
действующую Decimal mainnet-ноду этим бинарником или переносить её базу.
[Оставшиеся условия выпуска](../../../docs/RELEASE_STATUS.md).

## Сервер и зависимости

Проверенная сборочная среда: Linux amd64, Go 1.26.10, GCC, CGO_ENABLED=1.
Операторским скриптам нужен Python >=3.11, Git, curl и CA certificates.
Команды ниже рассчитаны на Ubuntu 24.04 LTS.

Production-нагрузка завершённой сети ещё не измерена, поэтому подтверждённого
минимального оборудования нет. Стартовый бюджет development-стенда:
4 современных vCPU, 16 GiB RAM, 200 GiB свободного NVMe SSD, стабильное
подключение 100 Mbit/s. Это ориентир, а не гарантия скорости или места.
Архивному RPC и сборочной машине нужна отдельная оценка. До production
обязательны замеры CPU/RAM/IOPS, роста GiB/сутки, block latency и catch-up
на целевой нагрузке. Оставляйте запас диска и настройте уведомления.

```sh
set -eu
sudo apt-get update
sudo apt-get install -y build-essential git curl ca-certificates python3
TASK_TMP=$(mktemp -d)
curl -fL --proto '=https' --tlsv1.2 https://go.dev/dl/go1.26.10.linux-amd64.tar.gz -o "$TASK_TMP/go.tar.gz"
echo 'ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5  '"$TASK_TMP/go.tar.gz" | sha256sum -c -
mkdir -p "$HOME/.local/opt/go1.26.10"
tar -xzf "$TASK_TMP/go.tar.gz" -C "$HOME/.local/opt/go1.26.10" --strip-components=1
export PATH="$HOME/.local/opt/go1.26.10/bin:$PATH"
go version
```

Не распаковывайте архив при несовпадении SHA256. Используйте новый каталог,
не заменяйте системную Go без проверки зависимых служб.

## Однострочная сборка опубликованной development-ревизии

После установки зависимостей команда ниже получает точный commit, проверяет
оба owned-source манифеста и собирает бинарник. Она не запускает ноду,
не создаёт ключи и не меняет systemd. Это фиксированный исторический
compatibility-кандидат без завершаемой нативной интеграции.

```sh
TASK_SRC=$(mktemp -d) && git -C "$TASK_SRC" init -q && git -C "$TASK_SRC" fetch --depth 1 https://github.com/maxwell2010/go-smart-node.git 245f13bf2baf04c617baefe1b87ee7e37c68383d && git -C "$TASK_SRC" checkout --detach FETCH_HEAD && test "$(git -C "$TASK_SRC" rev-parse HEAD)" = 245f13bf2baf04c617baefe1b87ee7e37c68383d && (cd "$TASK_SRC/network" && python3 scripts/verify_owned_evm.py && python3 scripts/verify_owned_stun.py && GOTOOLCHAIN=local go mod download && GOTOOLCHAIN=local go mod verify && test "$(go version | cut -d ' ' -f3)" = go1.26.10 && mkdir build && CGO_ENABLED=1 CC=gcc GOTOOLCHAIN=local go build -mod=readonly -p 1 -trimpath -ldflags '-X main.buildCommit=245f13bf2baf04c617baefe1b87ee7e37c68383d' -o build/mcnd ./cmd/mcnd && sha256sum build/mcnd && printf 'Бинарник: %s/network/build/mcnd\n' "$TASK_SRC")
```

Стабильный архив, подписанный genesis и каталог пиров новой сети пока не
опубликованы. Не подставляйте случайные URL. После release-gates команда
должна быть обновлена на утверждённую ревизию и проверенные артефакты.
Из проверенного checkout с операторскими скриптами также доступен сборщик:

```sh
python3 network/tools/install_mcnd.py --source "$PWD" --revision "$(git rev-parse HEAD)" --destination "$HOME/.local/bin/mcnd-development-$(git rev-parse --short HEAD)"
```

Сборщик требует чистую tracked-ревизию, точную Go и Linux amd64, отказывается
перезаписывать существующий бинарник. Обновления устанавливайте под новым
именем, сохраняйте предыдущую версию. Откат после миграции состояния требует
отдельного проверенного плана, одной замены бинарника может быть недостаточно.

## Genesis, ключи и подключение

Оператор публичной сети должен опубликовать Cosmos/EVM chain IDs,
genesis+SHA256, ревизию бинарника, bootstrap-пиры и RPC доверия. Пока доступны
изолированные лабораторные сети. Пример одиночного стенда:
[network/README](../../README.md). Его тестовые ключи не предназначены для
сети с ценностью.

Для присоединения к сети локальный шаблон genesis заменяют проверенным
официальным genesis этой сети. Собственные балансы/gentx в него не добавляют.
Указывайте отдельный `--home`; signing domains реального genesis проверяются
при старте и рестарте. Не запускайте один consensus-ключ на двух валидаторах.
Резервные копии ключей храните отдельно от экспериментальных стендов.

## Пиры, сиды и порты

Peer - нода для обмена блоками, транзакциями и consensus-сообщениями.
Persistent peer - соединение, которое нода старается поддерживать.
Seed - начальный источник адресов, обычно не постоянный поставщик блоков.
PEX обменивается адресами; по умолчанию здесь выключен. Без пиров одиночная
нода не синхронизирует существующую сеть.

Формат: `NODE_ID@HOST:26656`. NODE_ID своей ноды:
`mcnd comet show-node-id --home /path/to/home`. Пиры берутся у операторов
именно той сети и genesis. Пиры Decimal mainnet/testnet не подходят.

В `config/config.toml`, `[p2p]`:

```toml
persistent_peers = ""
seeds = ""
pex = false
```

Вставьте опубликованные проверенные адреса. Для фиксированных persistent
peers PEX не обязателен; seeds требуют согласованного включения PEX.
По умолчанию слушатели `mcnd` привязаны к loopback. Для публичного P2P
настройте интерфейс и firewall; за NAT указывайте `external_address` только
при рабочем пробросе порта. Не рекламируйте приватные адреса.

Обычно TCP26656 - P2P, 26657 - Comet RPC, 9090 - gRPC, 8545/8546 - JSON-RPC.
Сверяйте фактический конфиг и `mcnd start --help`. Открывайте только нужное.
Валидатору не обязательно публиковать RPC. Внешний RPC лучше отделить,
поставить TLS-прокси, ограничения запросов и доступа. Unsafe RPC не включайте.

## Синхронизация

Block sync загружает блоки от пиров и исполняет их. Для начала от genesis
нужен источник ранней истории; pruned-нода не заменяет архивного провайдера.

```sh
mcnd status --node tcp://127.0.0.1:26657
curl -fsS http://127.0.0.1:26657/net_info
```

Проверяйте высоту, время последнего блока, соединения, `catching_up`.
`catching_up=false` без роста высоты ещё не доказывает актуальность.
Сравнивайте высоты и hash с независимыми источниками одной сети.

State sync импортирует snapshot приложения и проверяет его light client.
Нужны минимум два доверенных RPC, независимая проверка `trust_height` и
`trust_hash`, согласованный `trust_period` и совместимый snapshot-провайдер.
В `[statesync]` config.toml используются `enable`, `rpc_servers`,
`trust_height`, `trust_hash`, `trust_period`. Здесь нет выдуманных endpoints
или доверенных hash. State sync начинайте на новом home; изменение data
работающего валидатора требует отдельной процедуры. Сторонняя DB-копия
и протокольный state sync имеют разные гарантии и требуют проверки
происхождения, версии, высоты/AppHash и безопасности ключей.

## История и диск

Раздельно растут версии состояния, Comet blockstore, ABCI responses,
индексы транзакций/EVM, snapshots, WAL, логи и живое состояние.
Одного `pruning` недостаточно. На реальном `mcnd init` подтверждено:
`min-retain-blocks=0` отключает Comet block pruning, Comet indexer=`kv`,
ABCI responses сохраняются. EVM `enable-indexer=false`, snapshots выключены.

Проверить development-профиль нового остановленного home:

```sh
python3 network/tools/configure_history.py --home /path/to/NEW-home
python3 network/tools/configure_history.py --home /path/to/NEW-home --apply --role rpc-ibc
```

Первый вызов не пишет файлы. Второй сохраняет backups. Скрипт отказывается
изменять home с существующей DB/WAL или перезаписывать backups. Для
существующих нод нужен отдельный план обслуживания.

Профиль `rpc-ibc` проверен на отдельном реальном процессе default-build
ревизии `e765d8ad45e5c772d48d3765e4f1dbd6a8fad176` в приватном сетевом
namespace: старт до высоты3, подписанный native-перевод с точным списанием
комиссии и перезапуск до высоты5 прошли. Неправильный signing domain
отклонён без изменения home. SHA256 бинарника:
`23ca343f0c83ec652739027e70cd21601e073214bab8f0c20ee3ab480e2920f8`.
Это проверка совместимости настроек со стартом, не миллион блоков pruning,
не доказательство уменьшения диска и не production-нагрузочный тест.

В корне `app.toml` профиль задаёт:

```toml
pruning = "custom"
pruning-keep-recent = "10000"
pruning-interval = "100"
min-retain-blocks = 1000000
```

В `[state-sync]`: `snapshot-interval=0`, `snapshot-keep-recent=2`.
Нода не становится snapshot-провайдером. Для его включения нужны замеры
размера/IO и дополнительное место, ограниченное число сохраняемых snapshots.

Это параметры стенда, не утверждённая публичная политика. 10000 - блоки,
не дни. Старые historical eth_call/proof-запросы станут недоступны. Для IBC
окно необходимо согласовать с timeout-политикой, задержкой relayer,
upgrade/recovery и архивными провайдерами. Не ставьте `pruning=everything`
на единственном RPC для IBC.

`min-retain-blocks` - минимум сохраняемых блоков, не верхний предел.
SDK53 учитывает также evidence.max_age_num_blocks и snapshot retention:
реально может сохраняться больше. Увеличение retention не восстанавливает
удалённую историю. LevelDB может уменьшаться позже из-за compaction.

Роль `rpc-ibc` сохраняет `[tx_index] indexer="kv"` и `[storage]
`discard_abci_responses=false` для поиска событий и block_results.
Индекс может расти независимо от pruning blockstore. Роль `consensus`
задаёт `indexer="null"`, `discard_abci_responses=true` ценой исторических
запросов. Такую ноду нельзя обещать как полноценный Explorer/Hermes backend;
нужны отдельные RPC/архивные службы.

Жёсткого безопасного лимита N GiB эти настройки не дают: живое состояние
и активные IBC commitments могут расти. Не удаляйте отдельные DB/SST/WAL
файлы. Quota не заменяет планирование: заполненный диск остановит ноду.

## Служба и журналы

Запускайте от непривилегированного пользователя. systemd Unit должен иметь
точные `User`, `WorkingDirectory` и
`ExecStart=/path/to/mcnd start --home /path/to/home`. До запуска проверьте
genesis SHA, domains, peers, права и backups. Установщик намеренно не
создаёт сервис и не перезапускает существующие службы.

`SystemMaxUse`/`SystemKeepFree` ограничивают journald в целом, не один mcnd;
учитывайте другие сервисы. Файловым логам нужна корректная ротация и
переоткрытие; copytruncate может терять записи. Не пишите debug/trace в
неограниченный файл. Контролируйте `du`, свободное место и рост каталогов.

Production-установщик и рекомендуемые параметры будут опубликованы после
закрытия native/IBC/security/upgrade release-gates.
