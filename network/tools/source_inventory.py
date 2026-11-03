"""Bind production Go source/embed inputs to a clean tracked repository."""
import hashlib
import argparse
import json
import os
from pathlib import Path
import subprocess

INPUT_FIELDS = (
    "GoFiles", "CgoFiles", "CFiles", "CXXFiles", "MFiles", "HFiles", "FFiles",
    "SFiles", "SwigFiles", "SwigCXXFiles", "SysoFiles", "EmbedFiles",
)


def production_environment(environment=None):
    env = dict(os.environ if environment is None else environment)
    for key in ("GOFLAGS", "GOEXPERIMENT", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_FFLAGS", "CGO_LDFLAGS"):
        if env.get(key, "").strip():
            raise ValueError("unsupported default-build environment override: " + key)
    if env.get("GOWORK", "") not in ("", "off"):
        raise ValueError("unsupported default-build environment override: GOWORK")
    env.update(GOFLAGS="", GOENV="off", GOWORK="off", GOTOOLCHAIN="local",
               GOOS="linux", GOARCH="amd64", GOAMD64="v1", CGO_ENABLED="1", CC="gcc")
    return env


def json_records(raw):
    decoder = json.JSONDecoder()
    offset = 0
    while offset < len(raw):
        while offset < len(raw) and raw[offset].isspace():
            offset += 1
        if offset == len(raw):
            break
        value, offset = decoder.raw_decode(raw, offset)
        yield value


def verify_build_inputs(source, *, environment=None, package_loader=None):
    environment = production_environment(environment)
    source = Path(source).resolve(strict=True)
    module = source / "network"
    def git(*args):
        return subprocess.check_output(["git", "-C", str(source), *args], text=True).strip()
    if Path(git("rev-parse", "--show-toplevel")).resolve(strict=True) != source:
        raise ValueError("source must be the pinned repository root")
    if git("status", "--porcelain", "--untracked-files=no"):
        raise ValueError("tracked source modifications prevent a pinned build")
    tracked = set(git("ls-files", "-z").split("\0"))
    inputs = {}

    def add_input(path):
        lexical = Path(path).absolute()
        try:
            relative = lexical.relative_to(source)
        except ValueError:
            raise ValueError("local build input escapes the pinned repository") from None
        probe = source
        for component in relative.parts:
            probe = probe / component
            if probe.is_symlink():
                raise ValueError("symbolic local build input is unsupported: " + relative.as_posix())
        resolved = lexical.resolve(strict=True)
        if not resolved.is_relative_to(source):
            raise ValueError("local build input escapes the pinned repository")
        name = resolved.relative_to(source).as_posix()
        if name not in tracked:
            raise ValueError("untracked or ignored production build input: " + name)
        inputs[name] = hashlib.sha256(resolved.read_bytes()).hexdigest()

    if package_loader is None:
        raw = subprocess.check_output(
            ["go", "list", "-mod=readonly", "-tags=", "-deps", "-json", "./cmd/mcnd"],
            cwd=module, env=environment, text=True,
        )
        packages = json_records(raw)
    else:
        packages = package_loader(module)
    local_packages = 0
    for package in packages:
        if package.get("Error") or package.get("Incomplete"):
            raise ValueError("incomplete production package inventory")
        directory = Path(package["Dir"]).absolute()
        dependency = package.get("Module", {})
        replacement = dependency.get("Replace", {})
        local = dependency.get("Main") or (replacement and not replacement.get("Version"))
        if not directory.is_relative_to(source):
            if local:
                raise ValueError("local replacement lies outside the pinned repository")
            continue
        local_packages += 1
        for field in INPUT_FIELDS:
            for filename in package.get(field, []):
                add_input(directory / filename)
        if dependency.get("GoMod"):
            add_input(dependency["GoMod"])
        if replacement.get("GoMod"):
            add_input(replacement["GoMod"])
    if local_packages == 0:
        raise ValueError("production inventory contains no local packages")
    add_input(module / "go.mod")
    add_input(module / "go.sum")
    return {"schemaVersion": 1, "sourceRevision": git("rev-parse", "HEAD"),
            "scope": "Actual default mcnd source/embed inputs; external modules require module/source verifiers",
            "buildPolicy": {key: environment[key] for key in ("GOENV", "GOWORK", "GOTOOLCHAIN", "GOFLAGS", "GOOS", "GOARCH", "GOAMD64", "CGO_ENABLED", "CC")},
            "localPackages": local_packages, "files": dict(sorted(inputs.items()))}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    inventory = verify_build_inputs(args.source)
    with Path(args.output).open("x", encoding="utf-8") as stream:
        json.dump(inventory, stream, indent=2, sort_keys=True)
        stream.write("\n")
