# Установка и эксплуатация отдельной сети MintCandy

Статус: DEVELOPMENT. Полноценный релиз пока не опубликован. Здесь описаны
`network/`, бинарник `mcnd` и новая сеть с отдельным genesis. Корневой модуль
и `dscd` относятся к отдельной legacy-сборке/replay. Нельзя заменять
действующую Decimal mainnet-ноду этим бинарником или переносить её базу.
[Оставшиеся условия выпуска](../../../docs/RELEASE_STATUS.md).
[Финансовые проверки и ограничения](VERIFICATION_LIMITS.md),
[газ нативной обработки](NATIVE_WORK_GAS.md),
[проверка зависимостей отдельной сборки](../security/mcnd-sdkarmor-264303b-20261103/README.md).
[Подробно о синхронизации и хранении](SYNCHRONIZATION_AND_STORAGE.md).
[Удалённая проверка индексов и следующие process gates](REMOTE_CONSENSUS_VERIFICATION_20261103.md).
[Выбранная модель изолированных NFT-позиций](ISOLATED_1155_MODEL.md).

## Сервер и зависимости

Проверенная сборочная среда: Linux amd64, Go 1.26.10, GCC, CGO_ENABLED=1.
Операторским скриптам нужен Python >=3.11, Git, curl и CA certificates.
Команды ниже рассчитаны на Ubuntu 24.04 LTS.

