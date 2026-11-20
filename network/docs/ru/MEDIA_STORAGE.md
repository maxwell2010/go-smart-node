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
- Сетевых запросов нет: нельзя передать URL для SSRF, redirect или DNS rebinding.
  HTML/script metadata frontend обязан выводить как текст, без innerHTML.

## Авторизация - обязательная граница

`MediaStore(root, authorizer=...)` требует доверенный callback на каждую публикацию:
`authorizer(asset, actor, "publish")` должен вернуть буквально `True`.
По умолчанию публикация запрещена. Строка owner, переданная пользователем,
не является доказательством владения. HTTP-запрос не должен получать право
передавать callback или значение результата его проверки.

Production adapter ещё нужно подключить к проверке подписи и актуального
владельца в committed state соответствующей сети. Он должен проверять chain ID,
contract, ERC20/ERC721/ERC1155 и полный uint256 ID, действие, nonce/срок и права
на изменение metadata. Этот компонент не выдаётся за готовую реализацию такого
API. Tests используют явный доверенный adapter-фикстуру; это не wallet proof.
При смене on-chain владельца adapter разрешает изменение новому владельцу;
история хранит публичного автора каждой предыдущей версии.

## Формат и целостность

Asset namespace состоит из chain ID, contract, типа и token ID. Объекты имеют
CIDv1 raw/SHA-256; SHA256 вычисляется по фактически сохранённым байтам. Это
валидный content identifier, но создание имени CID не размещает объект в IPFS.
Metadata JSON canonical, поле image - `ipfs://<CID нормализованной картинки>`.
Доступный через identity/URI pointer на JSON короче 256 байт.

Дедупликация не создаёт второй экземпляр одинакового содержимого. SQLite index
связывает namespace, owner, номер версии, metadata CID и image CID. Публикация
сериализована транзакцией; при отказе квоты или авторизации история не меняется.
Чужие существующие объекты не перезаписываются. `verify()` сверяет CID, байты,
references, namespace, версии, отсутствие лишних объектов и квоты.

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

Раздача через этот URL сохраняет CID и байты. Публичный pinning в сети IPFS
и интеграцию media-домена в кошельки нужно подключить отдельно.
[Семантика путей и MIME в Nginx](https://nginx.org/en/docs/http/ngx_http_core_module.html#try_files).

## Команды Linux CI

Из корня репозитория:

```sh
python3 -m venv "$RUNNER_TEMP/media-venv"
"$RUNNER_TEMP/media-venv/bin/pip" install --only-binary=:all: --require-hashes -r network/media/requirements-linux-cp312.lock
PYTHONPATH="$PWD/network/media" MCND_MEDIA_TEST_FULL=1 "$RUNNER_TEMP/media-venv/bin/python" -B -m unittest discover -s network/media/tests -p 'test_*.py'
```

Tests проверяют отказ без authorizer, другого владельца, границы namespace,
квоты, версии, дедупликацию, restart, изменённые blobs/backup, traversal и symlinks,
настоящую обработку PNG/JPEG/WebP, неверный MIME, SVG/HTML, усечённые данные,
pixel/decompression-bomb и восстановление исходных CID/owner bindings.
Тестовый pinned decoder локально не заменяется случайной установленной версией.

## Что ещё требуется для публичного сервиса

Для публичной эксплуатации нужны отдельный HTTPS origin и хостинг, автоматизация
публикации с wallet/on-chain authorization, интеграция SDK/Wallet/Explorer,
две независимые копии, IPFS pinning, мониторинг и правила хранения и оплаты.
Валидаторы не должны хранить все медиа, а доступность gateway не должна влиять
на приём транзакции или AppHash. Эти deployment-границы не подтверждаются
локальным object-store тестом и не объявлены готовыми автоматически.

Источники: [PyPI Pillow](https://pypi.org/project/Pillow/13.0.0/),
[IPFS pinning](https://docs.ipfs.tech/how-to/pin-files/),
[общий план user assets и metadata](USER_ASSET_CREATION_AND_MEDIA.md).
