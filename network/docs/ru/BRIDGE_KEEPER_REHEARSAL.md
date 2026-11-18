# Keeper моста и London EVM: проверка прототипа

Статус: внутренний прототип отдельной сети. Эти пакеты ещё не подключены
к message server, genesis и банковским полномочиям работающего daemon.
Контракт escrow поддерживает только депозит, не имеет выхода средств и
не должен размещаться с реальными активами.

На ревизии `248e97f` успешно выполнен
[Linux CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44266044517).
На той же ревизии прошёл и
[общий module CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44266535485).
[Сохранённая сводка](evidence/bridge-keeper-248e97f-20261118/summary.json)
содержит границы проверки, состав пакетов и замеры газа. Сборки и EVM-тесты
выполнялись на GitHub runner, работающие ноды не изменялись.

Следующий [account/codehash CI](https://github.com/maxwell2010/go-smart-node/actions/runs/44273486176)
прошёл на `b1919be`: тот же bridge/London scope дополнен аутентификацией
кода исходного escrow.
[Сводка account proof](evidence/bridge-account-b1919be-20261118/summary.json).
Общий module CI выше относится к `248e97f`; повторная проверка `b1919be`
охватывает все bridge-пакеты и commit probe, а не весь daemon/process suite.

## Что реализовано

| Пакет | Ответственность |
| --- | --- |
| `bridge/client` | Хранит policy, bootstrap anchor и последний проверенный LightBlock в SDK KV |
| `bridge/finality` | Проверяет bootstrap и обновления заголовков с подписями и сроками доверия |
| `bridge/protocol` | Задаёт точную запись депозита, operation ID и mapping storage slot |
| `bridge/storage` | Проверяет точное ICS-23 membership под корнем заголовка H+1 |
| `bridge/ledger` | Выпускает отдельный voucher и сохраняет durable replay marker |
| `bridge/inbound` | Соединяет проверку клиентского состояния, proof и выплату в одной SDK cache transaction |
| `bridge/contracts` | Содержит депозитный Solidity-прототип и отдельные London EVM-тесты |

Клиент и маршрут инициализируются только внутренними доверенными API будущего
genesis/module. Повторная инициализация существующей записи запрещена.
Permissionless bootstrap и загрузка checkpoint из пользовательского запроса
не реализованы.

## Привязка перевода к источнику

Маршрут закрепляет клиент Decimal, source chain ID `decimal_75-1`, EVM ID 75,
адрес и ожидаемый hash кода escrow, собственные Cosmos/EVM chain ID, destination bridge ID,
начальную высоту доказательства и максимальный возраст заголовка.
Начиная с `b1919be`, каждый входящий перевод требует два доказательства под
одним AppHash: account из `acc` и депозит из `evm`. Account key равен
`0x01 || escrow20`. Сначала проверяется membership сырых protobuf Any bytes,
затем фиксированная схема `/ethermint.types.v1.EthAccount`, адрес BaseAccount
с префиксом `d0` и точное совпадение 32-байтового code hash с route.
JSON-RPC `codeHash` не принимается как доказательство.

Hash закрепляется доверенной процедурой регистрации по фактическому
развёрнутому runtime с заполненными Solidity immutables. Совпадение hash
не доказывает отсутствие proxy/delegatecall или правильность финансовой логики;
этот профиль требует отдельно проверенного неизменяемого nonproxy escrow.
Старые development route records без hash отклоняются; автоматической
перезаписи или миграции таких записей нет.

Депозит кодируется как Solidity `abi.encode` из девяти слов, всего 288 байт:

```text
domain, source EVM ID, destination EVM ID, source escrow,
destination bridge ID, sender, recipient, amount, nonce
```

Domain равен `keccak256("MINTCANDY_DECIMAL_BRIDGE_V1_DEPOSIT")`.
Operation ID является Keccak-256 этой записи. Контракт сохраняет ID как
значение `mapping(bytes32 => bytes32) commitments` в slot 0. Go проверяет
ровно этот вычисленный slot, ID и source escrow. Nonce выделяется отдельно
для каждого отправителя, нулевой nonce допустим, переполнение отклоняется.

Изменение получателя, суммы, nonce, sender или любого домена меняет ID.
Доказательство для прежней записи не подтверждает изменённый перевод.

## Атомарное исполнение

`ReceiveWithUpdate` выполняет в одном внешнем `CacheContext`:

1. Обновление клиента из заголовка, проверенного относительно сохранённого состояния.
2. Проверку зарегистрированного маршрута и свежести source header.
3. Проверку account/code hash и точного source storage commitment под одним последним клиентским корнем.
4. Резервирование replay marker в дочерней cache и банковские mint/send.
5. Проверку результата typed event и запись cache только после полного успеха.

Корень для membership берётся из `bridge/client`, а не из параметров relayer.
При ошибке proof или выплаты не сохраняются ни новый checkpoint, ни voucher,
ни replay marker или события. Успешный повтор создаёт один voucher-перевод;
последующие повторы ID отклоняются, включая вызов через bank hook.

Входящий DEL Decimal представлен отдельным denom **`bridge/decimal/del`**.
Нативный `del` нашей сети этим путём не выпускается и не становится
взаимозаменяемым с активом Decimal. Проверки real SDK bank сверяют баланс
получателя, supply, остаток module account и неизменность нативного DEL.

Клиент хранит только последний корень. После обновления клиента relayer
должен получить proof для актуального состояния. Исторические корни и
неограниченная история клиентских записей не сохраняются. Перед полной
реализацией pending/reverse-потоков это правило нужно согласовать с
неизменяемостью исходных commitment и процедурами восстановления.

## Что проверено в CI

- Настоящие Ed25519-подписи, policy и срок доверия клиента.
- Настоящие IAVL/multistore proofs из отдельного SDK source store.
- Обновление клиента и последующий отказ proof без изменения сохранённого checkpoint.
- Ошибки после реальных банковских mint/send, ошибки store/event и полный откат.
- Отказ повторного входа и единственная выплата после retry.
- Внешний cache rollback, commit, закрытие LevelDB и повторное открытие.
- Сохранность клиентского состояния, маршрута, балансов и replay после перезапуска.
- Подмены сетей, escrow, bridge ID, суммы, sender, recipient, nonce и высоты.
- Подтверждённый Merkle-proof неизвестного runtime не разрешает mint.
- Повреждённые account bytes, proof и protobuf, разные account/storage roots отклоняются.
- Ошибка account proof откатывает checkpoint, balances, supply, replay marker и события; исправленный retry проходит.
- Отклонение устаревшего заголовка даже до истечения общего trusting period.
- Реальная компиляция Solidity 0.8.20 для London и исполнение bytecode в geth EVM.

В первом прогоне тесты перезапуска выявили ошибку стенда: несколько IAVL
stores подключались к общей базе без индивидуальных префиксов. В fixture
исправлена передача DB: CMS теперь самостоятельно выделяет namespace каждому
store. Это исправление тестовой конфигурации; данные работающих нод не менялись.

## Газ изолированного London-стенда

| Операция | Gas с intrinsic |
| --- | ---: |
| Развёртывание депозитного escrow | 326 094 |
| Первый депозит | 69 236 |
| Развёртывание standalone SP1 v6.0.0 verifier | 1 534 195 |
| Проверка известного upstream SP1 proof | 246 757 |

Лимит тестовой транзакции - 10 000 000 gas. Проверки исполнялись в памяти
geth с London rules. Они не включают Decimal ante handler, EVM hooks,
настройки комиссии или банковский учёт настоящей Decimal-ноды. Повторные
вызовы в том же runtime не считаются benchmark отдельных mainnet-транзакций.

## Что доказывает SP1-тест

Использованы неизменённые upstream-исходники и подходящий proof-вектор
**v6.0.0** из закреплённой ревизии
`d3629729c3216eb51bd4859d027a8eb729399fa4`. SHA256, Git blob IDs, provenance
вектора и MIT notices хранятся в `contracts/testdata/sp1-v6.0.0/`.
Проверены успешный proof и отказы при подмене proof, public values,
program vkey, verifier selector и при усечённых данных.

Это общий upstream sample, не proof заголовка Tendermint, состояния Decimal
или сжигания bridge voucher. Он подтверждает совместимость этого standalone
verifier с London в проверенном стенде. Он не выбирает production prover.
В закреплённом upstream нет соответствующего положительного fixture v6.1.0;
успех v6.1.0 и совместимость всего IBC Solidity не заявляются.

## До открытия транзакционных маршрутов

Остаются обязательными:

1. Аудит фактического runtime исходного escrow, immutable/nonproxy policy и доверенная регистрация его hash.
2. SDK gas metering вычислительной стоимости заголовков и proofs.
3. Genesis/governance authority, message handlers и module-account permissions.
4. Согласованные program vkey/public values и настоящие Tendermint/state proof vectors.
5. Обратные burn/release, подтверждение выплаты и безопасная обработка timeout.
6. Полный сценарий на двух daemon с source EVM receipt, commitment proof и сверкой резервов.
7. Перезапуск, миграция и остановка каждой сети во всех pending-состояниях.

Успешные unit/EVM проверки не заменяют эти этапы и не меняют
[статус релиза](../../../docs/RELEASE_STATUS.md).
