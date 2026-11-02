# Установка для изолированных испытаний

Стабильный релиз пока не опубликован. Это инструкция для разработки и проверки, а не замены работающей mainnet-ноды. Текущая воспроизводимая база CI: Ubuntu 22.04 amd64, GCC, CGO, Go1.24.4. Обновление устаревших зависимостей и инструментов остаётся обязательным условием production-релиза.

```sh
git clone --branch integration/reliability-ibc https://github.com/maxwell2010/go-smart-node.git mintcandy-node
cd mintcandy-node
mkdir -p build
CGO_ENABLED=1 go build -mod=readonly -trimpath -o build/dscd-replay ./cmd/dscd
CGO_ENABLED=1 go build -mod=readonly -trimpath -tags experimental_ibc -o build/dscd-ibc-lab ./cmd/dscd
./build/dscd-replay version --long
./build/dscd-ibc-lab version --long
```

Обычная сборка не подключает transfer module. Экспериментальная IBC-сборка принимает только decimal_900001-1 и decimal_900002-1; запуск с production genesis запрещён. Новые reliability-правила включаются в этих изолированных сетях с высоты1. Для mainnet высоты не назначены. Исправление журнала обновлений действует независимо от этой таблицы и требует исторической проверки.

Проверки:

```sh
CGO_ENABLED=1 go test -mod=readonly -count=1 ./app ./app/ante ./integrationconfig ./x/validator/keeper ./x/upgrade ./cmd/config ./integration/ibclab
CGO_ENABLED=1 go test -mod=readonly -tags experimental_ibc -count=1 -run '^TestExperimental' ./app ./app/ante ./integrationconfig
```

Linux CI формирует два отдельных кандидата с BUILD.json и SHA256SUMS. Сверяйте flavor и исходный коммит; после скачивания артефакта выполните sha256sum -c SHA256SUMS. Две одинаковые сборки на одном runner не заменяют независимую проверку воспроизводимости.

Используйте новый отдельный home, порты и временные тестовые ключи. Не подключайте рабочую БД, keyring, signing state или ключ валидатора. Не меняйте systemd и не перезапускайте работающие сервисы. Экспорт fixtures создаёт приватные тестовые ключи: их нельзя включать в публичный архив. Для двух процессов и внешнего relayer ещё нужен проверенный операторский сценарий.

Replay использует только неизменяемые snapshot-файлы или согласованную резервную копию, доверенные заголовки и соответствующую историю блоков. Не копируйте отдельные файлы работающей LevelDB. Не подключайте replay-ноду к production peers.

Наследуемый автообновлятор может заменить форк upstream-бинарником по будущему плану. До развёртывания нужно определить политику pinning/supervisor. Нельзя сохранять форк ценой отключения необходимых исторических миграций.

Для отката нужны проверенный предыдущий бинарник и совместимая согласованная копия данных. Запуск старого бинарника на уже мигрировавшей БД не является безопасным откатом. Скрипты сборки не устанавливают службы, не мигрируют БД и не выполняют rollback.