Production-нагрузка завершённой сети ещё не измерена, поэтому подтверждённого
минимального оборудования нет. Стартовый бюджет development-стенда:
4 современных vCPU, 16 GiB RAM, 200 GiB свободного NVMe SSD, стабильное
подключение 100 Mbit/s. Это предварительный бюджет планирования, а не
измеренный минимум и не основание для покупки production-сервера.
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
GO_INSTALL_DIR="$HOME/.local/opt/go1.26.10"
mkdir -p "$HOME/.local/opt"
test ! -e "$GO_INSTALL_DIR"
mkdir "$GO_INSTALL_DIR"
tar -xzf "$TASK_TMP/go.tar.gz" -C "$GO_INSTALL_DIR" --strip-components=1
export PATH="$GO_INSTALL_DIR/bin:$PATH"
go version
```

Не распаковывайте архив при несовпадении SHA256. Используйте новый каталог,
не заменяйте системную Go без проверки зависимых служб.

## Быстрая сборка точной development-ревизии

Утверждённого production pin нет. Проверенный development snapshot:
`8cf619da1c77ee5efffc80a678272010fd2263dd`. Он прошёл четыре CI, включая
[сборку и упаковку](https://github.com/maxwell2010/go-smart-node/actions/runs/41608914841)
и [candidate-проверки](https://github.com/maxwell2010/go-smart-node/actions/runs/41608914982).
В него входят SDK/source-inventory/binary-metadata checks, guard нового
retention-home и исправления version/completion/snapshots CLI.
[Проверка готового артефакта](evidence/published-cli-8cf619d-20261103/README.md)
подтверждает конкретные обычные CLI-регрессии, не финансовую активацию.

Это не stable и не полноценный финансовый релиз. Более поздние исправления
не приписываются этому pin: следующей ревизии нужны собственные CI/gates.
Не заменяйте полный commit на `master`, `HEAD`, tag или случайный SHA.
После установки зависимостей выполните из любого каталога:

```sh
MCND_REVISION=8cf619da1c77ee5efffc80a678272010fd2263dd && export MCND_REVISION && python3 -c 'import os,re,sys; sys.exit(0 if re.fullmatch(r"[0-9a-f]{40}", os.environ.get("MCND_REVISION", "")) else "Нужен утверждённый полный MCND_REVISION")' && TASK_SRC=$(mktemp -d) && git -C "$TASK_SRC" init -q && git -C "$TASK_SRC" fetch --depth 1 https://github.com/maxwell2010/go-smart-node.git "$MCND_REVISION" && git -C "$TASK_SRC" checkout --detach FETCH_HEAD && test "$(git -C "$TASK_SRC" rev-parse HEAD)" = "$MCND_REVISION" && env -u GOFLAGS -u GOEXPERIMENT -u CGO_CFLAGS -u CGO_CPPFLAGS -u CGO_CXXFLAGS -u CGO_FFLAGS -u CGO_LDFLAGS GOENV=off GOWORK=off GOTOOLCHAIN=local python3 "$TASK_SRC/network/tools/install_mcnd.py" --source "$TASK_SRC" --revision "$MCND_REVISION" --destination "$HOME/.local/bin/mcnd-development-$MCND_REVISION"
```

SHA идентифицирует исходники, но сам по себе не удостоверяет издателя.
Проверяйте канал публикации ревизии. Стабильный архив, подписанный genesis
и каталог пиров новой сети пока не опубликованы. Установщик создаёт только
новый бинарник; ноду, ключи, genesis, home и systemd он не создаёт.
CI-архивы имеют ограниченный срок хранения и не заменяют постоянную
подписанную релизную публикацию. Этот однострочник собирает source pin,
а не скачивает будущий stable asset.

В уже проверенном чистом checkout можно использовать короткий вариант
из корня репозитория; `MCND_REVISION` должен совпадать с checkout:

```sh
env -u GOFLAGS -u GOEXPERIMENT -u CGO_CFLAGS -u CGO_CPPFLAGS -u CGO_CXXFLAGS -u CGO_FFLAGS -u CGO_LDFLAGS GOENV=off GOWORK=off GOTOOLCHAIN=local python3 network/tools/install_mcnd.py --source "$PWD" --revision "${MCND_REVISION:?Укажите утверждённый полный commit}" --destination "$HOME/.local/bin/mcnd-development-$MCND_REVISION"
```

Политика сборки: точная Go 1.26.10, Linux amd64/v1, CGO=1/GCC, default
build tags; пользовательские GOFLAGS/GOEXPERIMENT/CGO flags не поддерживаются.
GOENV и workspace отключаются для этой команды. Это не меняет глобальное
окружение пользователя. Проверяются три owned-source дерева EVM/geth/SDK,
канонический Pion v3, h1 зависимостей и фактические tracked Go/embed inputs
до и после сборки. Игнорируемый или untracked production input недопустим.
Это проверка состава исходников; она не является подписью издателя,
полной аттестацией toolchain, SBOM или доказательством финансовой корректности.
Go metadata этой trimpath-сборки не содержит `-ldflags`: VERIFIED_BUILD
проверяет записанные поля и module identities, но не доказывает полный
набор linker inputs или побайтную воспроизводимость.

Упаковщик дополнительно сравнивает метаданные самого ELF-бинарника с этой
политикой: версию Go, основной пакет, архитектуру, CGO, `trimpath`, отсутствие
дополнительных флагов и чистую VCS-ревизию. Версии, h1 и замены всех модулей
должны точно совпасть с фактическим default package graph исходников. Новые
архивы сохраняют результат в `VERIFIED_BUILD.json` вместе с `BUILD_INFO.txt`
и `SOURCE_INPUTS.json`. Для упаковки нужен checkout без untracked-файлов,
которые помечают Go build как `vcs.modified=true`, даже если эти файлы не
являются Go/embed inputs. Старый snapshot `88ed536` ещё не содержит этого
дополнительного отчёта; его фактические метаданные проверены отдельно.

Обновления ставятся под новым именем: сборщик отказывается перезаписывать
существующий бинарник. Сохраняйте предыдущую версию. Откат после миграции
состояния требует отдельного проверенного плана; замены бинарника может
быть недостаточно. Неиспользуемый временный checkout можно сохранить для
проверки происхождения - команда не удаляет другие каталоги.

Историческая ревизия `245f13bf2baf04c617baefe1b87ee7e37c68383d`
сохранена только как provenance прежнего compatibility-стенда. Она содержит
другую dependency/native schema и старый STUN-путь. Не используйте её как
актуальную установку и не смешивайте с современными CLI-инструкциями.

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
изменять home с DB/WAL, неизвестными файлами в data или ненулевым
состоянием consensus-подписания. Допускается только пустой data или
нулевой `priv_validator_state.json` от init. Существующие backup/`.new`
файлы не перезаписываются. Изменения двух конфигов не являются общей
атомарной транзакцией: при ошибке остаются backups/временные файлы для
ручной проверки. Скрипт не останавливает процессы и не блокирует запуск
ноды; оператор должен исключить параллельный старт/редактирование. Для
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
Нода не становится snapshot-провайдером. `snapshot-keep-recent=0`
означал бы сохранять все snapshots, а не отключение. Для включения нужны замеры
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


## Явное объявление эмиссии в текущем development-genesis

Текущая интеграционная ветка добавляет реальные хранилища `coin`/`nft`
и их запросы только для чтения. После добавления всех genesis-счетов выполните
`mcnd genesis reconcile-native-allocation --home /path/to/NEW-home` перед
`genesis validate` и запуском. Команда явно вычисляет сумму DEL в банковских
балансах genesis и записывает её как объявленное начальное распределение
`coin.params.base_volume`. Она не выпускает монеты, не создаёт App и не
запускает ноду. Есть эксклюзивная резервная копия; существующие DB/WAL
и символьные ссылки конфигурации не принимаются. Повторный вызов при той
же сумме не изменяет файл. При последующем изменении распределения сначала
проверьте genesis и его backup, затем соберите согласованное объявление заново.

Сам старт никогда не исправляет несовпадение молча. Поле `base_volume` -
объявление начального распределения, а текущий денежный баланс/предложение
DEL берутся из SDK bank. При экспорте объявление пересчитывается в копии
экспортируемого genesis по экспортированным банковским балансам; сохранённые
параметры работающего состояния не меняются. Частичный экспорт должен
содержать и bank, и coin, если включён хотя бы один из них.

Приложение пока отклоняет непустой genesis пользовательских монет, чеков
и NFT до завершения проверенного развёртывания контрактов и сверки резервов.
Изолированные проверки metadata-хранилищ не являются доказательством
финансового обеспечения. Финансовые native Msg-маршруты пока не включены.
Запрос Coin возвращает проверенную сохранённую metadata; это не обещание
актуального EVM-резерва после перевода в контракт без события. Денежная
оценка требует отдельного bounded EVM view, проверенной привязки контракта
и реального исторического header. При ошибке view нельзя подставлять старые
metadata. Для базового DEL используйте запросы bank; BaseVolume доступен
через `query coin params` как объявление распределения.

Публичные native-списки используют key pagination: limit до50, без offset
и count-total; ответы ограничены2MiB. Полные NFT-ответы ограничены10000
вложенных записей и при превышении явно возвращают ошибку. Эти лимиты не
обрезают экспорт genesis. Унаследованный reserve разрешается в ответе,
явный нулевой reserve сохраняется нулевым. Исторический однострочник выше
фиксирует старый compatibility-кандидат: новые команды применяйте только
с соответствующей текущей сборкой. Финальный релиз обновит единую точную
ревизию установщика после всех проверок.
