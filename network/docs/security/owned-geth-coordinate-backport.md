# Объявленный source backport GO-2026-4507

Вместо изменения module cache в `network/third_party/cosmos-geth` сохранено полное дерево `github.com/cosmos/go-ethereum v1.16.2-cosmos-1` и применён точный upstream fix [895a8597cb16c02203e38707ed2d1da5c500fe60](https://github.com/ethereum/go-ethereum/commit/895a8597cb16c02203e38707ed2d1da5c500fe60). Это узкое исправление в разработке отдельной сети, не сертификат безопасности всего geth и не обновление Decimal mainnet.

## Происхождение и объём

- Канонический Cosmos commit: `9a3ffad54df1d51db495036a29bf5799be36b4e2`.
- Module checksum: `h1:QIaIS6HIdPSBdTvpFhxswhMLUJgcr4irbd2o9ZKldAI=`.
- SHA256 исходного module zip: `70a65b51ae33adf55f6a14de112caebdde2e4d2c69a42472359392ef02797713`.
- Сохранены все 2261 канонических файла; изменены ровно три файла upstream patch. `go.mod` и `go.sum` самого geth не изменены.
- Локальный label: `v1.16.2-cosmos-1-mintcandy.1`. Он не обозначает опубликованный официальный tag.
- Оригинальный patch сохранён в `scripts/patches/geth-GO-2026-4507.patch`, SHA256 `bfcb148b6dbab007f3a583019590ce43e84d76ee253fc9fdb89b20121e9800e1`.

Полный перечень оригинальных и изменённых SHA256, h1, лицензий и fix commit содержится в `third_party/cosmos-geth/UPSTREAM.json`. `scripts/verify_owned_geth.py` закрепляет исходный inventory и все три исправленных SHA256. Проверка отклоняет частичный patch без CGo-файла, подменённый canonical inventory, недостающие или незаявленные файлы.

[GO-2026-4507](https://pkg.go.dev/vuln/GO-2026-4507) указывает upstream исправление начиная с geth 1.16.9. На момент выбора официальные Cosmos tags и module proxy содержали 1.16.2-cosmos-1 и более новую minor-линию 1.17.2-cosmos-0/1, но не patch tag 1.16.9 для текущей линии. Совместимость 1.17.2 с используемым Cosmos EVM 0.6.3 здесь не установлена. Более крупный переход требует отдельной проверки API и приложения; он не подменяется этим backport.

## Что изменено

| Файл | Изменение |
| --- | --- |
| `crypto/secp256k1/curve.go` | `BitCurve.IsOnCurve` отклоняет координаты, достигающие или превышающие модуль поля. |
| `crypto/secp256k1/ext.h` | CGo-путь проверяет результат преобразования обеих координат в элементы поля и возвращает отказ при ошибке. |
| `crypto/signature_nocgo.go` | `btCurve.IsOnCurve` выполняет ту же проверку диапазона перед вызовом существующей реализации без CGo. |

Публичные сигнатуры методов сохранены. Поведение для недопустимых координат намеренно ужесточено. Точное upstream исправление не расширялось дополнительными изменениями математики, EVM или финансовых keepers.

Требуемый импорт `github.com/ethereum/go-ethereum` теперь заменён на `./third_party/cosmos-geth`. Проверка всех 767 выбранных внешних module records относительно snapshot b9636dc подтверждает, что изменилось только effective replacement geth; остальные выбранные версии и h1 сохранились. Старые исходные module cache и канонический fork не патчились.

## Проверки

Обычные локальные тесты `securitytests.TestOwnedGethCoordinateValidation` и `TestOwnedGethSignatureRoundTrip` прошли с `CGO_ENABLED=1` и `CGO_ENABLED=0`. Они используют искусственные значения: генератор кривой, удвоение точки, границы поля, обычные подписи и преобразования публичного ключа. Сетевые payloads, специальные контексты EVM и отложенные exploit/bypass-проверки не используются.

Прошли также upstream `crypto` и `common/math` в обоих режимах, а `crypto/secp256k1` - в CGo-сборке. Команды выполняются из `network` с реальным local replacement:

```bash
python3 scripts/verify_owned_geth.py
python3 -m unittest discover -s scripts -p test_verify_owned_geth.py
CGO_ENABLED=1 go test -mod=readonly -p 1 -run TestOwnedGeth ./securitytests
CGO_ENABLED=0 go test -mod=readonly -p 1 -run TestOwnedGeth ./securitytests
CGO_ENABLED=1 go test -mod=readonly -p 1 github.com/ethereum/go-ethereum/crypto github.com/ethereum/go-ethereum/crypto/secp256k1 github.com/ethereum/go-ethereum/common/math
CGO_ENABLED=0 go test -mod=readonly -p 1 github.com/ethereum/go-ethereum/crypto github.com/ethereum/go-ethereum/common/math
```

## Лицензии и упаковка

Оригинальные `COPYING`, `COPYING.LESSER`, вложенные notices и авторские заголовки сохранены. Они не заменены лицензией основного репозитория. Development packager включает исходные `LICENSE*`, `COPYING*` и `NOTICE*` рекурсивно, с исходными путями и SHA256; для этого добавлена проверка различающихся GPL/LGPL notices и вложенного notice в архиве. Фактическая сборка и source-bound development archive должны иметь отдельное свидетельство с точным commit и hash бинарника.

Для чистого source commit `b03012ff052026d867da03fc9a44fe06cb9f4a1e` собран настоящий `mcnd` с Go 1.26.10, Linux amd64 и CGO/GCC. SHA256 бинарника: `2d5ed4b419ec712776960cf2e052379b2e2c3910fee83f8d803b73c822f89edf`. Получен development archive размером 75 943 390 байт, SHA256 `77c9855427e91d7ec62c1a65efef413ca96072d4bd67ef14789b73305a8dbcfb`. Все file hashes архива проверены; девять license/notice-файлов geth, включая оба `COPYING`, совпадают с исходниками побайтно. Точный commit подтверждён реальной командой версии с отдельным временным home. [Свидетельство упаковки](owned-geth-coordinate-package-proof.json) сохраняет имена и SHA256 всех этих notices. Это development artifact указанного snapshot, не опубликованный стабильный релиз.

## Оставшиеся ограничения

Этот patch закрывает конкретное изменение исходника для GO-2026-4507. Он не означает закрытие ECIES и остальных upstream geth advisories, Pion DTLS v2 или OpenPGP. Проверка исходников доказывает наличие объявленного patch, а не отсутствие других уязвимостей.

[Предыдущие source/binary captures b9636dc](mcnd-b9636dc-20261103/README.md) остаются историческими и сохраняют оба FAIL. Они не относятся к новой замене. После интеграции нужны новая сборка и отдельные source/binary captures; local replacement снова требует ручного сопоставления upstream advisories и полного source provenance. Обычные тесты этого backport не сертифицируют работоспособность или безопасность всего интегрированного релиза.
