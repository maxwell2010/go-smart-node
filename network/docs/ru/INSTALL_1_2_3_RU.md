# Установка MintCandy Node 1.2.3 для майнета

Linux amd64, сеть `mintcandy_9905401-1`, EVM `9905401`, нативная монета
MSP (`msp`, 18 знаков). Для готового бинарника на Ubuntu 24.04
нужны Python 3.11+, curl, CA-сертификаты, sha256sum и GitHub CLI с
`gh attestation verify`. Go нужен только для сборки из исходников.
[Установка GitHub CLI](https://github.com/cli/cli/blob/trunk/docs/install_linux.md).

## Быстрая установка

Выполните от обычного пользователя. Команда проверяет SHA256 установщика,
SHA256 архива и происхождение пакета. Бинарник устанавливается в новый
каталог, службу systemd запускает оператор по процедуре для своей сети.

```sh
( set -eu; d=$(mktemp -d); trap 'rm -f -- "$d/install_software.py" "$d/provenance.sigstore.json"; rmdir -- "$d"' EXIT; curl -fsSL --proto '=https' --proto-redir '=https' 'https://raw.githubusercontent.com/maxwell2010/go-smart-node/ebc6111a488ee75166d5c0f8fb112e5b4b35d578/network/tools/install_software.py' -o "$d/install_software.py"; printf '%s  %s\n' 'a18b765cf2f2a384dc819642f8a0872a9bd85bbf12b4bac6edf75c62d3f0d3a1' "$d/install_software.py" | sha256sum -c --status; curl -fsSL --proto '=https' --proto-redir '=https' 'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.3/provenance.sigstore.json' -o "$d/provenance.sigstore.json"; python3 -I -B "$d/install_software.py" --source-sha ebc6111a488ee75166d5c0f8fb112e5b4b35d578 --source-ref refs/heads/release/mintcandy-1.2.3 --release-version 1.2.3 --native-denom msp --archive-sha 079c9fe2b275f4e14db49ff90237305e6989d8ee182e0dda77035f6c762ddc08 --archive-url 'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.3/mcnd-1.2.3-linux-amd64.tar.gz' --bundle "$d/provenance.sigstore.json" --destination "$HOME/.local/opt/mcnd/1.2.3-msp/mcnd" )
```

- SHA256 архива - `079c9fe2b275f4e14db49ff90237305e6989d8ee182e0dda77035f6c762ddc08`.
- SHA256 бинарника - `a27d36e8b4a8e07319357330aac84eb1f0c4f5509f173dab77199a61a256c6c5`.

[Сборка и аттестация](https://github.com/maxwell2010/go-smart-node/actions/runs/45367941768). [Проверки версионных архивов на изолированных нодах](https://github.com/maxwell2010/go-smart-node/actions/runs/45371010838).

## Какую версию запускать

Версия 1.2.3 исправляет название монеты возврата в REST и EVM RPC. В правилах
обработки блока она соответствует 1.2.2. Для сети, которая уже перешла
на 1.2.2, повторное голосование для этого исправления не требуется. Оператор
может установить пакет рядом с прежней версией и переключить службу во время
планового обслуживания. После перезапуска сверьте chain ID, рост высоты и
AppHash с другим валидатором на одной высоте. Проверьте также поле
`refund_asset` в `GET /mintcandy/fees/v1/policy` на локальном REST.

Если сеть работает на 1.2.1 или нода воспроизводит историю с genesis,
следуйте утверждённой высоте перехода. До неё нужен бинарник прежней версии.
[Процедура обновления](https://github.com/maxwell2010/go-smart-node/blob/ebc6111a488ee75166d5c0f8fb112e5b4b35d578/network/docs/ru/GOVERNED_UPGRADE_1_2_2.md) · [Genesis и начальная синхронизация](https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.1/INSTALL_RU.md).

[Оборудование и пиры](https://github.com/maxwell2010/go-smart-node/blob/ebc6111a488ee75166d5c0f8fb112e5b4b35d578/network/docs/ru/OPERATOR_LAUNCH.md) · [Синхронизация, снапшоты и хранение](https://github.com/maxwell2010/go-smart-node/blob/ebc6111a488ee75166d5c0f8fb112e5b4b35d578/network/docs/ru/SYNCHRONIZATION_AND_STORAGE.md).
