# Установка MintCandy Node 1.2.2 для тестовой сети

Linux amd64, сеть `mintcandy_990098-1`, EVM `990098`, нативная монета
tMSP (`tmsp`, 18 знаков). Для готового бинарника на Ubuntu 24.04
нужны Python 3.11+, curl, CA-сертификаты, sha256sum и GitHub CLI с
`gh attestation verify`. Go нужен только для сборки из исходников.
[Установка GitHub CLI](https://github.com/cli/cli/blob/trunk/docs/install_linux.md).

## Быстрая установка

Выполните от обычного пользователя. Команда проверяет SHA256 установщика,
SHA256 архива и происхождение пакета. Бинарник устанавливается в новый
каталог, службу systemd запускает оператор по процедуре для своей сети.

```sh
( set -eu; d=$(mktemp -d); trap 'rm -f -- "$d/install_software.py" "$d/provenance.sigstore.json"; rmdir -- "$d"' EXIT; curl -fsSL --proto '=https' --proto-redir '=https' 'https://raw.githubusercontent.com/maxwell2010/go-smart-node/c7c1331a201cd2141cf9d90493fbed539bf62642/network/tools/install_software.py' -o "$d/install_software.py"; printf '%s  %s\n' 'a18b765cf2f2a384dc819642f8a0872a9bd85bbf12b4bac6edf75c62d3f0d3a1' "$d/install_software.py" | sha256sum -c --status; curl -fsSL --proto '=https' --proto-redir '=https' 'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.2-testnet/provenance.sigstore.json' -o "$d/provenance.sigstore.json"; python3 -I -B "$d/install_software.py" --source-sha c7c1331a201cd2141cf9d90493fbed539bf62642 --source-ref refs/heads/release/mintcandy-1.2.2 --release-version 1.2.2 --native-denom tmsp --archive-sha 60c2822cd44a876881ffc981d1375b47286f6c86ce4414b6f705c297b6545e30 --archive-url 'https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.2-testnet/mcnd-1.2.2-tmsp-linux-amd64.tar.gz' --bundle "$d/provenance.sigstore.json" --destination "$HOME/.local/opt/mcnd/1.2.2-tmsp/mcnd" )
```

- SHA256 архива - `60c2822cd44a876881ffc981d1375b47286f6c86ce4414b6f705c297b6545e30`.
- SHA256 бинарника - `5a88caab856f4725d281694a2ece4672a96fd98a488d14faad17d9b318a8371d`.

[Сборка и аттестация](https://github.com/maxwell2010/go-smart-node/actions/runs/45338729464).

## Genesis и подключение

Архив содержит бинарник. Подтверждённый genesis, его SHA256 и адреса
пиров или сидов получите у оператора своего стенда. Для тестнета нужен
отдельный каталог данных, набор портов и, если используется, служба systemd.

Создайте локальную конфигурацию в новом каталоге. Если каталог уже есть,
используйте правила обновления своего стенда.

```sh
binary="$HOME/.local/opt/mcnd/1.2.2-tmsp/mcnd"
node_dir="$HOME/.mintcandy-tmsp"
if [ ! -e "$node_dir" ] && [ ! -L "$node_dir" ]; then
  "$binary" init mintcandy-testnet --home "$node_dir" \
    --chain-id mintcandy_990098-1 --evm.evm-chain-id 990098
fi
```

Сверьте SHA256 genesis от оператора и установите его перед запуском.
Проверьте его командой `genesis validate-genesis` с тем же `--home`,
`--chain-id` и `--evm.evm-chain-id`. В `config.toml` укажите сиды или
постоянные пиры своего стенда. Высоту согласованного обновления определяет
тестовая сеть. Для стенда, на котором план уже принят, уточните у оператора
версию для воспроизведения истории и точку перехода.

[Пиры, синхронизация и хранение](https://github.com/maxwell2010/go-smart-node/blob/436d6075777b0a70ce8f03c0345f865f6d2e4ea8/network/docs/ru/SYNCHRONIZATION_AND_STORAGE.md).
