# Локальное media-хранилище и восстановление

Компонент `network/media/mcnd_media` предназначен для отдельного media-хоста.
Он хранит публичные изображения и JSON, восстанавливает их из копии и готовит
статический каталог для выдачи через Nginx по CID.

## Действующие ограничения

- Linux, Python 3.12; decoder требует ровно Pillow 13.0.0. Wheel закреплён
  SHA256 в `requirements-linux-cp312.lock`, без source build.
- Avatar токена до 1 MiB, NFT image до 10 MiB, PNG/JPEG/WebP, один кадр,
  размеры не больше 4096x4096. MIME сверяется с декодированным форматом.
- Декодирование выполняется отдельным процессом: 512 MiB address space,
  6 секунд CPU, внешний timeout 12 секунд. Из пикселей создаётся новый PNG,
  исходные EXIF/ICC/комментарии не копируются. Ограничен и результат.
- Store до 64 MiB, до 512 version records и до 16 версий одного asset.
  Эти значения ограничивают данный компонент и требуют sizing для сервиса.
- Имя, описание и строковые attributes ограничены по размеру. Произвольный
  пользовательский `image`, URL, HTML/SVG и дополнительные поля не принимаются.
  Поле image формируется из реально принятых и нормализованных байтов.
- Декодер и хранилище обрабатывают локальные файлы. Проверка CID обращается
  только к настроенным оператором Comet и EVM RPC на `127.0.0.1`, с отключёнными
  proxy и redirect. HTML/script в metadata интерфейс должен выводить как текст.

## Авторизация - обязательная граница

`MediaStore(root, authorizer=...)` требует доверенный callback на каждую публикацию:
`authorizer(asset, actor, "publish")` должен вернуть буквально `True`.
По умолчанию публикация запрещена. Строка owner, переданная пользователем,
не является доказательством владения. HTTP-запрос не должен получать право
передавать callback или значение результата его проверки.

Для уже записанного CID используйте `mcnd_media.commitment`. Разрешение на
публикацию конкретных байтов задаёт обычная подписанная транзакция создания
актива. Для ERC20 проверяется допуск контракта в нативном реестре, для NFT -
запись официальной фабрики, тип коллекции и наличие указанного token ID.
Проверяется общий недавний блок Comet и EVM с совпадающим хешем. Затем
`identity()`, `tokenURI(id)` или `uri(id)` сравнивается с вычисленным URI JSON.
В индекс записывается `owner()` безрезервного DRC20 или `creator()` остальных
активов. Передача NFT держателю сама по себе не разрешает менять его CID.
Повторная публикация того же CID возвращает существующую версию.

### Подготовка и публикация CID

Сначала подготовьте файлы на Linux с проверенной версией decoder. В JSON
укажите `name` и при необходимости `description` и `attributes`. Поле `image`
формируется из нормализованной картинки. Для NFT укажите `erc721` или `erc1155`
вместо `erc20`. Сохраните исходные файлы до завершения публикации.

```sh
export PYTHONPATH="$PWD/network/media"
python3 -B -m mcnd_media.commitment prepare --chain-id "$CHAIN_ID" \
  --kind "$KIND" --metadata "$METADATA" --image "$IMAGE" --mime "$MIME"
```

Укажите выданный `metadata_uri` как `identity` DRC20 или URI конкретного NFT
при обычном создании либо mint. После включения операции получите адрес
контракта и для NFT полный token ID в десятичном виде. Когда RPC видят
транзакцию в проверяемом блоке, передайте те же файлы оператору хранилища.
Для DRC20 используется `TOKEN_ID=0`.

```sh
python3 -B -m mcnd_media.commitment publish --chain-id "$CHAIN_ID" \
  --kind "$KIND" --contract "$CONTRACT" --token-id "$TOKEN_ID" \
  --metadata "$METADATA" --image "$IMAGE" --mime "$MIME" \
  --comet-rpc http://127.0.0.1:26657 --evm-rpc http://127.0.0.1:8545 \
  --store "$MEDIA_STORE"
```

