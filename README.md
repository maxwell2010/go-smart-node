# MintCandy Network

MintCandy Network - самостоятельная сеть на базе форка Decimal. Нода майнета называется `mcnd`. Ветка `master` содержит исторический код Decimal. [Исходный код MintCandy](https://github.com/maxwell2010/go-smart-node/tree/codex/mintcandy-native-denoms/network).

| Параметр майнета | Значение |
| --- | --- |
| Chain ID | `mintcandy_9905401-1` |
| EVM chain ID | `9905401` |
| Нативная монета | MintCandy (MSP), 18 знаков |
| Базовый denom | `msp` |

## Установка и синхронизация

Подписанный genesis и bootstrap-пир опубликованы в стартовом выпуске. Проверьте подпись манифеста и контрольную сумму genesis перед подключением. [Стартовый комплект майнета](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.2.0).

Для Linux amd64 доступен пакет 1.2.2 с однострочником установки и проверкой происхождения бинарника. Для работающей ноды на 1.2.1 время перехода определяет принятая голосованием высота. При синхронизации с первого блока используйте версии для соответствующих участков истории. [Пакет 1.2.2](https://github.com/maxwell2010/go-smart-node/releases/tag/v1.2.2) · [Однострочник на русском](https://github.com/maxwell2010/go-smart-node/releases/download/v1.2.2/INSTALL_RU.md) · [Порядок обновления и восстановления](https://github.com/maxwell2010/go-smart-node/blob/22fe8d81692968ad11dab063640159d78573eb6f/network/docs/ru/GOVERNED_UPGRADE_1_2_2.md).

Конфигурация сервера, получение пиров, настройка снапшотов и ограничение роста базы описаны в руководстве оператора. [Документация на русском](https://github.com/maxwell2010/go-smart-node/blob/codex/mintcandy-native-denoms/network/docs/ru/README.md) · [Синхронизация и хранение](https://github.com/maxwell2010/go-smart-node/blob/codex/mintcandy-native-denoms/network/docs/ru/SYNCHRONIZATION_AND_STORAGE.md).
