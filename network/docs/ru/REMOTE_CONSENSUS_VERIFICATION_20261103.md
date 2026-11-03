# Консенсусные индексы: удалённая проверка и следующие process gates

Статус: DEVELOPMENT. Рабочие ноды, их home и сервисы в этой проверке не
используются. Локальные Go/Solidity сборки и тесты не запускались; тяжёлые
проверки выполняются GitHub runner.

## Проверяемый исходный код

База сохранённых индексов:
`a11b4f3198848f41844dbfaefcaf3fd4eaa0744f`.
Дополнительные проверки и форматирование:
`90e539ef3a150f9b5ae438277a32db3c7ac99703`, ветка
`fix/remote-consensus-indexes-20261103`.

Запущен существующий workflow `modern-network-linux.yml`, без входных параметров:

```sh
gh workflow run modern-network-linux.yml \
  --repo maxwell2010/go-smart-node \
  --ref fix/remote-consensus-indexes-20261103
```

[Точный запуск CI](https://github.com/maxwell2010/go-smart-node/actions/runs/41633094656).
Результат: SUCCESS. Все шаги job125907358972 завершились успешно, включая
полный `go test ./...`, CGO/non-CGO crypto, owned-source integrity, воспроизведение
контрактов, SDK53 governance, ICS20 двух приложений и memory-only family probe.
Keeper-пакет прошёл за1.965s, отдельный SDK53 governance за0.131s, ICS20 за0.37s.
Это длительности соответствующих тестовых пакетов/сценария, не всей сборки.

[Компактное доказательство](evidence/remote-consensus-90e539e-20261103/summary.json)
содержит итоговые шаги и выбранные строки лога. Полные логи сохранены в artifact
`modern-network-validation-41633094656-1`, ID13229687746, срок хранения до3 декабря2026.
Digest ZIP:
`1a971f2480d4193daed0ca8ed0b710bcba60d113ece2ea115d151a10b33922bd`.
Результат относится к исходному коду90e539e; последующая публикация этого
обзора и JSON-доказательства не переименовывает проверенный runtime.

Индексы0x13/0x07 отражают текущих bonded-участников, их число и сумму мощности;
индекс0x32 отражает предыдущие положительные мощности. Верхний регистр допустимого
SDK Bech32 сохраняется в исходном тексте адреса. Старую мощность нельзя требовать
равной сегодняшнему RS или статусу: stake изменяется до следующего EndBlock.

Тест с2048 посторонними неактивными кандидатами запрещает обход полного реестра
кандидатов и raw power-строк. Сценарий оставляет предыдущие мощности100/200,
меняет первый RS на150/Unbonded, уменьшает текущий bonded-набор до одного и
успешно устанавливает MaxValidators1. Старый effective/H+1 набор из двух
участников сохраняется; ограничение применяется к новому составу H+2.
Непосредственное уменьшение MaxValidators ниже числа **текущих** bonded-участников
по-прежнему запрещается существующей проверкой SetParams/GenesisState.
Допустимые delayed sets от этого ограничения отличаются; финансовая политика
не ослабляется.

Дополнительные тесты проверяют точность агрегатов и membership-индексов,
пропавшие/лишние записи, отказ горячего пути от скрытого восстановления и
холодный rebuild только при корректном исходном ledger. Настоящие IAVL/LevelDB
закрываются и повторно открываются между изменением RS и выбором состава,
затем после выбора. Проверяется весь набор KV-строк, uppercase-идентичность,
канонический export и сохранённая очередь. Отдельные проверки покрывают
нормализацию нулевых мощностей, поздний отказ записи и OOG с полным откатом.

Ранжирующий индекс0x33 и полный контроллер контрактного допуска сюда не входят.
`eligible` ещё может быть большим. Положительный RS сам по себе не доказывает
допуск MasterValidator. Подключение финансового/консенсусного конвейера к App
и совместный бюджет SourceAuth остаются отдельными условиями выпуска.

## Какие process gates повторить после общей интеграции

Команды ниже - план для Linux runner после новой согласованной сборки.
В этой проверке они не выполнялись. Нужны проверенные Linux amd64 binaries,
совпадающие SHA256, полный source commit и их build/source provenance.
Текущий workflow сохраняет логи, а не новый installable mcnd archive.

Инструменты: Python3, `unshare` из util-linux, `ip` из iproute2; для сборки
reference process test также Go1.26.10, GCC/CGO и заранее проверенный module cache.
Namespace должен отличаться от `/proc/1/ns/net`, содержать только поднятый `lo`
и пустую IPv4 routing table. Hermes harness проверяет это и отказывается от
исходного namespace. Runner должен иметь разрешение на создание такого namespace;
отказ нельзя заменять запуском lab в основной сети.

Инструменты, зависимости и binaries готовятся **до** изоляции сети. В namespace
нет доступа к Интернету, публичным RPC, пирам или DNS-маршрутам. Все home, ключи,
genesis и подписываемые транзакции - только новые disposable fixtures.

Проверка жизненного цикла daemon из корня checkout:

```sh
# MCND_BINARY - абсолютный путь к проверенному binary общей интеграции.
: "${MCND_BINARY:?set verified absolute binary path}"
REPORT_ROOT=$(mktemp -d)
export MCND_BINARY REPORT_ROOT
unshare --net -- bash -c '
  set -euo pipefail
  ip link set lo up
  exec python3 network/tools/mcnd_process_smoke.py \
    --binary "$MCND_BINARY" --history-role rpc-ibc \
    --home-parent "$REPORT_ROOT" --report "$REPORT_ROOT/daemon.json"
'
```

Существующий скрипт проверяет создание своего genesis и ключей, явное
`reconcile-native-allocation`, высоту, native bank-транзакцию Ethereum operator,
точные fee/value/nonce, перезапуск и отказ несовпадающего домена без изменения home.
Retention здесь проверяется как применённая конфигурация, а не как длительное
удаление всего окна блоков. Home с fixture keyring остаётся в REPORT_ROOT;
публиковать нужно выбранный JSON-отчёт, не весь каталог с ключами.

Независимый reference process test из `network/`:

```sh
unshare --net -- bash -c '
  set -euo pipefail
  ip link set lo up
  export MC_IBC_REFERENCE_PROCESS_SMOKE=1
  exec go test -mod=readonly -p 1 -count=1 -timeout 10m \
    -run "^TestDisposableReferenceProcess$" -v ./internal/ibcreference
'
```

Он сам собирает reference в `t.TempDir`, создаёт private genesis, требует height3
и останавливает только свой PID. Это отдельный smoke, не межсетевой handshake.

Полный Hermes gate из корня checkout:

```sh
# Все переменные экспортированы из проверенных build-отчётов.
: "${MCND_BINARY:?}" "${MCND_SHA256:?}" "${MCND_REVISION:?}"
: "${REFERENCE_BINARY:?}" "${REFERENCE_SHA256:?}" "${REFERENCE_REVISION:?}"
: "${HERMES_BINARY:?}"
REPORT_ROOT=$(mktemp -d)
export REPORT_ROOT MCND_BINARY MCND_SHA256 MCND_REVISION
export REFERENCE_BINARY REFERENCE_SHA256 REFERENCE_REVISION HERMES_BINARY
unshare --net -- bash -c '
  set -euo pipefail
  ip link set lo up
  exec python3 network/internal/ibclab/hermes_lab.py --mode full \
    --mcnd-binary "$MCND_BINARY" --mcnd-sha256 "$MCND_SHA256" \
    --mcnd-source-commit "$MCND_REVISION" \
    --reference-binary "$REFERENCE_BINARY" --reference-sha256 "$REFERENCE_SHA256" \
    --reference-source-commit "$REFERENCE_REVISION" \
    --hermes-binary "$HERMES_BINARY" --report "$REPORT_ROOT/hermes.json"
'
```

Проверяются handshake, send/return, restart с pending packet, error-ACK и timeout
refund, реальные commitment/escrow/voucher и точные комиссии обоих relayer accounts.
WARN/ERROR health-check не превращаются в успех по одному summary. Обычный
`reference-probe` проверяет только контрагента и не заменяет `--mode full`.
Same-binary restart не доказывает module migration или IBC client upgrade.

## Ранее зафиксированные pins и доступность

Доступность GitHub artifact подтверждена3 ноября2026 через API, без загрузки:

- [Development run41608914841](https://github.com/maxwell2010/go-smart-node/actions/runs/41608914841),
  artifact13218266382, source `8cf619da1c77ee5efffc80a678272010fd2263dd`;
  срок хранения до17 ноября2026. Binary SHA256:
  `d70f04927738a5a7459fb5607ed8b28daafef260f8ca6773eeece817456fb099`.
  ZIP artifact digest:
  `1a2d0b48398dbc7a8e3d408229957751835de8cea6a7afd83208f0a5077b3474`.
  [Прежняя проверка provenance](evidence/published-cli-8cf619d-20261103/README.md).

Исторический [Hermes process report](../../internal/ibclab/MAINTAINED53_EVIDENCE.json)
записывает следующие binaries. Их наличие теперь нужно проверить отдельно;
они не загружались и не запускались в этой проверке:

| Компонент | Source pin | SHA256 binary |
| --- | --- | --- |
| mcnd | `21e1c205daaf7564d316a9e130f129d93c36cc83` | `e2d72ed9b86bacf2adae903db428ae59ce80bec940841f410698cbabd2aa4612` |
| reference | `c911426c280024f7462e6a5946d5cf258717a4e2` | `29f9d9188439750a7eae2e3e8574dc32ad2c4e4693cf990af7b037d28f70aa72` |
| Hermes v1.13.3 | официальный release archive | `ed8d57781f57a93e74259c12c8feee661b9633c882307f3a6236e41dbf5ffe35` |

SHA256 архива Hermes:
`3582136824ac1984c8884b310c63940a1489f091fa45faca38ed80e5dfe7d51b`.
Этот pinned binary сообщает версию `1.13.2+bab3b80`; это уже учтено прежним
[описанием lab](../../internal/ibclab/README.md).

Старые binaries и отчёты предшествуют новым индексам и дальнейшим финансовым
изменениям. Их нельзя переименовать в сборку нового commit или использовать как
доказательство нового runtime. Для process gate общей интеграции нужны новые
согласованные artifacts и новый отчёт. Подтверждённого production hardware
minimum, ограниченного роста диска или готовности стабильного релиза здесь нет.
