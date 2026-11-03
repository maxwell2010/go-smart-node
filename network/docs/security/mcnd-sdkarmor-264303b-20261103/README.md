# SDK Armor: результаты отдельной сборки 264303b

Исходный commit: `264303bd5ca8bd8107ba219c77b60c9c14818f1c`.
Это отдельный dependency snapshot после исправлений geth, Pion,
compress/x-net и объявленного SDK Armor patch. Это не итоговая
финансовая сборка и не подтверждение готовности полноценного релиза.

Полные source/binary JSON captures сохранены в gzip без исключений.
Manifest связывает каждый raw capture с commit, argv, Go toolchain,
временем завершения, размером и SHA-256. Отдельные gate/summary/levels
файлы различают symbol, package и module findings. Scanner exit 0
сам по себе не означает отсутствие findings. Результаты других commit
остаются историческими и не переименованы в эту сборку.

В обоих captures symbol advisories: 0. Сохранены module-only
GO-2025-3442 и GO-2026-5932, без package/symbol finding. Их отдельный
разбор находится в `remaining-module-disposition.json`. Изменения
compress 1.18.7 и x/net 0.62.0 устранили прежние version-range findings
этого snapshot; это не результат проверки отложенных особых сценариев.

SDK 0.53.8 заменён объявленной копией v0.53.8-mintcandy.1.
Поэтому автоматическое version coverage SDK ограничено, как и у
локальных owned EVM/geth. `canonical-sdk-osv-*` сохраняют отдельный
официальный запрос канонической версии. Его advisories сравниваются
с фактическим списком compiled packages без скрытия результатов.
Отсутствие старого OpenPGP в compiled imports и успешные format/CRC
fixtures подтверждают конкретную миграцию ASCII Armor, а не закрытие
всей поверхности SDK, geth, EVM или новой финансовой модели.

Обычные тесты включают канонические синтетические Info/PubKey/private-key
fixtures, Argon2 и legacy bcrypt SDK tests, keyring, подписи CGO=1 и
CGO=0, App/CLI. Сетевые транзакции не отправлялись. Умышленно не
выполнялись отложенные специальные execution-context сценарии.

`package-proof.json` проверяет реальный development archive и все
его MANIFEST hashes, включая исходные Apache/BSD notices SDK и
поддерживаемой библиотеки. Бинарник и архив остаются локальными
артефактами; это не стабильная публикация и не полный SBOM.

Политика и точный объём патча: [SDK source patch](../sdk-armor-source-patch.md).
`sdk-source.patch` показывает все три изменения канонических файлов и
три добавленных файла относительно проверенной копии SDK 0.53.8.
