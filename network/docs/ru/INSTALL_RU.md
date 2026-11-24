# Установка MintCandy Node 1.2.1 для майнета

Пакет Linux amd64 для `mintcandy_9905401-1`, EVM `9905401`, нативной
монеты MSP (`msp`, 18 знаков) и обёртки WMSP. Проверенная платформа -
Ubuntu 24.04 с systemd. Нужны Python 3.11+, curl, CA-сертификаты, sha256sum
и GitHub CLI с `gh attestation verify`. Для подписанного genesis нужен
`gpgv`. [Установка GitHub CLI](https://github.com/cli/cli/blob/trunk/docs/install_linux.md).

```sh
sudo apt-get update
sudo apt-get install --yes python3 curl ca-certificates coreutils gpgv
```

## Быстрая установка бинарника

Выполните от обычного пользователя. Команда проверяет SHA256 установщика,
архива и аттестацию. Установка использует новый путь
`~/.local/opt/mcnd/1.2.1-msp/mcnd` и не запускает службу.

```sh
( set -eu; d=$(mktemp -d); trap 'rm -f -- "$d/install_software.py" "$d/provenance.sigstore.json"; rmdir -- "$d"' EXIT; curl -fsSL --proto '=https' --proto-redir '=https' 'https://raw.githubusercontent.com/maxwell2010/go-smart-node/b1a460046270399592773c845d00aef4b7277a9b/network/tools/install_software.py' -o "$d/install_software.py"; printf '%s  %s\n' 'a18b765cf2f2a384dc819642f8a0872a9bd85bbf12b4bac6edf75c62d3f0d3a1' "$d/install_software.py" | sha256sum -c --status; curl -fsSL --proto '=https' --proto-redir '=https' 'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.1/provenance.sigstore.json' -o "$d/provenance.sigstore.json"; python3 -I -B "$d/install_software.py" --source-sha b1a460046270399592773c845d00aef4b7277a9b --source-ref refs/heads/codex/mintcandy-native-denoms --release-version 1.2.1 --native-denom msp --archive-sha e70dbdd964bb24dc01ba0f46782290402165306dace3ae30f3864b574bea472c --archive-url 'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.1/mcnd-1.2.1-linux-amd64.tar.gz' --bundle "$d/provenance.sigstore.json" --destination "$HOME/.local/opt/mcnd/1.2.1-msp/mcnd" )
```

SHA256 архива - `e70dbdd964bb24dc01ba0f46782290402165306dace3ae30f3864b574bea472c`. SHA256 бинарника - `dc3fa4e88fe03173d81a03e2cc7ae75b17f8960ed2e8a1775130cd7aa0bf6c84`.
[Сборка и аттестация](https://github.com/maxwell2010/go-smart-node/actions/runs/45314434795).

## Новая нода из подписанного genesis

Genesis и подпись основателя опубликованы в выпуске `v1.2.0`. Выполните
блок «Быстрая установка бинарника» из его инструкции. Установщик genesis
сверяет SHA256 именно с этим бинарником. Затем выполните блок ниже для
нового наблюдателя. Он создаёт каталог `~/.mintcandy-mainnet`, проверяет
архив и OpenPGP-подпись, устанавливает genesis и валидирует его в v1.2.1.
[Установка бинарника v1.2.0](https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.0/INSTALL_RU.md).

```sh
(
  set -eu
  umask 077
  mcnd_foundation="$HOME/.local/opt/mcnd/1.2.0-msp/mcnd"
  mcnd_current="$HOME/.local/opt/mcnd/1.2.1-msp/mcnd"
  mcnd_home="$HOME/.mintcandy-mainnet"
  test -x "$mcnd_foundation"
  test -x "$mcnd_current"
  test ! -e "$mcnd_home"
  test ! -L "$mcnd_home"
  work=$(mktemp -d)
  trap 'rm -rf -- "$work"' EXIT
  curl -fsSL --proto '=https' --proto-redir '=https' \
    'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.0/mintcandy-mainnet-verified-launch-fb210aac.tar.gz' \
    -o "$work/launch.tar.gz"
  printf '%s  %s\n' '9bfd4305fa553a5504835cb817038e35cfeb37521c7595c982609c0e3cec694b' \
    "$work/launch.tar.gz" | sha256sum -c --status
  tar -xzf "$work/launch.tar.gz" --no-same-owner --no-same-permissions -C "$work"
  "$mcnd_foundation" init mintcandy-observer --home "$mcnd_home" \
    --chain-id mintcandy_9905401-1 --evm.evm-chain-id 9905401
  python3 -B "$work/network/tools/public_genesis_manifest.py" install-observer \
    --home "$mcnd_home" --binary "$mcnd_foundation" --genesis "$work/genesis.json" \
    --manifest "$work/genesis-manifest.json" --signature "$work/genesis-manifest.json.asc" \
    --trusted-keyring "$work/genesis-signer.gpg" --signer-fingerprint E6F019522D030B2F37249D4B7903FAD265CBFDB9 \
    --approved-manifest-sha256 fb210aac9393babbb87b56bff5b4f097ae2a30f65f768520904560d99c4571da \
    --draft "$work/network/docs/ru/public-genesis-msp.draft.json" \
    --plan "$work/network/docs/ru/public-launch-allocation-msp.draft.json" \
    --software-record "$work/software-verified.json" \
    --approved-software-record-sha256 666dba9207337f7a727e582518864e301a5a703b7a6bcaf48412fe3d3e7a7008
  "$mcnd_current" genesis validate-genesis --home "$mcnd_home" \
    --chain-id mintcandy_9905401-1 --evm.evm-chain-id 9905401
)
```

Используйте в службе systemd бинарник v1.2.1 и созданный каталог. Постоянный
пир для первого подключения указан в `publicP2P.persistent_peers` отчёта
`OBSERVER_GENESIS_INSTALLED`. [Пиры, RPC и синхронизация](https://github.com/maxwell2010/go-smart-node/blob/codex/mintcandy-native-denoms/network/docs/ru/OPERATOR_LAUNCH.md#пиры-сиды-и-rpc).

## Обновление существующей ноды

1. Проверьте новый бинарник командой `version --long` и сохраните путь к
   текущему бинарнику для отката.
2. Согласуйте окно обновления с другими операторами. Остановите только
   выбранную для обновления службу и сделайте офлайн-копию её каталога
   данных и конфигурации.
3. Укажите новый абсолютный путь к бинарнику в `ExecStart`, сохраните
   параметры `--home`, chain ID и порты. Перечитайте конфигурацию systemd
   и запустите службу. Проверьте рост высоты и AppHash на общей высоте.
4. Для отката бинарника используйте предыдущий исполняемый файл и
   сохранившееся актуальное состояние. Резервную копию базы восстанавливайте
   по отдельной процедуре. Для валидатора ключ и состояние последней подписи
   должны соответствовать одному активному подписанту, а высота последней
   подписи при откате не должна уменьшаться.
   [Восстановление и правила подписания](https://github.com/maxwell2010/go-smart-node/blob/codex/mintcandy-native-denoms/network/docs/ru/SYNCHRONIZATION_AND_STORAGE.md).

Genesis сети и утверждённые параметры консенсуса сохраняются. В этом выпуске
обновлены зависимости. [Репетиция перехода с незавершённым пакетом IBC](https://github.com/maxwell2010/go-smart-node/actions/runs/45318226553).
