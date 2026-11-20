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
