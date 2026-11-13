"""Install and run the disposable launch follower as a transient systemd service."""

from contextlib import nullcontext
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import tempfile
import time
import uuid

from configure_history import validate_fresh_home
from del_delegation_smoke import write_config
from mcnd_process_smoke import private_namespace
from withdrawal_genesis_smoke import private_bytes, private_write


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

    def _configure_environment(self):
        import pwd

        private_namespace()
        if os.geteuid() != 0 or not Path("/run/systemd/private").is_socket():
            raise RuntimeError("isolated follower requires the runner's systemd manager")
        self.user = pwd.getpwnam("nobody")
        if not self.user.pw_uid or not self.user.pw_gid:
            raise RuntimeError("systemd follower must use an unprivileged account")
        self.namespace_path = f"/proc/{os.getpid()}/ns/net"

    def _install_binary(self, bin_dir):
        bin_dir.mkdir()
        bin_dir.chmod(0o755)
        installed = bin_dir / "mcnd"
        shutil.copyfile(self.source, installed)
        if digest(installed) != self.expected_sha256:
            raise RuntimeError("clean installation binary SHA256 differs from the signed package")
        installed.chmod(0o755)
        self.binary = str(installed)

    def __enter__(self):
        self._configure_environment()
        self.root = Path(tempfile.mkdtemp(prefix="mcnd-systemd-launch-", dir="/tmp"))
        try:
            # The service may traverse the root, but only the harness may write it.
            self.root.chmod(0o711)
            self._install_binary(self.root / "bin")
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


def validator_identity(home):
    key = json.loads(private_bytes(home / "config/priv_validator_key.json", 8192))
    pub_key, priv_key = key["pub_key"], key["priv_key"]
    public = base64.b64decode(pub_key["value"], validate=True)
    private = base64.b64decode(priv_key["value"], validate=True)
    if (pub_key["type"] != "tendermint/PubKeyEd25519" or priv_key["type"] != "tendermint/PrivKeyEd25519" or
        len(public) != 32 or len(private) != 64 or private[32:] != public):
        raise RuntimeError("expected an Ed25519 fixture signing key")
    address = hashlib.sha256(public).hexdigest()[:40].upper()
    if key["address"] != address:
        raise RuntimeError("fixture signing key and consensus address disagree")
    return address


def validator_watermark(raw):
    state = json.loads(raw)
    height, round_number, step = state['height'], state['round'], state['step']
    if not isinstance(height, str) or not re.fullmatch(r"0|[1-9][0-9]{0,18}", height):
        raise RuntimeError("invalid height in validator signing state")
    height = int(height)
    if (height >= 2**63 or type(round_number) is not int or not 0 <= round_number < 2**31 or
        type(step) is not int or not 0 <= step <= 3):
        raise RuntimeError("invalid height, round or step in validator signing state")
    if height == 0:
        if round_number or step or any(state.get(name) not in (None, "") for name in ("signature", "signbytes")):
            raise RuntimeError("fresh validator signing state is not empty")
    elif (step == 0 or len(base64.b64decode(state.get('signature', ''), validate=True)) != 64 or
          not re.fullmatch(r"(?:[A-Fa-f0-9]{2})+", state.get('signbytes', ''))):
        raise RuntimeError("persisted validator signing state lacks its signature or sign bytes")
    return height, round_number, step


