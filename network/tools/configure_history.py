"""Apply a development history profile to a freshly initialized, stopped home only."""
import argparse, json, os, pathlib, re, tomllib

PROFILE = {
 "app.toml": {(None,"pruning"): '"custom"', (None,"pruning-keep-recent"): '"10000"', (None,"pruning-interval"): '"100"', (None,"min-retain-blocks"): '1000000', ("state-sync","snapshot-interval"): '0', ("state-sync","snapshot-keep-recent"): '2'},
 "config.toml": {("tx_index","indexer"): '"kv"', ("storage","discard_abci_responses"): 'false'},
}

def render(text, changes):
 section=None; seen=set(); lines=[]
 for line in text.splitlines():
  match=re.fullmatch(r"\s*\[([^]]+)\]\s*(?:#.*)?",line)
  if match: section=match[1]
  assignment=re.match(r"\s*([\w-]+)\s*=",line)
  key=(section,assignment[1]) if assignment else None
  if key in changes:
   if key in seen: raise ValueError("duplicate setting: "+str(key))
   seen.add(key); line=key[1]+" = "+changes[key]
  lines.append(line)
 if seen != set(changes): raise ValueError("settings missing from actual config: "+str(set(changes)-seen))
 result="\n".join(lines)+"\n";tomllib.loads(result);return result

def configure(home, apply=False, role="rpc-ibc"):
 if role not in {"rpc-ibc", "consensus"}:raise ValueError("unknown role")
 home=pathlib.Path(home).resolve(strict=True)
 if (home/"config").is_symlink() or (home/"data").is_symlink():raise ValueError("configuration/data directory symlink rejected")
 # Never edit a running/existing chain database. Init's validator state JSON is harmless.
 if any((home/"data").glob("*.db")) or (home/"data/cs.wal").exists():
  raise ValueError("existing chain data: stop and review retention manually; this helper only supports fresh homes")
 outputs={}
 for name,changes in PROFILE.items():
  path=home/"config"/name
  if path.is_symlink():raise ValueError("configuration symlink rejected")
  effective=dict(changes)
  if name == "config.toml" and role == "consensus":
   effective[("tx_index","indexer")]='"null"';effective[("storage","discard_abci_responses")]='true'
  outputs[path]=render(path.read_text(encoding="utf-8"),effective)
 if apply:
  for path in outputs:
   if os.path.lexists(path.with_suffix(path.suffix+".before-history")):raise ValueError("backup already exists; refusing overwrite")
  for path,result in outputs.items():
   with path.with_suffix(path.suffix+".before-history").open("xb") as backup:backup.write(path.read_bytes())
   path.with_suffix(path.suffix+".before-history").chmod(path.stat().st_mode & 0o777)
  for path,result in outputs.items():
   temporary=path.with_suffix(path.suffix+".new")
   with temporary.open("x",encoding="utf-8",newline="\n") as stream:stream.write(result)
   temporary.chmod(path.stat().st_mode & 0o777)
  for path in outputs:
   temporary=path.with_suffix(path.suffix+".new")
   os.replace(temporary,path)
 return {str(path):tomllib.loads(result) for path,result in outputs.items()}

if __name__ == "__main__":
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--home",required=True);parser.add_argument("--apply",action="store_true");parser.add_argument("--role",choices=["rpc-ibc","consensus"],default="rpc-ibc")
 args=parser.parse_args();configure(args.home,args.apply,args.role)
 print("Development profile "+("applied with backups" if args.apply else "validated; add --apply to write"))
