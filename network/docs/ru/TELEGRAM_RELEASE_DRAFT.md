# Черновик Telegram-поста

Текст для MintCandy Node v1.1.5. [Постер](media/mintcandy-network-release-cover.png).

---

**MintCandy Node v1.1.5**

Опубликовали ноду для отдельной MintCandy Network на Cosmos SDK, CometBFT,
EVM и IBC. Linux-бинарник можно установить одной командой без Go.

Нативная монета - **MintCandy (MSP)**. Её ERC-20 обёртка - **WMSP**.
В этой версии даже пустой шаблон новой ноды сразу показывает MSP.

Стейкинг поддерживает обычные и hold-позиции, а также изолированные
NFT-резервы. Награды выплачиваются из комиссий MSP без эмиссии. Для оплаты
поддерживаемых операций можно использовать DRC20 с публичным резервом.

На изолированных стендах проверили до 1 000 делегаций с настоящей EVM,
десять сценариев IBC с Hermes, перезапуск через systemd и восстановление
ноды из снапшота. У архива есть SHA256, SBOM и аттестация GitHub.

Публичный запуск MintCandy Network состоится после согласования genesis,
операторов и адресов подключения. Мост к Decimal пока на стадии прототипа.

[Релиз v1.1.5](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.1.5)
 · [Установка на русском](https://github.com/maxwell2010/go-smart-node/releases/download/v1.1.5/INSTALL_RU.md)
 · [Форк на GitHub](https://github.com/maxwell2010/go-smart-node).
