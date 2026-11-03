"""Apply a development history profile to a freshly initialized, stopped home only."""
import argparse, json, os, pathlib, re, tomllib

PROFILE = {
 "app.toml": {(None,"pruning"): '"custom"', (None,"pruning-keep-recent"): '"10000"', (None,"pruning-interval"): '"100"', (None,"min-retain-blocks"): '1000000', ("state-sync","snapshot-interval"): '0', ("state-sync","snapshot-keep-recent"): '2'},
 "config.toml": {("tx_index","indexer"): '"kv"', ("storage","discard_abci_responses"): 'false'},
}

def validate_fresh_home(home):
 for directory in (home/"config", home/"data"):
  if directory.is_symlink() or not directory.is_dir():raise ValueError("real configuration/data directories required")
 for path in (home/"data").iterdir():
  if path.name != "priv_validator_state.json" or path.is_symlink() or not path.is_file():
   raise ValueError("existing chain data or unexpected artifact: fresh homes only")
  state=json.loads(path.read_bytes())
  if not isinstance(state,dict) or set(state)-{"height","round","step","signature","signbytes"}:
   raise ValueError("unrecognized validator signing state")
  if state.get("height") != "0" or type(state.get("round")) is not int or state["round"] != 0 or type(state.get("step")) is not int or state["step"] != 0:
   raise ValueError("validator has existing signing state: fresh homes only")
  if any(state.get(name) not in (None, "") for name in ("signature","signbytes")):
   raise ValueError("validator has existing signature: fresh homes only")

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
 # A zeroed init signing-state file is allowed; unknown data is never guessed safe.
 validate_fresh_home(home)
 outputs={};originals={};modes={}
 for name,changes in PROFILE.items():
  path=home/"config"/name
  if path.is_symlink() or not path.is_file():raise ValueError("regular configuration file required")
  originals[path]=path.read_bytes();modes[path]=path.stat().st_mode & 0o777
  effective=dict(changes)
  if name == "config.toml" and role == "consensus":
   effective[("tx_index","indexer")]='"null"';effective[("storage","discard_abci_responses")]='true'
  outputs[path]=render(originals[path].decode("utf-8"),effective)
 if apply:
  for path in outputs:
   for suffix in (".before-history", ".new"):
    if os.path.lexists(path.with_suffix(path.suffix+suffix)):raise ValueError("backup/staging file already exists; refusing overwrite")
  validate_fresh_home(home)
  if any(path.is_symlink() or path.read_bytes()!=originals[path] for path in outputs):raise ValueError("configuration changed during review")
  for path,result in outputs.items():
   with path.with_suffix(path.suffix+".before-history").open("xb") as backup:backup.write(originals[path])
   path.with_suffix(path.suffix+".before-history").chmod(modes[path])
  for path,result in outputs.items():
   temporary=path.with_suffix(path.suffix+".new")
   with temporary.open("x",encoding="utf-8",newline="\n") as stream:stream.write(result)
   temporary.chmod(modes[path])
  validate_fresh_home(home)
  if any(path.is_symlink() or path.read_bytes()!=originals[path] for path in outputs):raise ValueError("configuration changed during staging")
  for path in outputs:
   temporary=path.with_suffix(path.suffix+".new")
   os.replace(temporary,path)
 return {str(path):tomllib.loads(result) for path,result in outputs.items()}

if __name__ == "__main__":
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--home",required=True);parser.add_argument("--apply",action="store_true");parser.add_argument("--role",choices=["rpc-ibc","consensus"],default="rpc-ibc")
 args=parser.parse_args();configure(args.home,args.apply,args.role)
 print("Development profile "+("applied with backups" if args.apply else "validated; add --apply to write"))
