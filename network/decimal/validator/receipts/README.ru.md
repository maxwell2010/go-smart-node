# Захват нативных stake bindings для общего receipt-конвейера

`NewStakeCaptureSession(bindings, authenticator)` создаёт одноразовую сессию для одного исполнения/плана. Она реализует `nft/receipts.StakeCapture`: `Capture` выбирает точные **уже сохранённые** custody subID действующих или frozen-позиций, `ValidateCapture` проверяет их свежесть до нативных записей. Сессия ничего не записывает и не устанавливает EVM hooks в App.

`StakeSourceAuthenticator.AuthenticateStakeSource` обязан удостоверять текущий независимый профиль: домен App, утверждённые compiler-derived runtime/proxy/implementation/library, роли registry и governance-owner. Возвращаемый fingerprint связывает этот scope. Наблюдённый codehash сам по себе не является разрешением. Проверка NFT lineage, фактического обеспечения, pending penalties и нативной мощности остаётся отдельной обязанностью финансовых planner/контроллера. Unit-тесты используют явный stub этого authenticator и не доказывают подлинность EVM-профиля.

Входные `Transfer` уже должны быть декодированы и удостоверены NFT planner. Проверяются ограниченный receipt, положение/контракт каждого движения, уникальность MoveRef, домен и точное соответствие custody. Отображение source stake/frozen index сохраняет uint256, включая frozen index 0. Выбор произвольных минимальных escrow ID отсутствует. Новый входящий origin, связь deposit -> burn и родословная новой квитанции принадлежат NFT financial planner; эта сессия их не восстанавливает. Если действующей source binding нет, сессия не изобретает selection: NFT planner обязан отдельно доказать новую/transient-позицию либо отклонить escrow-перемещение.

Непустой capture связывает bounded native binding token, digest receipt, профиль и точные selections. Перед возвратом и сохранением создаются независимые копии. Повторный Capture на той же сессии запрещён; ValidateCapture можно вызывать до записей. Изменение binding revision/снимка, заголовка/TxBytes, профиля или selections отклоняется. Постоянного processed-log журнала и глобальной карты сессий нет.

Если **уже декодированных NFT-перемещений нет**, этот компонент возвращает пустой capture без KV/EVM reads и без расхода SDK gas. Это локальный no-op выбора custody; другие компоненты всё ещё должны удостоверить монетные/валидаторские события. Это не глобальное правило пропускать zero-log транзакции. Неподдержанные SDK/IBC entrypoints остаются отклонёнными.

Координатор должен проверить все capture/планы на старом нативном состоянии, затем применить asset -> NFT -> validator в одном дочернем SDK cache. Validator stage использует готовые exact NFT Movements/PositionLineage; собственный origin allocator или второй NFT ledger запрещён. При ошибке любой стадии общий cache не коммитится. Финансовая активация и настоящий post-transaction SourceAuthenticator требуют отдельной интеграционной проверки.

Проверка из `network`:

```sh
go test -mod=readonly -p 1 -tags sdk53 -count=1 ./decimal/validator/... ./decimal/nft/receipts
```

Тесты используют настоящий SDK keeper/cache: exact unit 73 вместо lowest 1, active/frozen source, read-only capture, подмену selections, stale scope/revision/profile, full receipt digest, отсутствие придуманного selection для неизвестного escrow и no-op с зависимостью, которая паникует при любом чтении. Это не подписанный EVM-тест полного финансового конвейера.
