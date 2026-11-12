"""Install and run the disposable launch follower as a transient systemd service."""

from contextlib import nullcontext
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import time
import uuid

from del_delegation_smoke import write_config
from mcnd_process_smoke import private_namespace


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def home_records(home):
    records = {}
    for path in (home, *sorted(home.rglob("*"))):
        metadata = path.lstat()
        if not (stat.S_ISDIR(metadata.st_mode) or stat.S_ISREG(metadata.st_mode)):
            raise RuntimeError("unexpected file type in the offline follower home")
        records[path.relative_to(home).as_posix()] = (
            metadata.st_uid, metadata.st_gid, stat.S_IMODE(metadata.st_mode),
            digest(path) if stat.S_ISREG(metadata.st_mode) else None,
        )
    return records


def home_digest(records):
    return hashlib.sha256(json.dumps(records, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def copy_offline_home(source, target):
    records = home_records(source)
    shutil.copytree(source, target)
    for relative, (uid, gid, mode, _) in records.items():
        path = target / relative
        os.chown(path, uid, gid, follow_symlinks=False)
        path.chmod(mode)
    if home_records(target) != records:
        raise RuntimeError("offline follower home copy differs from its source")
    return home_digest(records)


class SystemdFollower:
    """Own a private installation and a single systemd service for its follower."""

    def __init__(self, binary, expected_binary_sha256, signals=None, *, iavl_schema_probe=None):
        self.source = Path(binary)
        self.expected_sha256 = expected_binary_sha256
        self.signals = signals
        self.iavl_schema_probe = iavl_schema_probe
        self.unit = f"mcnd-launch-ci-{uuid.uuid4().hex}.service"
        self.submitted = False

    def __enter__(self):
        import pwd

        private_namespace()
        if os.geteuid() != 0 or not Path("/run/systemd/private").is_socket():
            raise RuntimeError("isolated follower requires the runner's systemd manager")
        self.user = pwd.getpwnam("nobody")
        if not self.user.pw_uid or not self.user.pw_gid:
            raise RuntimeError("systemd follower must use an unprivileged account")
        self.namespace_path = f"/proc/{os.getpid()}/ns/net"
        self.root = Path(tempfile.mkdtemp(prefix="mcnd-systemd-launch-", dir="/tmp"))
        try:
            # The service may traverse the root, but only the harness may write it.
            self.root.chmod(0o711)
            bin_dir = self.root / "bin"
            bin_dir.mkdir()
            bin_dir.chmod(0o755)
            installed = bin_dir / "mcnd"
            shutil.copyfile(self.source, installed)
            if digest(installed) != self.expected_sha256:
                raise RuntimeError("clean installation binary SHA256 differs from the signed package")
            installed.chmod(0o755)
            self.binary = str(installed)
        except BaseException:
            shutil.rmtree(self.root)
            raise
        return self

    def prepare(self, home):
        if home.parent != self.root or home.is_symlink():
            raise RuntimeError("follower home must be inside the clean installation")
        if self.iavl_schema_probe:
            write_config(home, "app.toml", {(None, "app-db-backend"): '"goleveldb"',
                                            (None, "iavl-disable-fastnode"): "true"})
        for path in (home, *home.rglob("*")):
            mode = path.lstat().st_mode
            if not (stat.S_ISDIR(mode) or stat.S_ISREG(mode)):
                raise RuntimeError("unexpected file type in the fresh follower home")
            os.chown(path, self.user.pw_uid, self.user.pw_gid, follow_symlinks=False)
        self.home = home

    def _properties(self, *names):
        result = subprocess.run(["systemctl", "--system", "show", self.unit,
                                 *[f"--property={name}" for name in names]],
                                capture_output=True, text=True, timeout=10)
        return dict(line.split("=", 1) for line in result.stdout.splitlines() if "=" in line)

    def spawn(self, args, path, env):
        if self.submitted or args[0] != self.binary or path.parent != self.root or "--home" not in args:
            raise RuntimeError("unexpected systemd follower command")
        if args[args.index("--home") + 1] != str(self.home):
            raise RuntimeError("systemd follower must use the prepared home")
        private_namespace()
        with path.open("x"):
            path.chmod(0o600)
        os.chown(path, self.user.pw_uid, self.user.pw_gid, follow_symlinks=False)
        self.args, self.log, self.gomaxprocs = tuple(args), path, env['GOMAXPROCS']
        self._start()
        return self

    def _start(self):
        guard = self.signals.finish_owned_operation() if self.signals else nullcontext()
        with guard:
            self.submitted = True
            result = subprocess.run([
                "systemd-run", "--system", "--quiet", "--collect", f"--unit={self.unit}", "--service-type=exec",
                f"--property=User={self.user.pw_name}", f"--property=Group={self.user.pw_gid}",
                f"--property=NetworkNamespacePath={self.namespace_path}", f"--property=WorkingDirectory={self.home}",
                f"--property=StandardOutput=append:{self.log}", "--property=StandardError=inherit",
                "--property=NoNewPrivileges=yes", "--property=UMask=0077", "--property=KillSignal=SIGINT",
                "--property=TimeoutStartSec=15s", "--property=TimeoutStopSec=20s", "--property=RuntimeMaxSec=10min",
                f"--setenv=HOME={self.home}", f"--setenv=GOMAXPROCS={self.gomaxprocs}", "--", *self.args,
            ], capture_output=True, text=True, timeout=25)
            if result.returncode:
                raise RuntimeError(f"could not start transient systemd follower: {result.stderr.strip()[:300]}")

    def _stop(self, *, require_unloaded=False):
        if not self.submitted:
            return
        subprocess.run(["systemctl", "--system", "--quiet", "stop", self.unit],
                       capture_output=True, text=True, timeout=30)
        deadline = time.monotonic() + 10
        while True:
            props = self._properties("LoadState", "ActiveState", "MainPID")
            if props.get("LoadState") == "not-found" or (not require_unloaded and
                props.get("ActiveState") in ("inactive", "failed") and int(props.get("MainPID", "0")) == 0
            ):
                self.submitted = False
                return
            if time.monotonic() >= deadline:
                raise RuntimeError("transient follower service did not stop and unload")
            time.sleep(0.2)

    def _replace_binary(self, source, expected_sha256):
        staged = Path(self.binary).with_name("mcnd.next")
        with Path(source).open("rb") as original, staged.open("xb") as replacement:
            shutil.copyfileobj(original, replacement)
        if digest(staged) != expected_sha256:
            raise RuntimeError("staged binary SHA256 differs from the signed package or backup")
        staged.chmod(0o755)
        staged.replace(self.binary)
        self.expected_sha256 = expected_sha256

    def _transition_evidence(self, previous):
        current = self.evidence()
        if current["mainPid"] == previous["mainPid"] or current["invocationId"] == previous["invocationId"]:
            raise RuntimeError("package transition did not start a new follower invocation")
        return {"previousMainPid": previous["mainPid"], "restartedMainPid": current["mainPid"],
                "previousInvocationId": previous["invocationId"], "restartedInvocationId": current["invocationId"],
                "previousBinarySha256": previous["binarySha256"], "binarySha256": current["binarySha256"],
                "backupHomeSha256": self.backup_home_sha256, "backupBinarySha256": self.backup_binary_sha256,
                "sameHome": True}

    def _inspect_iavl(self, home):
        result = subprocess.run([str(self.iavl_schema_probe), "--home", str(home)],
                                capture_output=True, text=True, timeout=20)
        if result.returncode:
            raise RuntimeError("could not inspect stopped follower's IAVL schema: " + result.stderr.strip()[:240])
        return json.loads(result.stdout)

    def upgrade(self, binary, expected_sha256):
        private_namespace()
        previous = self.evidence()
        if expected_sha256 == previous["binarySha256"] or digest(binary) != expected_sha256:
            raise RuntimeError("upgrade requires a different verified binary")
        self.backup = self.root / "backup"
        self.backup.mkdir(mode=0o700)
        previous_data = (self.home / "data").stat()
        self._stop(require_unloaded=True)
        self.backup_home_sha256 = copy_offline_home(self.home, self.backup / "home")
        backup_binary = self.backup / "mcnd"
        shutil.copyfile(self.binary, backup_binary)
        self.backup_binary_sha256 = digest(backup_binary)
        if self.backup_binary_sha256 != previous["binarySha256"]:
            raise RuntimeError("backup binary differs from the original signed package")
        if self.iavl_schema_probe:
            self.iavl_before = self._inspect_iavl(self.backup / "home")
            if (self.iavl_before["storageVersion"] != "1.0.0" or self.iavl_before["fastNodes"] != 0 or
                self.iavl_before["committedHeight"] <= 0):
                raise RuntimeError("the offline backup must contain committed IAVL state without the fast index")
            write_config(self.home, "app.toml", {(None, "iavl-disable-fastnode"): "false"})
            os.chown(self.home / "config/app.toml", self.user.pw_uid, self.user.pw_gid, follow_symlinks=False)
        self._replace_binary(binary, expected_sha256)
        self._start()
        if not os.path.samestat(previous_data, (self.home / "data").stat()):
            raise RuntimeError("upgrade changed the follower data directory")
        evidence = self._transition_evidence(previous)
        if self.iavl_schema_probe:
            evidence["iavlBefore"] = self.iavl_before
        return evidence

    def rollback(self):
        private_namespace()
        previous = self.evidence()
        if previous["binarySha256"] == self.backup_binary_sha256:
            raise RuntimeError("rollback requires the upgraded follower")
        self._stop(require_unloaded=True)
        if self.iavl_schema_probe:
            migrated = self._inspect_iavl(self.home)
            if migrated["storageVersion"] != "1.1.0" or migrated["fastNodes"] <= 0:
                raise RuntimeError("updated daemon did not persist the IAVL fast storage schema and index")
        restored = self.root / "restored-home"
        if copy_offline_home(self.backup / "home", restored) != self.backup_home_sha256:
            raise RuntimeError("restored follower home differs from the offline backup")
        if self.iavl_schema_probe:
            restored_schema = self._inspect_iavl(restored)
            if restored_schema != self.iavl_before:
                raise RuntimeError("restored follower IAVL schema and height differ from the original backup")
        self.home.rename(self.root / "upgraded-home")
        restored.rename(self.home)
        self._replace_binary(self.backup / "mcnd", self.backup_binary_sha256)
        self._start()
        evidence = self._transition_evidence(previous)
        if self.iavl_schema_probe:
            evidence.update(iavlAfterUpgrade=migrated, iavlRestored=restored_schema)
        return evidence

    def inspect_iavl_after_rollback(self):
        private_namespace()
        self._stop()
        schema = self._inspect_iavl(self.home)
        if schema["storageVersion"] != "1.0.0" or schema["fastNodes"] != 0:
            raise RuntimeError("rolled-back follower changed the restored IAVL storage schema")
        return schema

    def poll(self):
        props = self._properties("ActiveState", "MainPID")
        return None if props.get("ActiveState") == "active" and int(props.get("MainPID", "0")) else 1

    def restart(self):
        private_namespace()
        previous = self.evidence()
        previous_data = (self.home / "data").stat()
        guard = self.signals.finish_owned_operation() if self.signals else nullcontext()
        with guard:
            result = subprocess.run(["systemctl", "--system", "--quiet", "restart", self.unit],
                                    capture_output=True, text=True, timeout=45)
            if result.returncode:
                raise RuntimeError(f"could not restart transient systemd follower: {result.stderr.strip()[:300]}")
        restarted = self.evidence()
        if restarted["mainPid"] == previous["mainPid"] or restarted["invocationId"] == previous["invocationId"]:
            raise RuntimeError("systemd restart did not start a new follower invocation")
        if not os.path.samestat(previous_data, (self.home / "data").stat()):
            raise RuntimeError("systemd restart changed the follower data directory")
        return {"previousMainPid": previous["mainPid"], "restartedMainPid": restarted["mainPid"],
                "previousInvocationId": previous["invocationId"], "restartedInvocationId": restarted["invocationId"],
                "sameHome": True}

    def evidence(self):
        props = self._properties("ActiveState", "SubState", "MainPID", "User", "Type", "Transient",
                                 "NetworkNamespacePath", "WorkingDirectory", "InvocationID")
        if (props.get("ActiveState"), props.get("SubState"), props.get("User"), props.get("Type"),
            props.get("Transient"), props.get("NetworkNamespacePath")) != (
                "active", "running", self.user.pw_name, "exec", "yes", self.namespace_path):
            raise RuntimeError("expected a running transient systemd follower in the isolated namespace")
        if props.get("WorkingDirectory") != str(self.home):
            raise RuntimeError("running systemd follower does not use the prepared home")
        pid = int(props["MainPID"])
        if pid <= 1:
            raise RuntimeError("systemd did not report a follower MainPID")
        invocation_id = props.get("InvocationID", "")
        if not invocation_id or not invocation_id.strip("0"):
            raise RuntimeError("systemd did not report a follower invocation ID")
        process = Path(f"/proc/{pid}")
        status = dict(line.split(":", 1) for line in (process / "status").read_text().splitlines() if ":" in line)
        uid = int(status["Uid"].split()[1])
        if uid != self.user.pw_uid or int(status["PPid"]) != 1:
            raise RuntimeError("follower MainPID is not a non-root child of the systemd manager")
        namespace_matches = os.readlink(process / "ns/net") == os.readlink(self.namespace_path)
        if not namespace_matches or not (process / "exe").samefile(self.binary):
            raise RuntimeError("follower MainPID is outside its namespace or installation")
        binary_sha256 = digest(process / "exe")
        if binary_sha256 != self.expected_sha256:
            raise RuntimeError("systemd MainPID binary SHA256 differs from the signed package")
        return {"unit": self.unit, "activeState": props["ActiveState"], "subState": props["SubState"],
                "mainPid": pid, "invocationId": invocation_id, "serviceUid": uid, "networkNamespaceMatches": namespace_matches,
                "binarySha256": binary_sha256}

    def __exit__(self, *_):
        guard = self.signals.finish_owned_operation() if self.signals else nullcontext()
        with guard:
            self._stop()
            shutil.rmtree(self.root)