Команда `publish` читает состояние доверенной ноды с проверенным genesis.
Если RPC разнесены по хостам, оператор предоставляет их через фиксированные
локальные туннели. Проверка доверяет выбранным RPC и видит подтверждённое
состояние на общей высоте. Команда не принимает приватные ключи. При подмене
контракта, ID, URI или байтов файла запись отклоняется до изменения истории.
Путь подходит для активов, содержащих прямой `ipfs://<CID metadata.json>`.

## HTTP API на отдельном media-хосте

Установите Python 3.12, Nginx и зависимости с проверкой SHA256. Из корня
репозитория:

```sh
python3.12 -m venv "$MEDIA_VENV"
"$MEDIA_VENV/bin/pip" install --only-binary=:all: --require-hashes \
  -r network/media/requirements-linux-cp312.lock -r network/media/requirements-api.lock
```

Запускайте Gunicorn от пользователя, которому принадлежит каталог хранилища.
Задайте `MEDIA_STORE` как абсолютный путь к этому закрытому каталогу,
`CHAIN_ID` как chain ID проверенного genesis. Адреса ниже предполагают локальные
RPC или настроенные оператором локальные туннели.

```sh
umask 077
export MCND_MEDIA_CHAIN_ID="$CHAIN_ID" MCND_MEDIA_STORE="$MEDIA_STORE"
export MCND_MEDIA_COMET_RPC=http://127.0.0.1:26657 MCND_MEDIA_EVM_RPC=http://127.0.0.1:8545
PYTHONPATH="$PWD/network/media" "$MEDIA_VENV/bin/python" -B -m gunicorn \
  --bind 127.0.0.1:8910 --workers 2 --worker-class sync --timeout 60 \
  --no-control-socket 'mcnd_media.api:from_environment()'
```

### Постоянный запуск через systemd

На выделенном Ubuntu 24.04 media-хосте установите `python3.12-venv` и `nginx`.
Из корня проверенной копии проекта разместите код и зависимости в `/opt/mcnd-media`.

```sh
sudo apt-get update
sudo apt-get install -y python3.12-venv nginx
getent passwd mcnd-media >/dev/null || \
  sudo useradd --system --user-group --no-create-home --shell /usr/sbin/nologin mcnd-media
sudo install -d -m 0755 /opt/mcnd-media/mcnd_media
sudo install -m 0644 network/media/mcnd_media/*.py /opt/mcnd-media/mcnd_media/
sudo python3.12 -m venv /opt/mcnd-media/venv
sudo /opt/mcnd-media/venv/bin/python -m pip install --only-binary=:all: --require-hashes \
  -r network/media/requirements-linux-cp312.lock -r network/media/requirements-api.lock
sudo install -m 0644 network/media/mcnd-media.service /etc/systemd/system/mcnd-media.service
```

Создайте `/etc/mcnd-media.env` под владельцем root с режимом `0600`. Укажите
chain ID проверенной сети и RPC на loopback. Если RPC размещены на другом
сервере, укажите порты локальных туннелей.

```ini
MCND_MEDIA_CHAIN_ID=mintcandy_9905401-1
MCND_MEDIA_COMET_RPC=http://127.0.0.1:26657
MCND_MEDIA_EVM_RPC=http://127.0.0.1:8545
```

```sh
sudo systemd-analyze verify /etc/systemd/system/mcnd-media.service
sudo systemctl daemon-reload
sudo systemctl enable --now mcnd-media.service
sudo systemctl status mcnd-media.service
sudo journalctl -u mcnd-media.service -n 50
```

Служба слушает `127.0.0.1:8910`. systemd создаёт для пользователя `mcnd-media`
закрытый каталог `/var/lib/mcnd-media` с режимом `0700`. SQLite и файлы
остаются там после перезапуска. Перед обновлением сделайте проверенную
резервную копию по инструкции ниже.
[Настройки службы в systemd](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html).

