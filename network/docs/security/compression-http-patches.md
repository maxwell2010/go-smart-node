# Совместимые обновления compression и HTTP

Выбраны официальные `github.com/klauspost/compress v1.18.7` и `golang.org/x/net v0.62.0`. Это отдельная группа после Pion migration. SDK/Comet/EVM/IBC, Pion и `x/crypto v0.58.0` не менялись. Сопоставление всего MVS подтверждает изменение ровно двух выбранных модулей и отсутствие добавленных/удалённых module paths.

| Модуль | Ранее | Теперь | h1 новой версии |
| --- | --- | --- | --- |
| compress | 1.18.5 | 1.18.7 | `h1:aUyZsS4kH3QTKurYhAOwAHxllVPnOthb3vPfnF1Ehjw=` |
| x/net | 0.61.0 | 0.62.0 | `h1:18ELalfM5+XNRwzM2nYqXsZQ9ByS1uhj2O/U7+5jqRY=` |

[GO-2026-5841](https://pkg.go.dev/vuln/GO-2026-5841) указывает исправление `s2.NewDict` в 1.18.7; официальный tag соответствует fix commit `8668e357e776d5152ed62f33c17f21b8690664fa`. [GO-2026-6860](https://pkg.go.dev/vuln/GO-2026-6860) и [GO-2026-6866](https://pkg.go.dev/vuln/GO-2026-6866) указывают исправления HTML parsing/rendering в x/net 0.62.0. В предыдущем production entry point affected packages были не импортированы, поэтому findings были module-only; обновление устраняет известные затронутые version ranges без их исключения из scanner.

Новый x/net требует Go 1.26.0 и сохраняет зависимость от `x/crypto 0.58.0`; закреплённый Go проекта 1.26.10 подходит. Здесь не удалён устаревший OpenPGP import SDK и не изменены форматы ключей. Это остаётся отдельным design/validation gate.

Прошли официальная module integrity и обычные локальные проверки:

- Zstd и S2 encode/decode на пустых, коротких и небольших обычных данных.
- Настоящий TLS HTTP/2 client/server с выбранным x/net, доверенным тестовым сертификатом и закрытием соединений.
- Нормальный HTML parse/render/parse round trip.
- Default App и stateless CLI compatibility tests.

Сетевой тест выполнялся в отдельном namespace только с loopback. Bomb fixtures, специально подготовленные dictionary payloads и специальные контексты EVM не запускались. Исходный module cache не патчился. Оригинальные notices изменённых зависимостей сохранены в `third_party/codec-http-notices` для development packaging.

[Свидетельство проверок](compression-http-patch-evidence.json) содержит полный MVS delta, h1/go.mod sums, actual test output и source hashes. Это проверка совместимости и выбора опубликованных исправленных версий, а не полный security PASS. Исторические captures [318aace](mcnd-pion3-318aace-20261103/README.md) не переименованы в результаты новой сборки. Интегрированный релиз требует новых captures и оставшихся финансовых проверок изолированных ERC1155 positions; OpenPGP и другие независимые gates остаются открытыми.
