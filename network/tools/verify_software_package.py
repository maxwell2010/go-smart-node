"""Verify bounded software-release package contents without executing its binary.

This checks checksums and consistency, not publisher identity. Verify the
GitHub OIDC attestation on the complete archive before trusting these contents.
"""
import argparse
import gzip
import hashlib
import json
from pathlib import Path, PurePosixPath
import re
import tarfile

from binary_provenance import MAIN_MODULE, NATIVE_BUILD_TAGS, verify_recorded_build, validate_release_version
from install_mcnd import validate_revision, pinned_go_version

REQUIRED = {"bin/mcnd", "BUILD_INFO.txt", "SOFTWARE_RELEASE.txt", "CLI_VERSION.json",
            "SOURCE_INPUTS.json", "VERIFIED_BUILD.json", "SBOM.cdx.json", "MANIFEST.json", "provenance/LICENSE"}
JSON_FILES = {name for name in REQUIRED if name.endswith(".json")}
MAX_ARCHIVE = 512 << 20
MAX_TOTAL = 1 << 30
MAX_FILES = 8192

def strict_json(data):
    def unique(pairs):
        result = {}
        for key,value in pairs:
            if key in result:raise ValueError("duplicate package JSON key")
            result[key]=value
        return result
    return json.loads(data,object_pairs_hook=unique)

def allowed_name(name):
    path=PurePosixPath(name)
    return (name in REQUIRED or name.startswith("provenance/") and
            path.name.startswith(("LICENSE","COPYING","NOTICE","UPSTREAM.json"))) and (
            not path.is_absolute() and ".." not in path.parts and "\\" not in name and str(path)==name)

def verify(archive,revision,release_version,*,native_denom=None):
    validate_revision(revision);validate_release_version(release_version)
    archive=Path(archive)
    if not archive.is_file() or not 0<archive.stat().st_size<=MAX_ARCHIVE:raise ValueError("invalid software archive size")
    hashes={};metadata={};total=0;binary_header=b""
    with gzip.open(archive,"rb") as stream:
        while True:
            header=stream.read(512)
            if header==bytes(512):
                tail=stream.read(tarfile.RECORDSIZE+1)
                if not 512<=len(tail)<=tarfile.RECORDSIZE or any(tail):raise ValueError("invalid software archive trailer")
                break
            if len(header)!=512 or header[257:263]!=b"ustar\0":raise ValueError("software archive requires bounded USTAR headers")
            member=tarfile.TarInfo.frombuf(header,"ascii","strict")
            if member.type!=tarfile.REGTYPE or not allowed_name(member.name) or member.name in hashes:raise ValueError("unexpected or duplicate software archive member")
            maximum=384<<20 if member.name=="bin/mcnd" else 16<<20
            total+=member.size
            if not 0<member.size<=maximum or total>MAX_TOTAL or len(hashes)>=MAX_FILES:raise ValueError("software archive member exceeds bounds")
            digest=hashlib.sha256();remaining=member.size;kept=[]
            while remaining:
                chunk=stream.read(min(1<<20,remaining))
                if not chunk:raise ValueError("truncated software archive member")
                if member.name=="bin/mcnd" and not binary_header:binary_header=chunk[:20]
                if member.name in JSON_FILES or member.name=="BUILD_INFO.txt":kept.append(chunk)
                digest.update(chunk);remaining-=len(chunk)
            padding=(-member.size)%512
            if stream.read(padding)!=bytes(padding):raise ValueError("invalid software archive padding")
            hashes[member.name]=digest.hexdigest()
            if kept:metadata[member.name]=b"".join(kept)
    if not REQUIRED<=hashes.keys():raise ValueError("missing required software release files")
    if binary_header[:6]!=b"\x7fELF\x02\x01" or binary_header[18:20]!=b"\x3e\x00":raise ValueError("software package binary is not Linux amd64 ELF")
    manifest=strict_json(metadata["MANIFEST.json"])
    expected_fields={"schemaVersion","status","sourceRevision","binarySha256","files","dependencyScope","releaseVersion","includesGenesis","networkLaunchApproved"}
    if native_denom is not None:expected_fields.add("nativeDenom")
    if set(manifest)!=expected_fields or manifest["schemaVersion"]!=1 or manifest["status"]!="software-release" or manifest["sourceRevision"]!=revision or manifest["releaseVersion"]!=release_version or manifest["includesGenesis"] is not False or manifest["networkLaunchApproved"] is not False:raise ValueError("software release manifest identity differs")
    if manifest.get("nativeDenom")!=native_denom:raise ValueError("software package native denomination differs")
    if manifest["files"]!={name:digest for name,digest in hashes.items() if name!="MANIFEST.json"} or manifest["binarySha256"]!=hashes["bin/mcnd"]:raise ValueError("software release manifest SHA256 differs")
    runtime=strict_json(metadata["CLI_VERSION.json"])
    if runtime.get("commit")!=revision or runtime.get("version")!=release_version or runtime.get("server_name")!="mcnd":raise ValueError("software runtime version identity differs")
    source=strict_json(metadata["SOURCE_INPUTS.json"])
    build=strict_json(metadata["VERIFIED_BUILD.json"])
    if source.get("sourceRevision")!=revision or build.get("settings",{}).get("vcs.revision")!=revision or build.get("settings",{}).get("vcs.modified")!="false" or build.get("module",{}).get("path")!=MAIN_MODULE:raise ValueError("software source/build provenance differs")
    expected_go=pinned_go_version((Path(__file__).resolve().parents[1]/"go.mod").read_text(encoding="utf-8"))
    if verify_recorded_build(metadata["BUILD_INFO.txt"].decode(),revision,expected_go,release_version=release_version,
                             native_denom=native_denom)!=build:raise ValueError("recorded software build metadata differs")
    sbom=strict_json(metadata["SBOM.cdx.json"])
    component=sbom.get("metadata",{}).get("component",{})
    props={p["name"]:p["value"] for p in component.get("properties",[])}
    if sbom.get("bomFormat")!="CycloneDX" or component.get("name")!="mcnd" or component.get("version")!=release_version or component.get("hashes")!=[{"alg":"SHA-256","content":hashes["bin/mcnd"]}] or props.get("source.commit")!=revision:raise ValueError("software SBOM identity differs")
    with archive.open("rb") as stream:archive_sha=hashlib.file_digest(stream,"sha256").hexdigest()
    result={"status":"software-release","releaseVersion":release_version,"sourceRevision":revision,
            "archiveSha256":archive_sha,"binarySha256":hashes["bin/mcnd"],"includesGenesis":False,"networkLaunchApproved":False}
    if native_denom is not None:result["nativeDenom"]=native_denom
    return result

if __name__=="__main__":
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive",type=Path,required=True);parser.add_argument("--revision",required=True);parser.add_argument("--release-version",required=True)
    parser.add_argument("--native-denom",choices=NATIVE_BUILD_TAGS)
    args=parser.parse_args()
    try:print(json.dumps(verify(args.archive,args.revision,args.release_version,native_denom=args.native_denom),sort_keys=True))
    except (ValueError,OSError,EOFError,tarfile.TarError,KeyError,TypeError) as error:parser.exit(1,"software package: "+str(error)+"\n")
