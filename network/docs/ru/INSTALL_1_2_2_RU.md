# Установка MintCandy Node 1.2.2 для майнета

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
( set -eu; d=$(mktemp -d); trap 'rm -f -- "$d/install_software.py" "$d/provenance.sigstore.json"; rmdir -- "$d"' EXIT; curl -fsSL --proto '=https' --proto-redir '=https' 'https://raw.githubusercontent.com/maxwell2010/go-smart-node/c7c1331a201cd2141cf9d90493fbed539bf62642/network/tools/install_software.py' -o "$d/install_software.py"; printf '%s  %s\n' 'a18b765cf2f2a384dc819642f8a0872a9bd85bbf12b4bac6edf75c62d3f0d3a1' "$d/install_software.py" | sha256sum -c --status; curl -fsSL --proto '=https' --proto-redir '=https' 'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.2/provenance.sigstore.json' -o "$d/provenance.sigstore.json"; python3 -I -B "$d/install_software.py" --source-sha c7c1331a201cd2141cf9d90493fbed539bf62642 --source-ref refs/heads/release/mintcandy-1.2.2 --release-version 1.2.2 --native-denom msp --archive-sha ab7fb1563a544da1b0963134daff833bf49251cd1864c86e2f6a383db7f2b5a4 --archive-url 'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.2/mcnd-1.2.2-linux-amd64.tar.gz' --bundle "$d/provenance.sigstore.json" --destination "$HOME/.local/opt/mcnd/1.2.2-msp/mcnd" )
```

- SHA256 архива - `ab7fb1563a544da1b0963134daff833bf49251cd1864c86e2f6a383db7f2b5a4`.
- SHA256 бинарника - `b098a5174fd7df151dfaf7740362e1c1c782726b925f1439340eec112338d1d2`.

[Сборка и аттестация](https://github.com/maxwell2010/go-smart-node/actions/runs/45338729024).

## Переход с 1.2.1

1.2.2 включает обработчик возврата нативного обеспечения для ERC721 и
ERC1155. Операторы применяют его на высоте, утверждённой голосованием.
До принятой высоты действующая нода продолжает работать на 1.2.1.
[Замена бинарника, проверка AppHash и восстановление](https://github.com/maxwell2010/go-smart-node/blob/436d6075777b0a70ce8f03c0345f865f6d2e4ea8/network/docs/ru/GOVERNED_UPGRADE_1_2_2.md).

Для новой ноды установите подписанный genesis по инструкции 1.2.1.
При полном воспроизведении истории начните на 1.2.1 и на границе принятого
плана переключитесь на 1.2.2 на том же каталоге данных.
[Genesis и начальная синхронизация](https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.1/INSTALL_RU.md).

[Оборудование, пиры и RPC](https://github.com/maxwell2010/go-smart-node/blob/436d6075777b0a70ce8f03c0345f865f6d2e4ea8/network/docs/ru/OPERATOR_LAUNCH.md) ·
[Снапшоты и настройки хранения](https://github.com/maxwell2010/go-smart-node/blob/436d6075777b0a70ce8f03c0345f865f6d2e4ea8/network/docs/ru/SYNCHRONIZATION_AND_STORAGE.md).
