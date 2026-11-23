# Установка офлайн-утилит запуска MintCandy Network

Архив `mintcandy-launch-tools-linux-amd64.tar.gz` содержит готовый
`mcnd-genesis`, скрипты сверки заявок и манифеста, русскую инструкцию и
черновики параметров. Утилиты рассчитаны на Linux x86_64. CI проверяет их
на Ubuntu 24.04 с опубликованным `mcnd`. Для обычной работы нужны Python
3.11+, `gpgv`, `tar` и `sha256sum`. Для загрузки и проверки аттестации нужен
GitHub CLI. Go для установки не требуется.

`mcnd-genesis` собирает genesis и проверяет холодный `InitChain` в памяти.
В `PACKAGE.json` указаны совместимая версия `mcnd` и SHA256 её бинарника.
Публичный genesis подписывается организатором после подтверждения параметров
и состава валидаторов. У архива утилит собственная аттестация сборки GitHub.

## Загрузка и проверка

Файлы утилит и `LAUNCH_TOOLS_VALIDATION.json` публикуются в выпуске той же
версии, что и совместимый `mcnd`. Задайте `RELEASE_TAG` из страницы выпуска,
`TOOLS_SOURCE_SHA` и `TOOLS_ARCHIVE_SHA256` из отчёта. Сверьте ожидаемые
версию и SHA256 `mcnd` со своим пакетом.
[Выпуски MintCandy](https://github.com/maxwell2010/go-smart-node/releases) ·
[Проверка аттестаций GitHub CLI](https://cli.github.com/manual/gh_attestation_verify).

```sh
set -eu
umask 077
work=$(mktemp -d)
repo=maxwell2010/go-smart-node
archive=mintcandy-launch-tools-linux-amd64.tar.gz
: "${RELEASE_TAG:?Укажите тег релиза}" "${TOOLS_SOURCE_SHA:?Укажите SHA исходников утилит}" \
  "${TOOLS_ARCHIVE_SHA256:?Укажите SHA256 архива утилит}"
gh release download "$RELEASE_TAG" --repo "$repo" --dir "$work" \
  --pattern "$archive" --pattern "$archive.sha256" --pattern launch-tools.sigstore.json
(
  cd "$work"
  sha256sum --check --strict "$archive.sha256"
  printf '%s  %s\n' "$TOOLS_ARCHIVE_SHA256" "$archive" | sha256sum --check --strict
  gh attestation verify "$archive" --bundle launch-tools.sigstore.json \
    --repo "$repo" --signer-workflow "$repo/.github/workflows/published-node-rehearsal.yml" \
    --source-ref refs/heads/feat/native-duplicate-vote-master-20261112 \
    --source-digest "$TOOLS_SOURCE_SHA" --signer-digest "$TOOLS_SOURCE_SHA" \
    --deny-self-hosted-runners
  mkdir -m 700 tools
  tar --no-same-owner --no-same-permissions -xzf "$archive" -C tools
  cd tools
  sha256sum --check --strict SHA256SUMS
  bin/mcnd-genesis --help
)
printf '%s\n' "$work/tools"
```

Каталог `tools` создаётся внутри нового временного каталога. Можно перенести
его целиком в постоянный каталог оператора. Структура `network/tools` и
`network/docs/ru` нужна скриптам для поиска черновиков и хеша бинарника.
Поля `sourceRevision`, `assemblerSha256` и совместимость с `mcnd` записаны
в `PACKAGE.json`. При переносе сохраните архив, контрольную сумму и bundle.

## Работа с пакетом

Из корня распакованных утилит задайте путь к бинарнику. В отдельной копии
анкеты соберите данные операторов и проверьте её офлайн.

```sh
export MCND_GENESIS="$PWD/bin/mcnd-genesis"
python3 -B network/tools/check_public_launch_draft.py
python3 -B network/tools/check_operator_applications.py "$REVIEWED_INTAKE" --require-four
```

Дальнейшие команды подготовки реестра, сборки, подписи, установки genesis и
подтверждения оператора выполняются из того же каталога. Для проверки или
установки подписанного genesis `MCND_BINARY` должен указывать на экземпляр
аттестованного `mcnd` с версией и SHA256 из `PACKAGE.json`.
[Порядок работы с genesis и подтверждениями](https://github.com/maxwell2010/go-smart-node/blob/feat/native-duplicate-vote-master-20261112/network/docs/ru/VALIDATOR_LAUNCH_INTAKE.md).
