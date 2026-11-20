# Установка офлайн-утилит запуска MintCandy Network

Архив `mintcandy-launch-tools-linux-amd64.tar.gz` содержит готовый
`mcnd-genesis`, скрипты сверки заявок и манифеста, русскую инструкцию и
черновики параметров. Утилиты рассчитаны на Linux x86_64. CI проверяет их
на Ubuntu 24.04 с опубликованным `mcnd` 1.1.1. Для обычной работы нужны
Python 3.11+, `gpgv`, `tar` и `sha256sum`. Для получения и проверки
аттестации нужен GitHub CLI. Go для установки не требуется.

`mcnd-genesis` собирает genesis и проверяет холодный `InitChain` в памяти.
Пакет совместим с отдельным [выпуском ноды 1.1.1](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.1).
Публичный genesis подписывается организатором после подтверждения параметров
и состава валидаторов. У архива утилит собственная аттестация сборки GitHub.

## Загрузка и проверка

Выберите успешный прогон `Attested MintCandy node rehearsal` со сценарием
`tools`. Запишите его `RUN_ID`, номер попытки `ATTEMPT` и полный SHA коммита
`SOURCE_SHA` из объявления о поставке. Скачайте итоговый артефакт
`attested-mintcandy-launch-tools-N`, где `N` - номер попытки.
[Проверка аттестаций GitHub CLI](https://cli.github.com/manual/gh_attestation_verify).

```sh
set -eu
umask 077
work=$(mktemp -d)
repo=maxwell2010/go-smart-node
archive=mintcandy-launch-tools-linux-amd64.tar.gz
gh run download "$RUN_ID" --repo "$repo" \
  --name "attested-mintcandy-launch-tools-$ATTEMPT" --dir "$work"
(
  cd "$work"
  sha256sum --check --strict "$archive.sha256"
  gh attestation verify "$archive" --bundle launch-tools.sigstore.json \
    --repo "$repo" --signer-workflow "$repo/.github/workflows/published-node-rehearsal.yml" \
    --source-ref refs/heads/feat/native-duplicate-vote-master-20261112 \
    --source-digest "$SOURCE_SHA" --signer-digest "$SOURCE_SHA" \
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
аттестованного `mcnd` 1.1.1. [Порядок работы с genesis и подтверждениями](https://github.com/maxwell2010/go-smart-node/blob/feat/native-duplicate-vote-master-20261112/network/docs/ru/VALIDATOR_LAUNCH_INTAKE.md).
