# Установка и упаковка development snapshot 88ed536

Проверяемый binary/source commit:
`88ed5365340a6cf22bb757b27a9a9e5eef3dbe28`.
[Development run](https://github.com/maxwell2010/go-smart-node/actions/runs/41601774112),
artifact ID `13215725594`. Исходники отдельно получены из опубликованного
commit в чистый Linux Git checkout. Существующие ноды, data, ключи и сервисы
не использовались. Полная сборка выполнялась один раз, p=1/GOMAXPROCS=2.
Крупные артефакты и Go TMP размещены на C; временные CLI homes - native /tmp.

## Подтверждено

- SHA ZIP совпал с GitHub artifact digest; tar SHA совпал с sidecar.
- Проверены все hashes MANIFEST, точный source commit и mode0755 tar binary.
- Все 1877 tracked production inputs/366 local packages совпали с Git blobs
  указанного commit. CI, local prebuild и local postbuild inventories равны.
- Три opt-in acceptance tests прошли на настоящем скачанном CI-артефакте:
  complete archive/digest, pinned Git inputs и actual ELF Go metadata.
- Документированный путь exact remote fetch + installer --source завершился,
  прошли owned EVM/geth/SDK integrity, module/Pion/armor checks, actual build,
  сравнение inputs до/после и обычная version-команда в disposable path.
- Повторная установка на существующий destination отклонена; SHA файла прежний.
- Упаковка исходным helper88 и новым helper
  `c7b10bd8b0c88f12ce84f41b24f3d9eeb0c1cf31` прошла на тех же source/binary.
  Новый архив содержит VERIFIED_BUILD: 247 точных dependency identities и
  проверенные default build settings. Это новая проверка упаковщика, не новая
  сборка финансового runtime. Все hashes обоих архивов проверены.
- Для CI и installed binary выполнены version/help, init,
  reconcile-native-allocation, validate-genesis, show-node-id,
  `snapshots list --help` и rpc-ibc retention. Никаких start/RPC requests/tx.
  Цепная DB не появилась, signing-state не менялся; новые key files mode0600
  проверены на native Linux filesystem. Права destination на DrvFS не выдаются
  за проверку Unix chmod.

## Найденные ограничения

В исходном88 packaging не сверял настройки/модули переданного ELF с declared
default SOURCE_INPUTS. Реальный CI88 этим настройкам соответствует; проверка
добавлена отдельным commit c7b10bd и проверена на его действительных данных.
В первом опубликованном архиве VERIFIED_BUILD отсутствует - он не
переименовывается в результат нового helper.

Версия88 `version --home NEW` создаёт только config/client.toml. Это не
filesystem-pure команда; первоначальное assertion «home не создаётся» упало
и не объявлено прошедшим. Installer/packager использовали собственные временные
paths, поэтому действующий home не затронут. Также в старом CLI guard было
singular snapshot вместо зарегистрированного snapshots. Export/restore и
другие snapshot mutations не выполнялись. Исправления требуют отдельной
проверки более нового опубликованного binary и не приписываются этому88.

CI binary SHA:
`a5b359c7ec929621cca2858dac52e9bd108ea7f66de35178394c7417058ee9e5`.
Installed binary SHA:
`e2fb07b379e60a82b8310d00c59baae33169d72c1277a538f16128f3dbe46a4c`.
Полные нормализованные Go metadata и source inventories равны, но бинарники
не побайтно идентичны. Оба .comment содержат одинаковую GCC13.3.0 строку:
версия компилятора не выдумывается как объяснение. Точная воспроизводимость
linking/полного build environment этим smoke не установлена. Различие SHA
сохранено; неподдерживаемые пересборки или специальные EVM probes не проводились.

Артефакт development, с ограниченным сроком GitHub хранения. Это не stable
release URL, не финальный vulnerability scan, не performance/hardware минимум,
не mainnet replay, не проверка новой изолированной финансовой модели и не
разрешение её активации. Новая общая сборка должна пройти собственные CI/gates.