class SystemdValidatorFleet:
    """Manage isolated validator services, their backups and restored signer state."""

    KEY_FILE = Path("config/priv_validator_key.json")
    STATE_FILE = Path("data/priv_validator_state.json")

    def __init__(self, binary, binary_sha256, signals=None):
        self.root_binary = str(Path(binary).resolve(strict=True))
        self.binary_sha256 = binary_sha256
        self.signals = signals
        self.services = {}
        self.original_identities = {}
        self.replay_identities = {}
        self.backups = None
        self.restored = False
        self.signing_checkpoints = None
        self.signers_reactivated = False

    def __enter__(self):
        private_namespace()
        if digest(self.root_binary) != self.binary_sha256:
            raise RuntimeError("fleet binary does not match its expected SHA256")
        self.root = Path(tempfile.mkdtemp(prefix="mcnd-systemd-fleet-", dir="/tmp"))
        self.root.chmod(0o711)
        return self

    def install(self, home):
        if home.parent != self.root or home.is_symlink() or not re.fullmatch(r"[a-d]", home.name) or home in self.services:
            raise RuntimeError("expected one new fixture validator home inside the fleet")
        validate_fresh_home(home)
        identity = validator_identity(home)
        if identity in self.original_identities.values():
            raise RuntimeError("fixture validators must use different signing keys")
        service = SystemdFollower(self.root_binary, self.binary_sha256, self.signals)
        service._configure_environment()
        service.root = self.root
        service._install_binary(self.root / f"bin-{home.name}")
        service.prepare(home)
        self.services[home] = service
        self.original_identities[home] = identity
        return service.binary

    def binary_for(self, home):
        return self.services[home].binary

    def spawn(self, args, log, env):
        home = Path(args[args.index("--home") + 1])
        service = self.services[home]
        if service.submitted:
            raise RuntimeError("validator service is already submitted")
        identity = validator_identity(home)
        if self.signers_reactivated:
            key, state, _ = self.signing_checkpoints[home]
            if (identity != self.original_identities[home] or private_bytes(home / self.KEY_FILE, 8192) != key or
                private_bytes(home / self.STATE_FILE, 8192) != state):
                raise RuntimeError("reactivated validator must start with its exact retained key and signing state")
        elif self.restored:
            replay_key = self.replay_identities.get(home)
            if (identity in self.original_identities.values() or
                (identity != replay_key and identity in self.replay_identities.values()) or
                validator_watermark(private_bytes(home / self.STATE_FILE, 8192)) != (0, 0, 0)):
                raise RuntimeError("backup replay requires a fresh non-signing consensus key and state")
        elif identity != self.original_identities[home]:
            raise RuntimeError("the fixture validator consensus identity changed before the upgrade")
        service.prepare(home)
        started = service.spawn(args, log, env)
        if self.restored and not self.signers_reactivated:
            self.replay_identities[home] = identity
        return started

    def stop(self, service):
        if service not in self.services.values():
            raise RuntimeError("cannot stop a service outside this fleet")
        private_namespace()
        guard = self.signals.finish_owned_operation() if self.signals else nullcontext()
        with guard:
            service._stop(require_unloaded=True)

    def _require_closed_fleet(self):
        private_namespace()
        if len(self.services) not in (3, 4) or any(service.submitted for service in self.services.values()):
            raise RuntimeError("checkpoint and replacement require all fleet services stopped")

    def checkpoint(self):
        self._require_closed_fleet()
        if self.backups is not None:
            raise RuntimeError("fleet checkpoint already exists")
        directory = self.root / "backups"
        directory.mkdir(mode=0o700)
        self.backups = {home.name: copy_offline_home(home, directory / home.name) for home in self.services}
        return dict(self.backups)

    def replace_binaries(self, binary, sha256):
        self._require_closed_fleet()
        if self.backups is None or digest(binary) != sha256:
            raise RuntimeError("replacement requires a fleet checkpoint and a verified binary")
        for service in self.services.values():
            service._replace_binary(binary, sha256)

    def _watermark_evidence(self):
        evidence = {}
        for home, (_, state, (height, round_number, step)) in self.signing_checkpoints.items():
            evidence[home.name] = {"height": height, "round": round_number, "step": step,
                                   "consensusAddress": self.original_identities[home],
                                   "stateSha256": hashlib.sha256(state).hexdigest()}
        return evidence

    def capture_signing_watermarks(self):
        self._require_closed_fleet()
        if self.backups is None or self.restored or self.signing_checkpoints is not None:
            raise RuntimeError("retain signing state once, after all updated validators stop and before restore")
        checkpoints = {}
        for home in self.services:
            backup = self.root / "backups" / home.name
            key = private_bytes(home / self.KEY_FILE, 8192)
            state = private_bytes(home / self.STATE_FILE, 8192)
            previous_state = private_bytes(backup / self.STATE_FILE, 8192)
            watermark = validator_watermark(state)
            previous = validator_watermark(previous_state)
            if (validator_identity(home) != self.original_identities[home] or
                key != private_bytes(backup / self.KEY_FILE, 8192) or watermark < previous or
                (watermark == previous and state != previous_state)):
                raise RuntimeError("updated validator signing key or state regressed relative to its backup")
            checkpoints[home] = key, state, watermark
        self.signing_checkpoints = checkpoints
        return self._watermark_evidence()

    def reactivate_signers(self, verified_heights):
        self._require_closed_fleet()
        if (not self.restored or self.signers_reactivated or self.signing_checkpoints is None or
            set(verified_heights) != set(self.services) or set(self.replay_identities) != set(self.services)):
            raise RuntimeError("signer reactivation requires a verified restored replay by the entire stopped fleet")
        maximum = max(watermark[0] for _, _, watermark in self.signing_checkpoints.values())
        for home, height in verified_heights.items():
            if (type(height) is not int or height < maximum or validator_identity(home) != self.replay_identities[home] or
                validator_watermark(private_bytes(home / self.STATE_FILE, 8192)) != (0, 0, 0)):
                raise RuntimeError("restored observer has not safely replayed past all retained signing heights")
        for home, (key, state, _) in self.signing_checkpoints.items():
            private_write(home / self.KEY_FILE, key)
            private_write(home / self.STATE_FILE, state)
        self.signers_reactivated = True
        return self._watermark_evidence()

    def restore(self):
        self._require_closed_fleet()
        if self.backups is None or self.restored:
            raise RuntimeError("restore requires one unused fleet checkpoint")
        restored = {}
        for home in self.services:
            candidate = self.root / f"restored-{home.name}"
            restored[home.name] = copy_offline_home(self.root / "backups" / home.name, candidate)
            if restored[home.name] != self.backups[home.name]:
                raise RuntimeError("restored validator home differs from its offline backup")
        for home in self.services:
            home.rename(self.root / f"upgraded-{home.name}")
            (self.root / f"restored-{home.name}").rename(home)
        self.replace_binaries(self.root_binary, self.binary_sha256)
        self.restored = True
        return restored

    def evidence(self):
        return {home.name: service.evidence() for home, service in self.services.items()}

    def __exit__(self, *_):
        guard = self.signals.finish_owned_operation() if self.signals else nullcontext()
        with guard:
            errors = []
            for service in reversed(tuple(self.services.values())):
                try:
                    self.stop(service)
                except (RuntimeError, OSError, subprocess.SubprocessError) as error:
                    errors.append(str(error))
            if errors:
                raise RuntimeError("could not stop all isolated validator services: " + "; ".join(errors))
            shutil.rmtree(self.root)
