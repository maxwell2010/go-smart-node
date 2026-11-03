# Заголовки жизненного цикла MasterValidator

`EVMValidatorHeader` хранит только происхождение и состояние валидатора: Master/validator по 20 байт, точный native operator, сырой status, paused, полный uint256 penalty count, hash последнего удостоверенного metadata payload и высоты. Монетных остатков, rewards и самостоятельной копии voting power здесь нет.

Для закреплённого нового контрактного профиля enum **Deleted=0, Candidate=1, Approved=2**. `SourceEligible` означает только `(Candidate || Approved) && !paused`, как действующий `isActive`. Регистрация, валидный уникальный immutable consensus key, нативные Online/Jailed, собственный стейк/минимумы, эффективная мощность и итоговый непустой Comet set проверяются отдельно. Header и его индекс не разрешают финансовую активацию.

`getValidatorState(address)` нового Master возвращает ровно три статических ABI-слова: status/paused/penaltyCount. История штрафов не копируется. JSON metadata в контракте вообще не сохраняется: native metadata можно создать или обновить только из удостоверенного `ValidatorMetaUpdated`, где indexed validator является субъектом; `msg.From` не подменяет его. `LastMetaHash` - ссылка на этот payload, не самостоятельное доказательство подлинности события.

## Хранение и API

- `0x57` сохраняет единственную привязку Master для профиля. Пустая инициализированная коллекция не содержит регистрации/разрешения; первый удостоверенный header закрепляет Master. Runtime-смена Master этим API запрещена и требует явной миграции.
- `0x58` - source row по Master/validator; `0x59` - обратная ссылка native operator; `0x5A` - только source-eligibility.
- `GetEVMValidatorHeader`, `GetEVMValidatorHeaderByOperator` проверяют каноническую запись, домен, Master, индексы и наличие полноценной native metadata/consensus identity.
- `ApplyValidatorHeaderBatch` принимает до 64 Expected/Next изменений, сериализует независимые данные, проверяет CAS и неизменяемые идентификаторы и атомарно пишет затронутые строки/индексы. Обхода всего ledger в горячем пути нет. Точное уже достигнутое состояние не переписывается.
- Удалённый валидатор сохраняется как status=Deleted tombstone. `GetSourceEligibleValidatorHeadersPage` использует полный эксклюзивный validator20 cursor, предел 256 и возвращает только source-предикат.

Native metadata применяется вместе с header в общем внешнем SDK cache. Сам HeaderBatch не меняет native Online/Jailed/RS/power. Настоящий контроллер обязан при штрафе/паузе сразу обнулить эффективную native/Gov мощность, сохранив финансовые позиции для ленивого сжигания. Unpause требует полного catchup по удостоверенному append count, новой net-valuation и непустого допустимого итогового набора. Значения cursor из schema сами по себе не доказывают отсутствие pending penalties.

## Genesis, restart и пределы проверок

Если headers присутствуют, genesis требует полное покрытие native validators и один Master. Импорт/экспорт сверяют точные обратные/eligibility индексы. Старые изолированные schema fixtures могут не содержать headers; это не разрешение для production controller пропускать обязательные source proofs. Его constructor/Init должен безусловно удостоверять реальный настроенный Master, домен, код и полное покрытие существующей native metadata.

Тесты проверяют raw enum/full uint256, неизменность RS/rewards, точечный batch без iterator scan, CAS/replay, Candidate/Approved/Deleted/paused, paginated cursor, откат поздней записи и genesis, orphan/corruption errors, а также настоящий commit/close/reopen LevelDB/IAVL. Протокольный финансовый контроллер и consensus/Gov activation этим хранилищем не устанавливаются.
