"""Check recorded Go binary metadata against the pinned default package graph.

This detects accidental build/input mismatches. It is not a signed compiler
attestation and does not replace owned-source or module-integrity verification.
"""
import json
import re
from pathlib import Path
import subprocess

from source_inventory import NATIVE_BUILD_TAGS, json_records, native_build_tag, production_environment

MAIN_MODULE = "github.com/maxwell2010/go-smart-node/network"

def validate_release_version(value):
    if not isinstance(value,str) or len(value)>64 or not re.fullmatch(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)",value):
        raise ValueError("release-version must be canonical stable SemVer X.Y.Z")
    return value


def parse_build_info(info):
    lines = info.splitlines()
    if not lines or ": " not in lines[0]:
        raise ValueError("missing Go binary toolchain metadata")
    result = {"goVersion": lines[0].rsplit(": ", 1)[1], "settings": {}, "dependencies": {}}
    previous = None
    for line in lines[1:]:
        if not line.strip():
            continue
        fields = line.lstrip("\t").split("\t")
        kind = fields[0]
        if kind == "path" and len(fields) == 2 and "path" not in result:
            result["path"] = fields[1]
            previous = None
        elif kind in ("mod", "dep", "=>") and len(fields) in (3, 4):
            module = {"path": fields[1], "version": fields[2], "sum": fields[3] if len(fields) == 4 else ""}
            if kind == "mod" and "module" not in result:
                result["module"] = module
                previous = module
            elif kind == "dep" and module["path"] not in result["dependencies"]:
                result["dependencies"][module["path"]] = module
                previous = module
            elif kind == "=>" and previous is not None and "replace" not in previous:
                previous["replace"] = module
                previous = None
            else:
                raise ValueError("duplicate or unattached Go module metadata")
        elif kind == "build" and len(fields) == 2 and "=" in fields[1]:
            key, value = fields[1].split("=", 1)
            if key in result["settings"]:
                raise ValueError("duplicate Go build setting")
            if value.startswith('"'):
                value = json.loads(value)
            result["settings"][key] = value
            previous = None
        else:
            raise ValueError("unexpected Go build metadata record")
    return result


def module_identity(module):
    result = {"path": module["Path"], "version": module.get("Version", "(devel)"), "sum": module.get("Sum", "")}
    if module.get("Replace"):
        result["replace"] = module_identity(module["Replace"])
    return result


def verify_recorded_build(info, revision, go_version, *, release_version=None, native_denom=None):
    metadata = parse_build_info(info)
    if metadata["goVersion"] != go_version or metadata.get("path") != MAIN_MODULE + "/cmd/mcnd":
        raise ValueError("binary toolchain or main package differs from default build policy")
    if metadata.get("module") != {"path": MAIN_MODULE, "version": "(devel)", "sum": ""}:
        raise ValueError("binary main module differs from pinned source")
    required = {"-buildmode": "exe", "-compiler": "gc", "-trimpath": "true",
                "CGO_ENABLED": "1", "GOOS": "linux", "GOARCH": "amd64", "GOAMD64": "v1",
                "vcs": "git", "vcs.revision": revision, "vcs.modified": "false"}
    build_tag = native_build_tag(native_denom)
    if build_tag:
        if metadata["settings"].get("-tags") != build_tag:
            raise ValueError("binary build tag differs from the requested native denomination")
        required["-tags"] = build_tag
    settings = metadata["settings"]
    if any(settings.get(key) != value for key, value in required.items()):
        raise ValueError("binary settings or clean VCS revision differ from default build policy")
    # Fixed Go toolchain/default flags emit no additional instrumentation or
    # compiler/linker overrides. Preserve, but do not interpret, the VCS time.
    optional={"vcs.time"}
    if release_version is not None:
        validate_release_version(release_version)
        optional.add("-ldflags")
        expected_flags="-X main.buildCommit="+revision+" -X main.buildVersion="+release_version
        if "-ldflags" in settings and settings["-ldflags"]!=expected_flags:
            raise ValueError("binary contains unsupported release linker settings")
    if set(settings) - set(required) - optional:
        raise ValueError("binary contains unsupported default-build settings")
    return metadata


def verify_binary_build(info, source, revision, go_version, *, environment=None, package_loader=None,
                        release_version=None, native_denom=None):
    metadata = verify_recorded_build(info, revision, go_version, release_version=release_version,
                                     native_denom=native_denom)
    environment = production_environment(environment)
    if package_loader is None:
        raw = subprocess.check_output(
            ["go", "list", "-mod=readonly", "-tags=" + native_build_tag(native_denom), "-deps", "-json", "./cmd/mcnd"],
            cwd=Path(source) / "network", text=True, env=environment,
        )
        packages = json_records(raw)
    else:
        packages = package_loader()
    expected = {}
    main_seen = False
    for package in packages:
        if package.get("Error") or package.get("Incomplete"):
            raise ValueError("incomplete binary dependency inventory")
        module = package.get("Module")
        if module is None:
            continue
        if module.get("Main"):
            if module_identity(module) != metadata["module"]:
                raise ValueError("default source main module does not match binary")
            main_seen = True
        else:
            identity = module_identity(module)
            previous = expected.setdefault(module["Path"], identity)
            if previous != identity:
                raise ValueError("inconsistent default dependency module identity")
    if not main_seen or metadata["dependencies"] != expected:
        raise ValueError("binary module versions, checksums or replacements differ from default package graph")
    return metadata
