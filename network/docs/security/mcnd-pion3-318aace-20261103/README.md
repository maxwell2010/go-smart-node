# Полные captures mcnd после перехода на Pion v3

Для source snapshot `318aace03f41132c4b6b704a287c05c227d6c26e` повторно выполнены оба официальных scan: исходников настоящего `mcnd` и собранного бинарника. DTLS v2 finding больше не появляется ни на одном уровне findings. Оба security gate остаются FAIL из-за OpenPGP; остальные module-only предупреждения также сохранены полностью.

## Объект и результаты

- Обычная Linux amd64 сборка, CGO/GCC, Go 1.26.10, `-mod=readonly -trimpath`, без test tags.
- SHA256 бинарника: `514dd9f6010c3458dccedd27c93ad178ed573c9ab3e29ac186ab42c7e7a57139`; размер 185 983 768 байт.
- Команда версии настоящего бинарника с отдельным home подтвердила `server_name=mcnd`, точный commit 318aace и SDK 0.53.8.
- Официальный govulncheck v1.9.0; database timestamp `2026-11-02T22:01:43Z`; exclusions отсутствуют.

| Режим | Symbol advisory ID | Symbol findings | Scanner exit | Manifest-bound gate |
| --- | --- | ---: | ---: | ---: |
| Source | GO-2026-5932 | 10 | 0 | FAIL / 1 |
| Binary | GO-2026-5932 | 9 | 0 | FAIL / 1 |

JSON-mode exit 0 означает завершение scanner, а не отсутствие findings. [OpenPGP advisory](https://pkg.go.dev/vuln/GO-2026-5932) остаётся открытым; scoped disposition SDK armor/удалённых CLI-команд из [предыдущего отчёта](../mcnd-b9636dc-20261103/README.md) не переименовывается в исправление.

Полный source raw имеет размер 1 140 225 байт и SHA256 `be54ee094d17f2e4489249f455236207ce7787fbf2b25288404ea6e595bdf09b`. Binary raw: 1 113 801 байт, SHA256 `5b3030678ca35cbad91c5b4b833d8c5ec1659733b16c0a5a0fd81a3294bb513c`.

## MVS inventory и реально компилируемые packages

Pion v3 h1, отсутствие replacements и исходники external cache проверены `verify_pion_transport.py` и официальным `go mod verify`. Default `go list -mod=readonly -deps ./cmd/mcnd` содержит STUN v3/DTLS v3 и не содержит v2. При этом MVS сохраняет старые v2 indirect-декларации Cosmos EVM. Они не удалялись из owned EVM metadata и не скрывались фильтром scanner.

Полный набор findings изменился с восьми advisory ID в b9636dc на семь в 318aace. Единственный исчезнувший ID - GO-2026-4479. Два старых symbol ID стали одним; шесть оставшихся module-only ID существовали и в прежнем capture. [MVS delta](mvs-delta.json), [module inventory](modules.jsonl.gz), [actual compiled imports](compiled-deps.txt.gz) и [полный integrity report](pion-integrity.json) сохранены отдельно.

## Module-only disposition

Эти записи не являются подтверждёнными достижимыми вызовами. Они не исключались из raw и не переводились автоматически в «исправлено».

| Advisory | Точный source/package disposition |
| --- | --- |
| [GO-2025-3442](https://pkg.go.dev/vuln/GO-2025-3442) | У отчёта две ветки: public `blocksync` исправлен в 0.38.17, а `internal/blocksync` - в 1.0.1. Фактический Comet 0.38.26 выше первого исправления; `internal/blocksync` в imports ноды отсутствует. |
| [GO-2026-5841](https://pkg.go.dev/vuln/GO-2026-5841) | Выбран compress 1.18.5; исправление опубликовано в 1.18.7. Затронута `s2.NewDict`; package `compress/s2` в actual imports отсутствует. Patch update остаётся отдельной работой. |
| [GO-2026-6860](https://pkg.go.dev/vuln/GO-2026-6860), [GO-2026-6866](https://pkg.go.dev/vuln/GO-2026-6866) | Выбран x/net 0.61.0, указано исправление в 0.62.0. Затронуты HTML parsing/rendering; `x/net/html` в actual imports отсутствует. Patch update остаётся отдельной работой. |
| [GO-2023-1821](https://pkg.go.dev/vuln/GO-2023-1821), [GO-2023-1881](https://pkg.go.dev/vuln/GO-2023-1881) | Описывается `x/crisis`, а не каждый SDK keeper. Этот package не импортируется default `mcnd`; finding остаётся module-only и не подтверждает наличие его Msg endpoint в ноде. |

[Первичный Comet advisory](https://github.com/cometbft/cometbft/security/advisories/GHSA-22qq-3xwm-r5x4) явно разделяет исправления 0.38.17 и 1.0.1. Поэтому единичное поле scanner `fixed_version=1.0.1` не является основанием требовать смены core family для выбранной ветки 0.38. [Первичные SDK advisories](https://github.com/cosmos/cosmos-sdk/security/advisories/GHSA-qfc5-6r3j-jj22), [ConstantFee](https://github.com/cosmos/cosmos-sdk/security/advisories/GHSA-w5w5-2882-47pc) относятся к конкретному crisis-модулю. Здесь не имитировались атаки на blocksync или транзакции остановки цепочки.

Полные ranges, affected imports, перечисленные symbols, результаты сопоставления с actual imports и primary references сохранены в [module-only-disposition.json](module-only-disposition.json). Отсутствие затронутого package в этом бинарнике не сертифицирует весь модуль и не распространяется автоматически на будущий entry point или build tags.

## Проверки совместимости и границы

В private network namespace только с loopback прошли обычные STUN codec/client/закрытие, UDP connection, настоящий geth NAT и DTLS v3 PSK handshake ping/pong. Crypto/signature/math проверки прошли с CGO=1 и CGO=0. App и stateless CLI также прошли. Логи этих source-specific проверок входят в комплект.

Это отдельный dependency snapshot, не финальная интегрированная нода. Он не подтверждает новую модель изолированных ERC1155 stake positions, mainnet replay, все financial failure gates или готовность стабильного релиза. Local geth/EVM source replacements требуют собственного provenance и ручного сопоставления upstream advisories; прежние ограничения покрытия форков остаются применимыми.

[provenance.json](provenance.json) связывает commit, binary SHA, фактическую версию, toolchain/scanner, module files, source manifests и все приложенные файлы. Full raw сохранён в `source.jsonl.gz`/`binary.jsonl.gz`; completion manifests проверяют SHA256 распакованных байтов. Summaries не заменяют raw. Старые captures b9636dc не перезаписываются. После объединения с новыми финансовыми изменениями нужны новая сборка и captures конкретного интегрированного commit.
