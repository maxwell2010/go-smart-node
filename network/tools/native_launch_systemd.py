"""Install and run the disposable launch follower as a transient systemd service."""

from contextlib import nullcontext
import hashlib
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import uuid

from mcnd_process_smoke import private_namespace


def digest(path):
    with Path(path).open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


class SystemdFollower:
    """Own a private installation and a single systemd service for its follower."""

    def __init__(self, binary, expected_binary_sha256, signals=None):
        self.source = Path(binary)
        self.expected_sha256 = expected_binary_sha256
        self.signals = signals
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
        guard = self.signals.finish_owned_operation() if self.signals else nullcontext()
        with guard:
            self.submitted = True
            result = subprocess.run([
                "systemd-run", "--system", "--quiet", "--collect", f"--unit={self.unit}", "--service-type=exec",
                f"--property=User={self.user.pw_name}", f"--property=Group={self.user.pw_gid}",
                f"--property=NetworkNamespacePath={self.namespace_path}", f"--property=WorkingDirectory={self.home}",
                f"--property=StandardOutput=append:{path}", "--property=StandardError=inherit",
                "--property=NoNewPrivileges=yes", "--property=UMask=0077", "--property=KillSignal=SIGINT",
                "--property=TimeoutStartSec=15s", "--property=TimeoutStopSec=20s", "--property=RuntimeMaxSec=10min",
                f"--setenv=HOME={self.home}", f"--setenv=GOMAXPROCS={env['GOMAXPROCS']}", "--", *args,
            ], capture_output=True, text=True, timeout=25)
            if result.returncode:
                raise RuntimeError(f"could not start transient systemd follower: {result.stderr.strip()[:300]}")
        return self

    def poll(self):
        props = self._properties("ActiveState", "MainPID")
        return None if props.get("ActiveState") == "active" and int(props.get("MainPID", "0")) else 1

    def evidence(self):
        props = self._properties("ActiveState", "SubState", "MainPID", "User", "Type", "Transient",
                                 "NetworkNamespacePath")
        if (props.get("ActiveState"), props.get("SubState"), props.get("User"), props.get("Type"),
            props.get("Transient"), props.get("NetworkNamespacePath")) != (
                "active", "running", self.user.pw_name, "exec", "yes", self.namespace_path):
            raise RuntimeError("expected a running transient systemd follower in the isolated namespace")
        pid = int(props["MainPID"])
        if pid <= 1:
            raise RuntimeError("systemd did not report a follower MainPID")
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
                "mainPid": pid, "serviceUid": uid, "networkNamespaceMatches": namespace_matches,
                "binarySha256": binary_sha256}

    def __exit__(self, *_):
        guard = self.signals.finish_owned_operation() if self.signals else nullcontext()
        with guard:
            if self.submitted:
                subprocess.run(["systemctl", "--system", "--quiet", "stop", self.unit],
                               capture_output=True, text=True, timeout=30)
                props = self._properties("LoadState", "ActiveState", "MainPID")
                if props.get("LoadState") != "not-found" and not (
                    props.get("ActiveState") in ("inactive", "failed") and int(props.get("MainPID", "0")) == 0
                ):
                    raise RuntimeError("transient follower service did not stop")
            shutil.rmtree(self.root)
