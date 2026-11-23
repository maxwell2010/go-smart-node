# Релеер Hermes для MintCandy Network

Hermes передаёт сообщения и доказательства IBC между двумя сетями. Состояние
пакетов, подтверждения и возвраты хранятся в самих сетях. Для работы нужны
открытый канал, доступ к RPC и gRPC обеих цепочек и операционные кошельки с
монетами для оплаты комиссий на каждой стороне.

В плане запуска MintCandy предусмотрено по **100 MSP** на два независимых
кошелька релееров. Для сети-партнёра отдельно согласуйте адреса и бюджет в её
валюте. Публичный маршрут пока ждёт выбора сети-партнёра и публикации endpoint.
[Проект запуска и состав](PUBLIC_LAUNCH_DRAFT.md).

## Установка на Linux x86_64

Скачайте официальный Hermes v1.13.3 и сверьте SHA256 архива и бинарника.
Команды выполняются от отдельного непривилегированного пользователя релеера.
Понадобятся `curl`, `tar`, `sha256sum`, `install` и `jq`.
Выпуск v1.13.3 на x86_64 может выводить `1.13.2+bab3b80`. Точную сборку
определяет SHA256. [Официальный выпуск Hermes](https://github.com/informalsystems/hermes/releases/tag/v1.13.3).

```sh
set -eu
umask 077
work=$(mktemp -d)
curl --fail --silent --show-error --location --retry 3 \
  https://github.com/informalsystems/hermes/releases/download/v1.13.3/hermes-v1.13.3-x86_64-unknown-linux-gnu.tar.gz \
  --output "$work/hermes.tar.gz"
printf '%s  %s\n' 3582136824ac1984c8884b310c63940a1489f091fa45faca38ed80e5dfe7d51b \
  "$work/hermes.tar.gz" | sha256sum --check --strict
tar -xzf "$work/hermes.tar.gz" -C "$work" hermes
printf '%s  %s\n' ed8d57781f57a93e74259c12c8feee661b9633c882307f3a6236e41dbf5ffe35 \
  "$work/hermes" | sha256sum --check --strict
mkdir -p "$HOME/.local/bin" "$HOME/.hermes"
install -m 0755 "$work/hermes" "$HOME/.local/bin/hermes"
"$HOME/.local/bin/hermes" version
```

## Кошельки и конфигурация

1. Подготовьте отдельный ключ Cosmos `secp256k1` для MintCandy с путём
   `m/44'/60'/0'/0/0`. Hermes с `derivation = "cosmos"` должен вывести тот же
   адрес `d0...`, что и `mcnd` для этого ключа. Импортируйте только свой
   выделенный ключ релеера. Для второй цепочки используйте отдельный ключ и её
   путь деривации. В анкете для genesis указывайте только публичный адрес.
2. Получите с обеих сторон опубликованные chain ID, RPC с `/websocket`, gRPC и
   окна хранения блоков, транзакций и IBC-доказательств. Релееру нужен
   `indexer = "kv"` в `[tx_index]` на RPC-ноде. Исторические запросы направляйте на профиль
   `rpc-ibc`. [Настройки хранения](SYNCHRONIZATION_AND_STORAGE.md).
3. После согласования канала узнайте **локальный** `channel-N` на каждом конце.
   Номера концов могут различаться. Порт для ICS-20 v1 - `transfer`.

Начальный фрагмент `~/.hermes/config.toml` приведён ниже. Все значения
`REPLACE_...` нужно заменить. Повторите блок `[[chains]]` для сети-партнёра
и укажите её prefix, denom, RPC, WebSocket, gRPC, ключ, предел газа и доверенное
окно клиента. `gas_price` задаётся в атомах denom. Цена `1000000000del` и
`max_gas = 4000000` взяты со стенда. Перед публичным запуском сверьте цену
газа с утверждённым профилем. `trusting_period = "14days"` подходит для
проверенного профиля MintCandy с выводом стейка 28 дней. Для второй сети
сверьте её период разблокировки, параметры клиента и допустимый `max_block_time`.
[Справочная конфигурация Hermes v1.13.3](https://github.com/informalsystems/hermes/blob/v1.13.3/config.toml).

```toml
[global]
log_level = "info"

[mode.clients]
enabled = true
refresh = true
misbehaviour = true

[mode.connections]
enabled = false

[mode.channels]
enabled = false

[mode.packets]
enabled = true
clear_interval = 100
clear_on_start = true

[[chains]]
id = "mintcandy_9905401-1"
rpc_addr = "https://REPLACE_MINTCANDY_RPC.invalid"
grpc_addr = "https://REPLACE_MINTCANDY_GRPC.invalid"
event_source = { mode = "push", url = "wss://REPLACE_MINTCANDY_RPC.invalid/websocket", batch_delay = "200ms" }
rpc_timeout = "10s"
trusted_node = false
account_prefix = "d0"
key_name = "mintcandy-relayer"
store_prefix = "ibc"
default_gas = 200000
max_gas = 4000000
gas_price = { price = 1000000000, denom = "del" }
gas_multiplier = 1.3
max_msg_num = 10
max_tx_size = 1048576
clock_drift = "5s"
max_block_time = "30s"
trusting_period = "14days"
trust_threshold = "1/3"
address_type = { derivation = "cosmos" }
packet_filter = { policy = "allow", list = [["transfer", "REPLACE_MINTCANDY_CHANNEL"]] }
```

`MINTCANDY_MNEMONIC_FILE` указывает на локальный файл с правами `0600` в
приватном каталоге. Резервная копия ключа хранится отдельно в защищённом
хранилище. После добавления обоих ключей проверьте их адреса и `health-check`.
Для `PARTNER_CHAIN_ID` используйте идентификатор из второй записи `[[chains]]`.

```sh
set -eu
umask 077
hermes="$HOME/.local/bin/hermes"
config="$HOME/.hermes/config.toml"
test -n "$PARTNER_CHAIN_ID"
"$hermes" --config "$config" keys add --chain mintcandy_9905401-1 \
  --key-name mintcandy-relayer --mnemonic-file "$MINTCANDY_MNEMONIC_FILE" \
  --hd-path "m/44'/60'/0'/0/0"
"$hermes" --config "$config" keys list --chain mintcandy_9905401-1
"$hermes" --config "$config" keys list --chain "$PARTNER_CHAIN_ID"
health=$("$hermes" --config "$config" --json health-check)
printf '%s\n' "$health" | jq -es \
  'any(.[]; .status == "success") and all(.[]; .level != "WARN" and .level != "ERROR")'
```

Убедитесь, что в выводе проверены обе сети. Проверьте состояние клиентов и
соединения, баланс кошельков и доступность proof-запросов. Настройки клиента
сверяются с другой стороной до открытия маршрута.

## Сверка открытого канала перед запуском Hermes

Для первого маршрута используйте отдельную конфигурацию Hermes с двумя
сетями. В каждом `packet_filter` укажите только локальную пару `transfer` и
`channel-N`, согласованную с оператором второй сети. Перед запуском фонового
процесса сверяйте уже открытый канал. Передайте в `MINTCANDY_CHANNEL` и
`PARTNER_CHANNEL` идентификаторы соответствующих концов, которые подтвердили
обе стороны.

```sh
python3 -B network/tools/verify_hermes_route.py \
  --hermes "$HOME/.local/bin/hermes" \
  --hermes-sha256 ed8d57781f57a93e74259c12c8feee661b9633c882307f3a6236e41dbf5ffe35 \
  --config "$HOME/.hermes/config.toml" \
  --left mintcandy_9905401-1 "$MINTCANDY_CHANNEL" \
  --right "$PARTNER_CHAIN_ID" "$PARTNER_CHANNEL"
```

Команда сверяет SHA256 Hermes и фильтры в конфигурации. Затем через RPC
читаются два конца канала, обе стороны соединения и статусы клиентов
Tendermint. Результат `IBC_ROUTE_STATE_MATCH` означает, что указанные концы
открыты, ссылаются друг на друга по ICS-20 v1 и оба клиента активны. Команда
выполняет запросы на чтение. Состояние зависит от выбранных RPC и времени
проверки. Для решения о запуске используйте подтверждённые endpoint и
сверьте данные с оператором другой стороны.
[Команды чтения состояния канала Hermes](https://hermes.informal.systems/documentation/commands/queries/channel.html).

На изолированном стенде проверка подтвердила маршрут `mintcandy_990014-1` ↔
`ibc-reference-1` с двумя активными клиентами. Список транзакций в обеих
сетях остался прежним. После проверки Hermes на той же конфигурации с
фильтрами доставил новый пакет и восстановил доставку после перезапуска.
[GitHub CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44699409744).

## Фоновый процесс

Пример `~/.config/systemd/user/mintcandy-hermes.service` запускает Hermes под
тем же пользователем и с той же конфигурацией, что и предыдущие команды.

```ini
[Unit]
Description=MintCandy IBC relayer (Hermes)

[Service]
Type=exec
ExecStart=%h/.local/bin/hermes --config %h/.hermes/config.toml start
Restart=on-failure
RestartSec=5s
UMask=0077
NoNewPrivileges=true

[Install]
WantedBy=default.target
```

После готовности маршрута оператор может включить службу в своей сессии.
Чтобы пользовательская служба запускалась после перезагрузки без входа
пользователя, администратор включает `loginctl enable-linger` для этой учётной
записи. Состояние проверяется по журналу и подтверждениям IBC на обеих сетях.

```sh
systemctl --user daemon-reload
systemctl --user enable --now mintcandy-hermes.service
journalctl --user -u mintcandy-hermes.service -n 50 --no-pager
```

При перезапуске Hermes использует тот же кошелёк, канал и конфигурацию.
`clear_on_start = true` включает подбор ожидающих пакетов при старте.
Контролируйте высоту RPC, последние подтверждённые пакеты, срок действия
клиентов и остаток газа на обеих сторонах. `100 MSP` из плана genesis -
начальный бюджет. Длительность работы зависит от числа сообщений и цен газа.

На изолированном стенде с четырьмя валидаторами и опубликованным `mcnd` 1.1.1
режим `hermes start` автоматически доставил новый пакет и подтвердил его в
исходной цепочке. После остановки релеера новый пакет остался в очереди и был
доставлен после запуска нового процесса. CI сверил номера пакетов, суммы и
комиссии, затем подтвердил одинаковый AppHash блока 58 у четырёх валидаторов.
В этом прогоне `clear_interval` был равен `0`, чтобы отдельно проверить
доставку по событиям и подбор очереди при старте. Вторая сторона была
эталонной IBC-цепочкой. [Прогон GitHub CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44681169004).

## Проверка смены версии при ожидающем пакете

В отдельном прогоне CI проверил аттестации и SHA256 опубликованных архивов
`mcnd` v1.1.4 и v1.1.5. Четыре тестовых валидатора запустились на v1.1.4,
перешли на v1.1.5 и вернулись на v1.1.4. Использовались те же каталоги
данных и файлы состояния подписей. Эталонную цепочку на время смены
версий остановили, поэтому пакет ICS-20 № 3 оставался в очереди.

На v1.1.5 совпали подписи и AppHash блока 40. После возврата на v1.1.4
этот блок сохранился, а все четыре валидатора согласовали AppHash на
высоте 42. Номер и хеш ожидающего пакета, а также сумма в escrow сохранились.
После запуска эталонной цепочки Hermes доставил пакет. На высоте 45
исходная цепочка записала одно подтверждение.

Сценарий подтверждает возврат бинарника для этой пары версий с сохранением
состояния. Миграция модулей и восстановление базы из резервной копии
проверяются отдельно. [Прогон CI и отчёт](https://github.com/maxwell2010/go-smart-node/actions/runs/45131811346).