В изолированном Linux CI служба запустилась от отдельного пользователя и
сохранила версию публикации, JSON и PNG после перезапуска. Повтор публикации
вернул прежнюю версию. Тест также сверил права каталога и SQLite.
[Прогон systemd](https://github.com/maxwell2010/go-smart-node/actions/runs/45009462939).

### Подключение Nginx

Подключите [настройки лимитов](../../media/nginx-media-limits.conf) один раз в
блоке `http` и [маршруты API](../../media/nginx-live.conf) в блоке `server`
выделенного HTTPS-домена. В `http` задайте
`ssl_protocols TLSv1.2 TLSv1.3;`. Замените тестовое имя и пути в примере
на согласованный домен, полную цепочку сертификата и закрытый ключ.

```nginx
server {
    listen 443 ssl;
    server_name media.example.com;
    ssl_certificate /etc/nginx/media/fullchain.pem;
    ssl_certificate_key /etc/nginx/media/privkey.pem;
    include /etc/nginx/media/nginx-live.conf;
}
```

Используйте абсолютные пути в `include` и проверьте конфигурацию командой
`nginx -t` до её применения. Nginx отправляет запросы на Gunicorn по loopback.
Данные, SQLite и RPC остаются во внутреннем контуре. Проверьте с отдельного
клиента сертификат домена и выдачу известного CID по HTTPS.
[Настройка HTTPS в Nginx](https://nginx.org/en/docs/http/configuring_https_servers.html).

1. В `POST /v1/prepare` передайте JSON с полями `kind`, `metadata`,
   `image_base64` и `mime`. Ответ содержит `metadata_uri` и `image_uri`.
   Этот запрос только вычисляет CID, файлы в хранилище не появляются.
2. Запишите `metadata_uri` в `identity` DRC20 или URI NFT обычной
   подписанной транзакцией. Дождитесь подтверждения в обоих локальных RPC.
3. В `POST /v1/publish` передайте те же поля и добавьте `contract` и `token_id`.
   ID передаётся десятичной строкой, для DRC20 это `"0"`. API сверяет CID на
   общей подтверждённой высоте, затем сохраняет изображение и JSON. Повтор той
   же публикации возвращает уже созданную версию.
4. Получите файл по `GET /ipfs/<CID>` или проверьте заголовки через `HEAD`.
   Выдаются только отражённые в индексе объекты с совпадающим CID. Публичное
   чтение поддерживает CORS. Загрузки из браузера рассчитаны на тот же origin.

Оба POST-запроса принимают `Content-Type: application/json`. Лимит JSON с
Base64 - 14 MiB. Ограничения исходного файла - 1 MiB для аватара DRC20 и
10 MiB для NFT. Nginx ограничивает частоту до 6 запросов в минуту с IP и до 2
в секунду на origin. В каждом случае разрешён кратковременный всплеск до 4
дополнительных запросов без задержки. На origin допускается до 4 одновременных
загрузок.

На изолированном стенде CI Nginx принял TLS 1.2 и 1.3, клиент отклонил
недоверенный сертификат и неверное имя, а незашифрованный запрос на HTTPS-порт
получил 400. Через HTTPS проверены публикация, GET/HEAD исходных байтов по CID
и ответы 413/429. Подтверждение CID по подписанной EVM-транзакции проверено
отдельно на опубликованном бинарнике v1.1.5.
[HTTPS и systemd CI](https://github.com/maxwell2010/go-smart-node/actions/runs/45150996621) ·
[Подписанная транзакция в изоляции](https://github.com/maxwell2010/go-smart-node/actions/runs/45124389500).

## Формат и целостность

Asset namespace состоит из chain ID, contract, типа и token ID. JSON и изображения
до 2 МиБ включительно сохраняют CIDv1 raw/SHA-256. Изображения большего размера
получают UnixFS CIDv1 с блоками по 256 КиБ. Поле `image` в JSON указывает
на CID нормализованной картинки. Сохранённые ранее raw CID продолжают работать
через HTTP и резервные копии. Стандартный Bitswap передаёт raw-блоки до 2 МиБ.
[Параметры UnixFS](https://specs.ipfs.tech/unixfs/) · [Лимит Kubo](https://docs.ipfs.tech/reference/kubo/cli/#ipfs-block-put).
Доступный через identity/URI pointer на JSON короче 256 байт.

Дедупликация не создаёт второй экземпляр одинакового содержимого. SQLite index
связывает namespace, owner, номер версии, metadata CID и image CID. Публикация
сериализована транзакцией; при отказе квоты или авторизации история не меняется.
Чужие существующие объекты не перезаписываются. `verify()` сверяет CID, байты,
references, namespace, версии, отсутствие лишних объектов и квоты.

Если процесс завершится в момент записи, файлы без записи в индексе не
выдаются по HTTP. Следующая разрешённая публикация или создание резервной
копии под SQLite-блокировкой удаляет оставленные файлы с допустимым именем
CID. Перед фиксацией индекса Linux синхронизирует файлы и каталог объектов.
Если отсутствует индекс или файл, на который он ссылается, операция
останавливается. Восстановите проверенную копию в новом каталоге.

Root должен принадлежать отдельной service account, быть недоступным для записи
другим процессам и не находиться под управлением непроверенного пользователя.
Не использовать общий каталог с произвольными symlink/файлами. Это не защита от
администратора ОС, который одновременно меняет базу и данные процесса.

## Backup и restore

`store.backup(absolute_new_zip)` фиксирует index и точные объекты под writer lock,
возвращает SHA256. ZIP содержит только JSON index и имена objects/CID; SQLite
файл, ключи, токены доступа и wallet secrets туда не копируются.

`restore(archive, absolute_new_root, trusted_sha256, authorizer=...)`:

1. Сверяет внешний доверенный SHA256 до разбора архива.
2. Отвергает duplicate names, traversal, symlinks, compression, encryption,
   лишние файлы, превышения размеров и невалидный индекс.
3. В sibling staging восстанавливает объекты, сверяет CID и metadata bindings.
4. Публикует только в новый каталог; существующий destination не заменяется.

Ожидаемый SHA256 нужно хранить отдельно в доверенном manifest/канале. Хеш,
присланный вместе со случайным архивом, не удостоверяет backup или owner.
Restore не перепроверяет исторические on-chain права: он воспроизводит
доверенную копию. Права на новые публикации снова проверяет adapter.
Копирование store в другой namespace без проверенного backup не поддерживается.

## HTTP-выдача по CID после восстановления

На отдельном media-хосте задайте абсолютные пути к архиву и новому каталогу
выдачи. Возьмите `MEDIA_BACKUP_SHA256` из доверенного манифеста копии.
Экспорт работает из проверенной копии, восстанавливает её во временном
каталоге и сохраняет только публичные PNG и JSON.

```sh
PYTHONPATH="$PWD/network/media" python3 -B -m mcnd_media.gateway_export \
  --backup "$MEDIA_BACKUP" --backup-sha256 "$MEDIA_BACKUP_SHA256" \
  --destination "$MEDIA_EXPORT"
```

Укажите `$MEDIA_EXPORT/public` как `root` отдельного HTTPS `server` в Nginx
и подключите [конфигурацию маршрута](../../media/nginx-ipfs.conf) через
`include` в этом блоке. Используйте выделенный media-домен и предоставьте
пользователю Nginx права только на чтение каталога выдачи. Сначала проверьте
итоговую конфигурацию командой `nginx -t`. При замене копии подготовьте новый
каталог и переключите `root` после проверки.

Клиенты преобразуют `ipfs://<CID>` в `https://<media-домен>/ipfs/<CID>`.
Медиа возвращается по `GET` и `HEAD`, с `image/png` или `application/json`
и заголовками `nosniff` и CORS. CID и содержимое остаются прежними. Для
проверки при восстановлении используйте `EXPORT.json` с путями, SHA256 и
длинами объектов. Он хранится рядом с `public`, за пределами HTTP-каталога.

В GitHub CI после имитации потери основного хранилища и шлюза Nginx выдал
аватар DRC20, изображение ERC1155 и оба JSON по прежним CID. Тест сверил байты,
SHA256, MIME и отсутствие HTTP-доступа к служебным файлам.
[Прогон Linux CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44702433627).

Раздача через этот URL сохраняет CID и байты. Интеграцию media-домена в кошельки
нужно подключить отдельно.
[Семантика путей и MIME в Nginx](https://nginx.org/en/docs/http/ngx_http_core_module.html#try_files).

## Закрепление проверенной копии в Kubo

Для импорта подготовьте отдельный инициализированный репозиторий Kubo.
Утилита рассчитана на импорт при выключенном процессе Kubo для этого каталога.
Проверьте SHA256 исходного архива по доверенному манифесту. Из корня проекта:

```sh
PYTHONPATH="$PWD/network/media" python3 -B -m mcnd_media.pin_backup \
  --backup "$MEDIA_BACKUP" --backup-sha256 "$MEDIA_BACKUP_SHA256" \
  --ipfs "$KUBO_BINARY" --repo "$DEDICATED_IPFS_PATH"
```

Утилита проверяет архив, сверяет CID каждого файла с результатом Kubo и читает
закреплённые файлы обратно. В отчёте `pinned` перечислены закреплённые CID,
в `legacyRawHttpOnly` - старые raw-блоки больше 2 МиБ. Ссылки на эти
файлы продолжает обслуживать HTTPS-шлюз. После импорта доступность файлов в
сети IPFS зависит от запуска и связности отдельного узла с закреплёнными данными.
[Режимы Kubo](https://docs.ipfs.tech/reference/kubo/cli/).

В изолированном Linux CI два узла Kubo обменялись JSON и крупным NFT-изображением
по исходным CID. В том же прогоне Nginx выдал изображение после восстановления
из резервной копии. [Прогон Linux CI](https://github.com/maxwell2010/go-smart-node/actions/runs/45004941490).

## Команды Linux CI

Из корня репозитория:

```sh
python3 -m venv "$RUNNER_TEMP/media-venv"
"$RUNNER_TEMP/media-venv/bin/pip" install --only-binary=:all: --require-hashes \
  -r network/media/requirements-linux-cp312.lock -r network/media/requirements-api.lock
PYTHONPATH="$PWD/network/media" MCND_MEDIA_TEST_FULL=1 "$RUNNER_TEMP/media-venv/bin/python" -B -m unittest discover -s network/media/tests -p 'test_*.py'
```

Tests проверяют отказ без authorizer, другого владельца, границы namespace,
квоты, версии, дедупликацию, restart, изменённые blobs/backup, traversal и symlinks,
настоящую обработку PNG/JPEG/WebP, неверный MIME, SVG/HTML, усечённые данные,
pixel/decompression-bomb и восстановление исходных CID/owner bindings.
Тестовый pinned decoder локально не заменяется случайной установленной версией.

## Что ещё требуется для публичного сервиса

Для публичной эксплуатации нужны отдельный HTTPS origin и хостинг, интеграция
SDK/Wallet/Explorer, две независимые копии, IPFS pinning, мониторинг и правила
хранения и оплаты. Валидаторы могут обходиться без локальных медиафайлов.
Доступность шлюза не влияет на приём подписанной транзакции или AppHash.

Источники: [PyPI Pillow](https://pypi.org/project/Pillow/13.0.0/),
[IPFS pinning](https://docs.ipfs.tech/how-to/pin-files/),
[общий план user assets и metadata](USER_ASSET_CREATION_AND_MEDIA.md).
