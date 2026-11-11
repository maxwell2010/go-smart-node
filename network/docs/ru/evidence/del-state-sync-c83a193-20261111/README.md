# Проверка state sync DEL на c83a193

11 ноября 2026 года изолированный Linux CI завершился успешно для коммита
[`c83a1934642e1f00a63f5f54803b7971deb61625`](https://github.com/maxwell2010/go-smart-node/commit/c83a1934642e1f00a63f5f54803b7971deb61625).
[Прогон 43035085651](https://github.com/maxwell2010/go-smart-node/actions/runs/43035085651)
запущен с `scope=process`, `remote_profile=off` и `del_state_sync=true`.
Он также прошёл проверки финансовых genesis, обычного запуска ноды и шесть
сценариев ICS20 с отдельным процессом Hermes.

| Высота | Проверенный результат |
| --- | --- |
| 1902 | Первый блок в истории отдельного RPC-процесса, синхронизированного от genesis. |
| 1920 | Snapshot активной DEL-позиции. Третий процесс восстановил его по P2P. |
| 1922 | Trust height. Источник и RPC-свидетель вернули одинаковые хеши блоков на высотах 1920-1922. |
| 1923 | Восстановленная позиция содержит 4 DEL. Сверены нативные и EVM-записи. |
| 1925 | Нода после state sync воспроизвела запрос на вывод 4 DEL. |
| 1929 | `EndBlock` выплатил 4 DEL. Расход обработки на источнике составил 5 260 959 единиц газа SDK. |
| 1931 | Записи после выплаты и `AppHash` совпали. |

Trust hash на высоте 1922 -
`E9ABAFDED258567555C302725DA76C641E7E2B09B92C0FDFBFD96EE1EC087DBB`.
`AppHash` snapshot на высоте 1920 -
`5395AE1BC66AAF92BFCBBC0D9B6F0913039E4C4643C654CD1AF10F20C8F5B2F6`.
Финальный `AppHash` на высоте 1931 для всех трёх процессов -
`4D536010AC99138E19BD6AAFFB890CE026FB7803BFA3CAF1339FB8A7756A8F57`.
Источник обладал голосующим весом. У RPC-свидетеля и ноды после state sync
голосующий вес равнялся нулю. Все три процесса использовали один CI-runner и
единственного валидатора тестовой цепи.

## Проверка архива

[Архив 13863168554](https://github.com/maxwell2010/go-smart-node/actions/runs/43035085651/artifacts/13863168554)
хранится в GitHub Actions 7 дней. Для скачивания через `gh` нужен доступ к
артефактам прогона. SHA256 архива сверена с метаданными GitHub. Ревизия из
`del-delegation.json` совпала с `manifest.json` и `mcnd-version.json`.
SHA256 бинарника `mcnd` -
`5f5aaab7b5e65af358c1f690016f0f2ef98c416a1bdd702fb5df75b1eb395cbb`.

```bash
gh api repos/maxwell2010/go-smart-node/actions/artifacts/13863168554/zip > process-evidence.zip
printf '%s  %s\n' 'd8b03777d8b4b7e9f546303b86492a5b7158542afd4f1c141ec81e1730af1ffe' process-evidence.zip | sha256sum -c
python3 - <<'PY'
import hashlib
import json
import zipfile

with zipfile.ZipFile("process-evidence.zip") as archive:
    raw = archive.read("del-delegation.json")
    manifest = json.loads(archive.read("manifest.json"))
    version = json.loads(archive.read("mcnd-version.json"))
assert hashlib.sha256(raw).hexdigest() == "f6eab3ca3828a55ddfdcf26772f450ad54158abbaf6eb8c7b098f700a36e9b52"
report = json.loads(raw)
assert report["status"] == "PASS"
assert report["sourceCommit"] == manifest["sourceCommit"] == version["commit"] == "c83a1934642e1f00a63f5f54803b7971deb61625"
assert report["binarySha256"] == manifest["mcndSHA256"] == "5f5aaab7b5e65af358c1f690016f0f2ef98c416a1bdd702fb5df75b1eb395cbb"
sync = report["stateSync"]
assert sync["snapshotHeight"] == 1920 and sync["trustHeight"] == 1922
assert sync["restored"]["activeStakeAtomic"] == "4000000000000000000"
assert sync["pending"]["pendingAtomic"] == "4000000000000000000"
assert sync["replayed"] == report["final"]
assert sync["finalAppHash"] == sync["witnessFinalAppHash"] == "4D536010AC99138E19BD6AAFFB890CE026FB7803BFA3CAF1339FB8A7756A8F57"
print("Проверка отчёта прошла")
PY
```

[Повторный запуск на изолированном GitHub CI](../../../../../scripts/remote/README.ru.md#проверка-state-sync-для-del-на-трёх-процессах).
