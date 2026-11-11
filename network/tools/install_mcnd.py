"""Build a pinned DEVELOPMENT daemon without changing any node home or service."""
import argparse, pathlib, platform, re, shutil, subprocess, tempfile
from source_inventory import production_environment, verify_build_inputs
REPOSITORY="https://github.com/maxwell2010/go-smart-node.git"

def pinned_go_version(module_text):
    versions = re.findall(r"^go[ \t]+(1\.[0-9]+\.[0-9]+)[ \t]*$", module_text, re.MULTILINE)
    if len(versions) != 1:
        raise ValueError("network/go.mod must declare one exact Go patch version")
    return "go" + versions[0]

def validate_revision(revision):
 if not re.fullmatch(r"[0-9a-f]{40}",revision):raise ValueError("full lowercase 40-character Git revision required")
 return revision

def run(command, **kwargs):return subprocess.check_output(command,text=True,**kwargs).strip()

def install(revision, destination, source=None):
 validate_revision(revision)
 destination=pathlib.Path(destination).expanduser().absolute()
 if destination.exists() or destination.is_symlink():raise ValueError("destination already exists; choose a new versioned path")
 if platform.system()!="Linux" or platform.machine()!="x86_64":raise ValueError("only Linux amd64 is validated")
 environment=production_environment()
 for executable in ("git","gcc","python3"):
  if not shutil.which(executable):raise ValueError("missing dependency: "+executable)
 with tempfile.TemporaryDirectory(prefix="mcnd-build-") as temporary:
  if source is None:
   source=pathlib.Path(temporary)/"source"
   run(["git","init","--quiet",str(source)])
   run(["git","-C",str(source),"fetch","--depth","1",REPOSITORY,revision])
   run(["git","-C",str(source),"checkout","--detach","FETCH_HEAD"])
  else:source=pathlib.Path(source).resolve(strict=True)
  if run(["git","-C",str(source),"rev-parse","HEAD"])!=revision:raise ValueError("source revision mismatch")
  if run(["git","-C",str(source),"status","--porcelain","--untracked-files=no"]):raise ValueError("source has tracked modifications")
  module=source/"network"
  expected_go=pinned_go_version((module/"go.mod").read_text(encoding="utf8"))
  if run(["go","version"],env=environment).split()[:3]!=["go","version",expected_go]:raise ValueError("install exact "+expected_go+" for the selected revision")
  for verifier in ("verify_owned_evm.py","verify_owned_geth.py","verify_owned_sdk.py"):
   subprocess.run(["python3",str(module/"scripts"/verifier)],check=True)
  for command in (["go","mod","download"],["go","mod","verify"]):subprocess.run(command,cwd=module,env=environment,check=True)
  subprocess.run(["python3",str(module/"scripts/verify_pion_transport.py")],cwd=module,env=environment,check=True)
  subprocess.run(["python3",str(module/"scripts/verify_owned_sdk.py"),"--compiled"],cwd=module,env=environment,check=True)
  source_inputs=verify_build_inputs(source,environment=environment)
  target=pathlib.Path(temporary)/"mcnd"
  subprocess.run(["go","build","-mod=readonly","-tags=","-p","1","-trimpath","-ldflags","-X main.buildCommit="+revision,"-o",str(target),"./cmd/mcnd"],cwd=module,env=environment,check=True)
  if verify_build_inputs(source,environment=environment)!=source_inputs:raise ValueError("production source inputs changed during build")
  if run(["git","-C",str(source),"diff","--name-only","--","network/go.mod","network/go.sum"]):raise ValueError("module files changed")
  subprocess.run([str(target),"version","--home",str(pathlib.Path(temporary)/"version-home")],check=True)
  destination.parent.mkdir(parents=True,exist_ok=True)
  # Exclusive create: another installer cannot be overwritten after validation.
  with destination.open("xb") as stream, target.open("rb") as built:shutil.copyfileobj(built,stream)
  destination.chmod(0o755)
 print("Installed DEVELOPMENT binary:",destination)
 print("No node home, keys, genesis or systemd service was created.")

if __name__=="__main__":
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument("--revision",required=True);parser.add_argument("--destination",required=True);parser.add_argument("--source")
 args=parser.parse_args();install(args.revision,args.destination,args.source)
