# Native coin state staging

`StateKeeper` is a new-network storage layer, not a legacy database migration.
It keeps a full coin protobuf and a unique contract-address index in one SDK
child transaction. Reads include the stored volume and reserve, and do not use
keeper memory as a financial cache. Contract assignments cannot be changed by
ordinary writes. Storage and typed-event errors propagate and discard the
child transaction, including partially emitted events.

The tests use the actual SDK store and CacheContext. They check discarded and
committed balances, event failure, index-write failure, duplicate assignments
and discarded parameters. They do not prove bank supply or reserve backing.

This keeper is not yet registered in the application. It does not mint coins,
move bank reserves, redeem checks, implement fees or expose transaction/query
routes. An EVM integration must authenticate the system contract, decode its
actual ABI, account for gas and commit an entire receipt batch atomically.
Application genesis must reconcile all exported amounts with bank/EVM state.
