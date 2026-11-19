# Версионный выпуск программы и проверка издателя

Это процедура **software-only** выпуска `mcnd`. Она не утверждает публичный
genesis, число валидаторов, распределение монет, пиры или запуск сети.
Наличие процедуры в исходниках не означает, что выпуск уже опубликован
или что все [release gates](../../../docs/RELEASE_STATUS.md) пройдены.

## Что готовит workflow

Переиспользуемый `.github/workflows/software-release.yml` принимает
`release_version`. Его вызывает уже зарегистрированный
`modern-network-development.yml`, когда указан непустой
`software_release_version`, например `1.0.0`. Пустая строка сохраняет
обычный development-сценарий. Допускается только канонический SemVer `X.Y.Z` без
префикса `v`, ведущих нулей, prerelease и build metadata. Workflow разрешён
в `maxwell2010/go-smart-node` на ветке
`feat/native-duplicate-vote-master-20261112` или `release/**`.
Запуск из PR или другого репозитория не предусмотрен. Пример вызова после
согласования конечной ревизии и проверок:

```sh
gh workflow run modern-network-development.yml --repo maxwell2010/go-smart-node --ref feat/native-duplicate-vote-master-20261112 -f software_release_version=1.0.0
```

Этот вызов не создаёт тег или GitHub Release. Запуск использует SHA выбранной
ветки на момент dispatch. Перед выполнением сверяйте его с согласованным SHA.

Сборка использует точный SHA самого запуска, Go 1.26.11, обычные production
build tags, CGO=1 и проверенный состав исходников. В `mcnd version` задаются
одинаковые `buildCommit` и согласованная версия. Default локальной сборки
`0.0.0-dev` не меняется. Отдельный режим упаковки `--release-version` требует
совпадения фактической CLI-версии, source commit, Go metadata и source inventory.

Архив `mcnd-X.Y.Z-linux-amd64.tar.gz` включает бинарник, манифест SHA256,
SBOM, сведения о сборке и лицензии. Он **не содержит genesis и ключей**.
`MANIFEST.json` помечен `status=software-release`,
`includesGenesis=false`, `networkLaunchApproved=false`. Старые
development-архивы и подписанные тестовые launch-пакеты остаются другими
форматами и не переименовываются в стабильный выпуск.

Перед упаковкой проверяются зависимости, owned source, Python-инструменты,
весь Go-модуль, подписанные App/EVM token-маршруты, 10/100/1000 делегаций,
governance и IBC harness. Для точного бинарника выполняются source/binary
security gates и проверка SBOM. Это не заменяет отдельные daemon/Hermes,
systemd, snapshot и согласованные upgrade/rollback проверки той же ревизии.

## Откуда берётся подпись

После успеха сборки отдельный GitHub-hosted job скачивает **артефакт того же
запуска**, сверяет его SHA256, source/version и внутренний манифест, затем
создаёт Sigstore-аттестацию через GitHub OIDC. Используется закреплённый
`actions/attest` v4.2.2. Постоянный пользовательский приватный GPG-ключ
для этого не нужен. Права записи ограничены `id-token` и `attestations`;
job не получает право создавать GitHub Releases или теги.
[Официальная модель аттестаций](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations).

Проверяемая идентичность издателя - GitHub-репозиторий, конкретный workflow,
его ref и source commit. Она не удостоверяет решение о запуске публичной сети,
отсутствие всех ошибок, экономическую доходность или юридическое лицо.
Одноразовый OpenPGP-ключ старого CI launch-пакета остаётся тестовым ключом.

Workflow сохраняет сам software-архив, отчёты и `provenance.sigstore.json`
разными Actions-артефактами на 30 дней. Это ещё не постоянная релизная
публикация. Создание тега и GitHub Release выполняется отдельно после
сверки конечных проверок; данный workflow их не создаёт.

## Проверка скачанного архива

Получите через доверенный канал полный `SOURCE_SHA`, ref запуска `SOURCE_REF`
и версию `RELEASE_VERSION`. Не подставляйте их только из скачанного манифеста.
В этой схеме caller и reusable workflow берутся из одного commit, поэтому
SHA подписывающего workflow равен SHA исходников. Signer identity должна
указывать именно вызываемый `software-release.yml`, а не caller
`modern-network-development.yml`. Итоговый job проверяет это по настоящему
bundle; несовпадение должно завершить проверку ошибкой, а не ослабить политику.

Нужны GitHub CLI с поддержкой приведённых параметров и Python >=3.11.
Параметры сверены с `gh 2.94.0`; нужен доступ к GitHub и Sigstore trust roots.
[Справочник проверки](https://cli.github.com/manual/gh_attestation_verify).

```sh
set -eu
: "${ARCHIVE:?Укажите локальный архив}" "${SOURCE_SHA:?Укажите полный SHA}" "${SOURCE_REF:?Укажите refs/heads/... запуска}" "${RELEASE_VERSION:?Укажите X.Y.Z}"
gh attestation verify "$ARCHIVE" \
  --repo maxwell2010/go-smart-node \
  --signer-workflow maxwell2010/go-smart-node/.github/workflows/software-release.yml \
  --source-ref "$SOURCE_REF" --source-digest "$SOURCE_SHA" \
  --signer-digest "$SOURCE_SHA" --deny-self-hosted-runners
```

Для проверки сохранённого bundle добавьте
`--bundle /absolute/path/provenance.sigstore.json`. Сам bundle не заменяет
доверенные source/ref/workflow и корни проверки. После успеха аттестации
используйте инструмент из чистого checkout того же `SOURCE_SHA`:

```sh
set -eu
test "$(git rev-parse HEAD)" = "$SOURCE_SHA"
git diff --quiet
git diff --cached --quiet
python3 -B network/tools/verify_software_package.py \
  --archive "$ARCHIVE" --revision "$SOURCE_SHA" \
  --release-version "$RELEASE_VERSION"
```

Инструмент читает ограниченный USTAR-формат без распаковки и без запуска
бинарника: отклоняет ссылки, повторные/лишние файлы, genesis, приватные ключи,
несовпадение SHA256, версии, provenance и SBOM. Его проверка внутреннего
состава сама по себе не подтверждает издателя - для этого нужен предыдущий
шаг GitHub OIDC.

После проверки ставьте бинарник под новым версионным именем и сохраняйте
старый. Установка не должна создавать или менять home действующих нод.
Для новой сети всё равно нужен отдельно утверждённый genesis и параметры
подключения. [Руководство оператора](OPERATOR_LAUNCH.md),
[синхронизация и история](SYNCHRONIZATION_AND_STORAGE.md).
