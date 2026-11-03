# Опубликованный development binary 8cf619d: проверка CLI

Source/binary pin: `8cf619da1c77ee5efffc80a678272010fd2263dd`.
[Development run](https://github.com/maxwell2010/go-smart-node/actions/runs/41608914841),
artifact ID 13218266382. Проверен готовый CI-артефакт; повторной source-сборки
не было. Это не стабильный релиз и не финансовая активация.

GitHub ZIP digest `1a2d0b48398dbc7a8e3d408229957751835de8cea6a7afd83208f0a5077b3474`
совпал с загруженным ZIP. Tar sidecar и все hashes MANIFEST проверены.
Binary SHA: `d70f04927738a5a7459fb5607ed8b28daafef260f8ca6773eeece817456fb099`.
Все 1890 production input hashes сверены с Git blobs именно 8cf619d,
не с изменяемым HEAD. Полные actual ELF Go metadata совпали с BUILD_INFO;
VERIFIED_BUILD совпал с их структурным разбором: 247 dependency identities,
Go 1.26.10, default Linux amd64/v1, CGO 1, trimpath, clean VCS 8cf619d.
Три opt-in artifact tests прошли за 9.243 s. Метод добавлен отдельным audit commit
`da4506d7e6a418e837ea09d8227958a29c8568dc`, он не приписывается source8cf.

На двух новых nativeLinux /tmp fixtures реально выполнены version и
completion: новый home не создаётся. С отдельно созданным некорректным
client.toml обе команды успешны и не читают его для исправления/инициализации:
файл побайтно прежний, дополнительных файлов нет. `snapshots list` без
допустимого domain отклонён до создания home/snapshot store. Snapshot mutation,
start, RPC requests и транзакции не выполнялись. Временные fixtures удалены
контекстом TemporaryDirectory; действующие ноды/ключи/службы не затрагивались.

Это подтверждает исправление обнаруженных в 88 CLI-регрессий на готовом
новом бинарнике. Старые результаты 88, его создание client.toml и различающиеся
SHA исходной/локальной сборок остаются отдельным историческим evidence.
Не проверялись новый genesis финансовой сети, полномасштабное snapshot restore,
mainnet replay, нагрузка/hardware minima, полная byte-for-byte reproducibility
или ещё не опубликованные изменения следующего commit.
