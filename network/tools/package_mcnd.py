"""Package a source-bound Linux amd64 DEVELOPMENT binary; never publish it."""
import argparse, hashlib, io, json, os, pathlib, platform, subprocess, sys, tarfile, tempfile
from install_mcnd import validate_revision

def package(binary,source,revision,output):
 validate_revision(revision);source=pathlib.Path(source).resolve(strict=True);binary=pathlib.Path(binary).resolve(strict=True)
 if subprocess.check_output(["git","-C",str(source),"rev-parse","HEAD"],text=True).strip()!=revision:raise ValueError("source mismatch")
 if subprocess.check_output(["git","-C",str(source),"status","--porcelain","--untracked-files=no"],text=True).strip():raise ValueError("source has tracked modifications")
 sdk=source/"network/third_party/cosmos-sdk"
 module_file=source/"network/go.mod"
 if sdk.exists() or (module_file.exists() and "replace github.com/cosmos/cosmos-sdk => ./third_party/cosmos-sdk" in module_file.read_text()):
  subprocess.run([sys.executable,str(source/"network/scripts/verify_owned_sdk.py")],check=True)
 header=binary.read_bytes()[:20]
 if header[:5]!=b"\x7fELF\x02" or header[18:20]!=b"\x3e\x00":raise ValueError("Linux amd64 ELF required")
 info=subprocess.check_output(["go","version","-m",str(binary)],text=True)
 runtime_version=None
 if "main.buildCommit=" in info:
  if "main.buildCommit="+revision not in info:raise ValueError("binary lacks exact source buildCommit")
 else:
  # Go omits linker flags from build info under -trimpath.
  if platform.system()!="Linux" or platform.machine()!="x86_64":raise ValueError("trimpath provenance requires native Linux amd64 version verification")
  with tempfile.TemporaryDirectory(prefix="mcnd-package-version-") as version_home:
   runtime_version=json.loads(subprocess.check_output([str(binary),"version","--long","--output","json","--home",version_home],text=True,timeout=30))
  if runtime_version.get("commit")!=revision or runtime_version.get("server_name")!="mcnd":raise ValueError("runtime binary source commit mismatch")
 data={"bin/mcnd":binary.read_bytes(),"BUILD_INFO.txt":info.encode(),"DEVELOPMENT.txt":b"DEVELOPMENT ONLY. Native feature integration and release gates remain incomplete.\n"}
 if runtime_version is not None:data["CLI_VERSION.json"]=(json.dumps(runtime_version,sort_keys=True,indent=2)+"\n").encode()
 third_party=source/"network/third_party"
 notices=sorted({path for pattern in ("LICENSE*","COPYING*","NOTICE*") for path in third_party.rglob(pattern) if path.is_file()})
 for path in [source/"LICENSE",source/"network/LICENSE",*sorted((source/"network/systemcontracts").glob("LICENSE*")),*notices,*sorted(third_party.glob("*/UPSTREAM.json"))]:
  if path.is_file():data["provenance/"+path.relative_to(source).as_posix()]=path.read_bytes()
 manifest={"schemaVersion":1,"status":"development","sourceRevision":revision,"binarySha256":hashlib.sha256(data["bin/mcnd"]).hexdigest(),"files":{name:hashlib.sha256(value).hexdigest() for name,value in data.items()},"dependencyScope":"Go build information records effective module replacements. UPSTREAM manifests identify local owned patches; this is not a complete security audit or signed provenance."}
 data["MANIFEST.json"]=(json.dumps(manifest,indent=2)+"\n").encode()
 output=pathlib.Path(output);output.parent.mkdir(parents=True,exist_ok=True)
 if os.path.lexists(output.with_name(output.name+".sha256")):raise ValueError("checksum sidecar already exists")
 with output.open("xb") as stream, tarfile.open(fileobj=stream,mode="w:gz") as archive:
  for name,value in sorted(data.items()):
   entry=tarfile.TarInfo(name);entry.size=len(value);entry.mode=0o755 if name=="bin/mcnd" else 0o644;entry.mtime=0
   archive.addfile(entry,io.BytesIO(value))
 with output.with_name(output.name+".sha256").open("x",encoding="utf-8") as sidecar:sidecar.write(hashlib.sha256(output.read_bytes()).hexdigest()+"  "+output.name+"\n")
 return manifest

if __name__=="__main__":
 parser=argparse.ArgumentParser(description=__doc__)
 for name in ("binary","source","revision","output"):parser.add_argument("--"+name,required=True)
 args=parser.parse_args();package(args.binary,args.source,args.revision,args.output)
