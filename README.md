# MintCandy Node

Independent integration fork of Decimal go-smart-node.

**Status: integration development. This is not a stable production IBC release and is not certified for mainnet validators.**

The integration/reliability-ibc branch includes grouped upgrade-replay, auto-unbond and financial-event corrections; bounded updater operations; real EVM fault tests; and ICS-20 wiring guarded by the experimental_ibc build tag and exact isolated chain IDs.

- [Russian installation and isolation guide](docs/INSTALL.ru.md)
- [Новая сеть mcnd: установка, зависимости, синхронизация и ограничение истории](network/docs/ru/README.md)
- [English installation and isolation guide](docs/INSTALL.en.md)
- [Release gates](docs/RELEASE_STATUS.md)
- [Integration history and replay requirements](docs/integration-progress.md)
- [Reliability validation](docs/reliability-validation.md)

An ordinary build does not wire transfer stores/modules. An experimental IBC build refuses production/public-testnet genesis IDs. Do not replace an operating validator binary with either build until the relevant replay, security and release gates pass. Preserve upstream LICENSE and existing source notices.

The separate `network/` module builds the new-network `mcnd` development
candidate with its own modern SDK/EVM/IBC stack. The build-tag statements above
refer to the legacy root module. Its mainnet replay evidence and the separate
network's compatibility checks are documented independently in the release gates.
