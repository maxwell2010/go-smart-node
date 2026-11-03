# Переход реального geth NAT на Pion v3

Изменён импорт STUN в фактической реализации `third_party/cosmos-geth/p2p/nat/stun.go`: теперь используется официальный `github.com/pion/stun/v3`, связанный с DTLS v3. Замены имени модуля `v2 => v3` нет. API geth NAT сохраняется: разбор настройки `stun:...`, `ExternalIP`, `SupportsMapping`, методы отображения портов и формат результата не вырезались.

## Закреплённые upstream-версии

| Модуль | Версия | h1 |
| --- | --- | --- |
| `github.com/pion/stun/v3` | `v3.1.7` | `h1:uRXMTlGLf89WgItGNyZ6aR5jMTX0NBbybXADpQCzn+E=` |
| `github.com/pion/dtls/v3` | `v3.1.15` | `h1:okWk5LNwU21DfhvG8z4wm45knHVSPEdkihwp6BxfQcc=` |

Это проверенные опубликованные tags, а не собственные версии библиотеки. [STUN v3](https://pkg.go.dev/github.com/pion/stun/v3@v3.1.7) сохраняет используемые geth операции `Dial`, `Build`, `Client.Do/Close`, `Event` и `XORMappedAddress.GetFrom`. [Код клиента](https://github.com/pion/stun/blob/v3.1.7/client.go) также сохраняет STUN/TURN/TURNS и TLS/DTLS ветки `DialURI`; они не были удалены ради уменьшения отчёта сканера. DTLS v3 использует PacketConn и отдельный адрес собеседника. Для текущих версий нужен Go не ниже 1.24; сборка проекта остаётся на закреплённом Go 1.26.10.

Объявленный geth label `v1.16.2-cosmos-1-mintcandy.2` сохраняет точное исправление координат из предыдущей группы и добавляет NAT import/go.mod adaptation. Все пять отличающихся от канонической базы файлов перечислены в `third_party/cosmos-geth/UPSTREAM.json`; неизменённый geth `go.sum` сохраняет канонический SHA256. Смена библиотек не применяется к старому mainnet/replay модулю корня репозитория.

## MVS и реально импортируемый код

`go list -mod=readonly -deps ./cmd/mcnd` подтверждает наличие STUN v3/DTLS v3 и отсутствие импорта STUN v2/DTLS v2 в обычной ноде. После этой проверки удалены устаревшая tracked v2-копия, её local replace и `verify_owned_stun.py`. Module cache и другие рабочие копии не изменялись.

При этом `go list -m all` всё ещё содержит неиспользуемые версии `pion/stun/v2 v2.0.0` и `pion/dtls/v2 v2.2.7` из старых indirect-деклараций upstream Cosmos EVM. Канонический owned-EVM `go.mod` ради этого не переписывался. Это разные утверждения: отсутствие v2 в коде фактического `mcnd` не является удалением v2 из всего MVS inventory. Сканер запускается полностью; module-only и symbol findings следует разбирать отдельно.

Относительно geth-backport snapshot изменяются версии logging 0.2.2 -> 0.2.4, testify 1.11.1 -> 1.12.1, objx 0.5.2 -> 0.5.3 и YAML v3 3.0.4 -> 3.0.5. Добавляются STUN/DTLS v3, transport v4/v5 и anet. Версии SDK, Comet, Cosmos EVM, IBC, `x/crypto`, `x/net` и остальные ранее выбранные версии сохранены. Старые checksums не удаляются; новые h1 дополняют `go.sum`.

## Проверка происхождения и установка

`scripts/verify_pion_transport.py` требует точные version/h1/go.mod h1, отвергает alias/local replacement, запускает официальный `go mod verify` и проверяет реальные default imports `mcnd`. Все запросы графа используют `-mod=readonly`; helper не запускает tidy/download и проверяет неизменность `go.mod`/`go.sum`. Отдельные negative checks подтверждают отказ при version/h1 mismatch, replacement и возврате v2 в imports. Наличие старого неиспользуемого MVS inventory выводится в отчёте.

Порядок CI/installer: проверка owned EVM/geth, стандартные download/integrity checks, затем upstream Pion checker и сборка. Имена и вызовы обновлены одновременно. Исторический однострочник с commit 245f13b сохраняет старый source-bound смысл; он не переименован в установку текущей версии.

Оригинальные MIT notices STUN и DTLS сохранены в `third_party/pion-transport-notices` и входят в development packaging отдельными файлами, со ссылками на upstream и SHA256. Они не заменяют geth GPL/LGPL notices или лицензию основного репозитория.

## Обычные проверки

В отдельном network namespace, содержащем только loopback, прошли:

- Корректный codec round trip IPv4/IPv6, fingerprint и transaction ID.
- Создание и закрытие клиента, повторное закрытие и отказ использовать уже закрытый клиент.
- UDP binding request/response к собственному локальному серверу.
- `ExternalIP` настоящего owned geth NAT через тот же локальный сервер.
- Обычный DTLS v3 PSK handshake, передача ping/pong и закрытие локальных соединений с искусственным ключом.

Эти проверки не используют стандартный список внешних STUN-серверов, рабочие ноды или специальные контексты EVM. Они подтверждают перечисленные пути, а не все функции upstream-библиотек.

Обычные crypto/curve/signature проверки и upstream crypto/math прошли в обоих режимах CGO. Проверки App и stateless CLI также прошли на данном dependency snapshot. Это проверки совместимости; они не подтверждают новую финансовую модель изолированных ERC1155 positions или готовность релиза.

Для security disposition необходимы новые полные source/binary captures конкретного commit. Исторические [два FAIL из snapshot b9636dc](mcnd-b9636dc-20261103/README.md) не переписываются и не исключаются. OpenPGP остаётся отдельным открытым gate; полный интегрированный релиз и оставшиеся geth advisories этим переходом не сертифицируются.
