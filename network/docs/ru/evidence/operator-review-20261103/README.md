# Проверка operator docs и retention 3 ноября 2026 года

Изменение helper: `342125cd37f335a36c3ea949cdbad9120cc87489`.
Review base: `645d3c5a72408856b087d4b3c4a4c45ac7f22966`.

Linux ordinary tests: 5 новых safety tests и 11 operator regressions прошли.
Проверялись ненулевое signing-state без DB, неизвестные data artifacts,
сохранение нулевого init state, preexisting staging, symlink и file modes.
В Windows те же safety tests прошли с пропуском Linux symlink/mode случая.

Оба профиля применены на двух новых конфигурационных fixtures, созданных
настоящим бинарником исходного commit264303b; его точный SHA записан в JSON.
Проверены dry-run, исходные backup bytes и эффективные TOML-параметры.
Исполненный helper побайтно совпадал с указанным commit. Бинарник264303b
не переименовывается в binary текущей integration: профили используют те же
SDK53/Comet38 поля, но это не новая runtime/финансовая проверка.

Нода не стартовала, порты не открывались, транзакции не отправлялись.
Существующие home, keys, data и службы не использовались. Новые fixtures
находятся отдельно в C:/MintCandyBuild; их содержимое с ключами не публикуется.
В evidence сохранены только не секретные настройки, filenames и результаты.

Проверка документации: 8 shell blocks прошли bash -n, 11 относительных ссылок
разрешаются. Это syntax/reference check, не выполнение apt/install команд.
Официальный Go download index подтвердил опубликованный SHA Go1.26.10 amd64.
Deployment pin/environment policy и actual Development CI принадлежат отдельным
проверкам интеграционной ветки, а не этим коротким fixture tests.

Не выполнены: миллион блоков pruning, измерение уменьшения DB после compaction,
нагрузочный тест, snapshot/state-sync restoration новой финансовой модели,
production hardware qualification. Никакого гарантированного лимита GiB нет.
