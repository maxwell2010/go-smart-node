#!/usr/bin/env python3
"""Disposable real-process IBC lab. Never use public homes, endpoints or keys."""
import argparse
import base64
import calendar
from contextlib import contextmanager, nullcontext
import hashlib
import json
import os
from pathlib import Path
import re
import signal
import socket
import subprocess
import tempfile
import time
import urllib.request

HERMES_SHA256 = "ed8d57781f57a93e74259c12c8feee661b9633c882307f3a6236e41dbf5ffe35"
A_ID, B_ID, EVM_ID = "mintcandy_990001-1", "ibc-reference-1", 990001
UPGRADE_NAME = "ibc-client-proof-rehearsal"
UPGRADE_UNBONDING = 21 * 24 * 60 * 60 + 60 * 60
UPGRADE_DEPOSIT = 10**7
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


class TerminationRequested(RuntimeError):
    pass


class SignalGuard:
    """Unwind once through owned-process cleanup, then ignore repeated stops."""
    def __init__(self):
        self.previous, self.requested = {}, None
        self.protected, self.unwinding = 0, False

    def handle(self, number, _frame):
        if self.requested is None:
            self.requested = number
        self.check()

    def check(self):
        if self.requested is not None and not self.protected and not self.unwinding:
            self.unwinding = True
            raise TerminationRequested("termination requested: signal " + str(self.requested))

    @contextmanager
    def finish_owned_operation(self):
        # Finish ownership registration or all cleanup attempts before raising
        # the first recorded stop. Repeated stops cannot interrupt that unwind.
        self.protected += 1
        try:
            yield
        finally:
            self.protected -= 1
            self.check()

    def __enter__(self):
        for number in (signal.SIGINT, signal.SIGTERM):
            self.previous[number] = signal.getsignal(number)
            signal.signal(number, self.handle)
        return self

    def __exit__(self, *_):
        for number, handler in self.previous.items():
            signal.signal(number, handler)


def private_environment(root):
    home, temporary = root / "tool-home", root / "tmp"
    home.mkdir(mode=0o700)
    temporary.mkdir(mode=0o700)
    return {"PATH": os.environ.get("PATH", "/usr/bin:/bin"), "LANG": "C.UTF-8", "TZ": "UTC",
            "HOME": str(home), "TMPDIR": str(temporary), "TMP": str(temporary), "TEMP": str(temporary),
            "RUST_BACKTRACE": "0", "GOMAXPROCS": "1", "NO_PROXY": "127.0.0.1,localhost"}


def private_namespace():
    require(os.name == "posix" and Path("/proc/self/ns/net").exists(), "Linux network namespace required")
    require(os.readlink("/proc/self/ns/net") != os.readlink("/proc/1/ns/net"), "refuse initial network namespace")
    require(sorted(name for _, name in socket.if_nameindex()) == ["lo"], "only loopback interface is permitted")
    require(len(Path("/proc/net/route").read_text().splitlines()) == 1, "routing table must be empty")


def binary(path, expected):
    path = Path(path).resolve(strict=True)
    require(path.is_file() and os.access(path, os.X_OK), "executable required")
    actual = hashlib.sha256(path.read_bytes()).hexdigest()
    require(re.fullmatch(r"[0-9a-f]{64}", expected or "") and actual == expected, "binary SHA256 mismatch: " + path.name)
    return path, actual


def command(args, env, timeout=45, secret=False, stdin=None):
    try:
        result = subprocess.run([str(a) for a in args], input=stdin, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, timeout=timeout)
    except (OSError, subprocess.SubprocessError) as error:
        if secret:
            raise RuntimeError("private key command failed (" + type(error).__name__ + "); output suppressed") from None
        raise
    if result.returncode:
        detail = "private key command output suppressed" if secret else (result.stdout + result.stderr)[-6000:]
        raise RuntimeError("command failed: " + str(args[0]) + " " + str(args[1]) + ": " + detail)
    return result.stdout


def import_hermes_key(hermes, config, chain, mnemonic, hd_path, env):
    return command([hermes, "--config", config, "--json", "keys", "add", "--chain", chain,
                    "--key-name", "lab", "--mnemonic-file", mnemonic, "--hd-path", hd_path],
                   env, timeout=60, secret=True)


def rpc(port, method, **params):
    data = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
    req = urllib.request.Request(f"http://127.0.0.1:{port}", data, {"Content-Type": "application/json"})
    with HTTP.open(req, timeout=4) as response:
        body = json.load(response)
    require("error" not in body, "local RPC error: " + str(body.get("error")))
    return body["result"]


# Wire decoding is restricted to ordinary standard protobuf query responses.
# Consensus proofs and packet verification are executed by the real chain/relayer.
def varint(value):
    out = bytearray()
    while value > 127:
        out.append((value & 127) | 128)
        value >>= 7
    return bytes(out + bytes([value]))


def field(number, value):
    value = value.encode() if isinstance(value, str) else value
    return varint(number * 8 + 2) + varint(len(value)) + value


def fields(data):
    offset = 0
    def read_int():
        nonlocal offset
        value, shift = 0, 0
        while True:
            require(offset < len(data) and shift < 70, "malformed protobuf varint")
            part = data[offset]; offset += 1
            value |= (part & 127) << shift
            if part < 128:
                return value
            shift += 7
    out = {}
    while offset < len(data):
        tag = read_int(); number, wire = tag >> 3, tag & 7
        require(number > 0, "invalid protobuf field")
        if wire == 0:
            value = read_int()
        elif wire in (1, 2, 5):
            size = read_int() if wire == 2 else (8 if wire == 1 else 4)
            require(offset + size <= len(data), "truncated protobuf response")
            value = data[offset:offset + size]; offset += size
        else:
            raise RuntimeError("unsupported protobuf wire type")
        out.setdefault(number, []).append(value)
    return out


def query(port, path, request=b""):
    response = rpc(port, "abci_query", path=path, data=request.hex(), prove=False)["response"]
    require(int(response.get("code", 0)) == 0, "actual ABCI query failed: " + str(response.get("log")))
    return fields(base64.b64decode(response.get("value", "")))


def amount(port, address, denom):
    body = query(port, "/cosmos.bank.v1beta1.Query/Balance", field(1, address) + field(2, denom))
    coin = fields(body[1][0]) if body.get(1) else {}
    return int(coin.get(2, [b"0"])[0])


def escrow(port, denom):
    body = query(port, "/ibc.applications.transfer.v1.Query/TotalEscrowForDenom", field(1, denom))
    coin = fields(body[1][0]) if body.get(1) else {}
    return int(coin.get(2, [b"0"])[0])


def commitments(port, channel):
    body = query(port, "/ibc.core.channel.v1.Query/PacketCommitments", field(1, "transfer") + field(2, channel))
    records = [fields(entry) for entry in body.get(1, [])]
    return [{"sequence": record[3][0], "commitment": record[4][0].hex()} for record in records]


def open_channel(port):
    body = query(port, "/ibc.core.channel.v1.Query/Channels")
    channels = [fields(entry) for entry in body.get(1, [])]
    channels = [entry for entry in channels if entry.get(6) == [b"transfer"] and entry.get(1) == [3]]
    require(len(channels) == 1, "expected exactly one genuine OPEN transfer channel")
    return channels[0][7][0].decode(), fields(channels[0][3][0])[2][0].decode()


class Lab:
    def __init__(self, root, env, report, signals=None):
        self.root, self.env, self.report = root, env, report
        self.children, self.handles, self.ports = [], [], set()
        self.signals = signals

    def port(self):
        while True:
            with socket.socket() as sock:
                sock.bind(("127.0.0.1", 0)); port = sock.getsockname()[1]
            if port not in self.ports:
                self.ports.add(port); return port

    def spawn(self, args, name):
        with self.signals.finish_owned_operation() if self.signals else nullcontext():
            log = open(self.root / (name + ".log"), "w", encoding="utf8")
            self.handles.append(log)
            child = subprocess.Popen([str(a) for a in args], env=self.env, stdout=log, stderr=subprocess.STDOUT)
            self.children.append(child)
        return child

    def stop(self, child):
        if child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=10)
            except subprocess.TimeoutExpired:
                child.kill(); child.wait(timeout=10)

    def close(self):
        with self.signals.finish_owned_operation() if self.signals else nullcontext():
            self._close_all()

    def _close_all(self):
        errors = []
        for child in reversed(self.children):
            try:
                self.stop(child)
            except Exception as error:
                errors.append("child cleanup: " + type(error).__name__)
                try:
                    if child.poll() is None:
                        child.kill()
                        child.wait(timeout=10)
                except Exception as fallback:
                    errors.append("child kill/wait: " + type(fallback).__name__)
        for handle in self.handles:
            try:
                handle.close()
            except Exception as error:
                errors.append("log cleanup: " + type(error).__name__)
        if errors:
            raise RuntimeError("owned process cleanup failed: " + "; ".join(errors))

    def wait(self, condition, message, timeout=45):
        deadline, last = time.monotonic() + timeout, None
        while time.monotonic() < deadline:
            try:
                if condition(): return
            except (RuntimeError, OSError, ValueError) as error:
                last = str(error)
            time.sleep(0.25)
        raise RuntimeError(message + ": " + str(last))

    def height(self, port, chain):
        status = rpc(port, "status")
        require(status["node_info"]["network"] == chain, "wrong node signing domain")
        return int(status["sync_info"]["latest_block_height"])

    def committed_time_ns(self, port):
        timestamp = rpc(port, "status")["sync_info"]["latest_block_time"]
        match = re.fullmatch(r"([0-9-]{10}T[0-9:]{8})(?:\.([0-9]{1,9}))?Z", timestamp)
        require(match is not None, "unexpected committed timestamp encoding")
        seconds = calendar.timegm(time.strptime(match.group(1), "%Y-%m-%dT%H:%M:%S"))
        return seconds * 10**9 + int((match.group(2) or "0").ljust(9, "0"))


def chain_config(chain, rpc_port, grpc_port, prefix, denom, keys):
    return f'''[[chains]]
id = '{chain}'
rpc_addr = 'http://127.0.0.1:{rpc_port}'
grpc_addr = 'http://127.0.0.1:{grpc_port}'
event_source = {{ mode = 'push', url = 'ws://127.0.0.1:{rpc_port}/websocket', batch_delay = '200ms' }}
rpc_timeout = '10s'
trusted_node = false
account_prefix = '{prefix}'
key_name = 'lab'
key_store_folder = '{keys}'
store_prefix = 'ibc'
default_gas = 200000
max_gas = 4000000
gas_price = {{ price = {1000000000 if denom == 'del' else 0.025}, denom = '{denom}' }}
gas_multiplier = 1.3
max_msg_num = 10
max_tx_size = 1048576
clock_drift = '5s'
max_block_time = '30s'
trusting_period = '14days'
trust_threshold = '1/3'
address_type = {{ derivation = 'cosmos' }}
'''


def health_check(run):
    # Hermes can exit zero and print final status=success while per-chain health
    # failed in WARN records. A command exit code alone is not a health gate.
    output = run("health-check")
    documents = [json.loads(line) for line in output.splitlines() if line.strip()]
    warnings = [item for item in documents if item.get("level") in ("WARN", "ERROR")]
    require(not warnings, "Hermes chain health warnings: " + json.dumps(warnings))
    require(any(item.get("status") == "success" for item in documents), "Hermes health result missing")
    return output


def reference_probe(lab, reference, hermes):
    rpc_port, p2p, grpc = lab.port(), lab.port(), lab.port()
    home = lab.root / "reference"
    command([reference, "prepare", "--home", home, "--rpc-port", rpc_port, "--p2p-port", p2p, "--grpc-port", grpc], lab.env, timeout=60)
    marker = json.loads((home / "disposable-ibc-reference.json").read_text())
    require(marker["chain_id"] == B_ID, "wrong generated counterparty")
    child = lab.spawn([reference, "start", "--home", home, "--log_level", "error"], "reference")
    lab.wait(lambda: lab.height(rpc_port, B_ID) >= 3, "reference must advance", 60)
    require(amount(rpc_port, marker["address"], "stake") == 10**18, "unexpected reference account funding")
    require(escrow(rpc_port, "stake") == 0, "new reference escrow must be zero")
    config = lab.root / "hermes.toml"
    config.write_text("[global]\nlog_level = 'warn'\n" + chain_config(B_ID, rpc_port, grpc, "cosmos", "stake", lab.root / "hermes-keys"))
    run = lambda *args: command([hermes, "--config", config, "--json", *args], lab.env, timeout=60)
    import_hermes_key(hermes, config, B_ID, home / "mnemonic.txt", "m/44'/118'/0'/0/0", lab.env)
    health = health_check(run)
    lab.report["reference_probe"] = {"chain": B_ID, "height": lab.height(rpc_port, B_ID), "funded_address": marker["address"], "hermes_health": health}
    print("independent reference and Hermes health: PASS", flush=True)
    return {"rpc": rpc_port, "grpc": grpc, "home": home, "address": marker["address"], "process": child, "start": [reference, "start", "--home", home, "--log_level", "error"]}, config


def patch_toml(path, changes):
    """Edit only a newly generated fixture, failing on unknown config keys."""
    lines, section, found = path.read_text().splitlines(), "", set()
    for index, line in enumerate(lines):
        match = re.match(r"^\s*\[([^]]+)\]\s*$", line)
        if match:
            section = match.group(1)
        match = re.match(r"^\s*([\w-]+)\s*=", line)
        key = (section, match.group(1)) if match else None
        if key in changes:
            require(key not in found, "duplicate fixture config key")
            lines[index] = key[1] + " = " + json.dumps(changes[key])
            found.add(key)
    require(found == set(changes), "unsupported CLI fixture config keys: " + str(set(changes) - found))
    path.write_text("\n".join(lines) + "\n")


def prepare_owned(lab, mcnd, upgrade=False):
    home = lab.root / "owned"
    common = ["--home", home, "--chain-id", A_ID, "--evm.evm-chain-id", EVM_ID]
    command([mcnd, "init", "disposable-owned", *common], lab.env, timeout=60)
    # No stdout/stderr from a key-generating command is ever logged or persisted.
    key = json.loads(command([mcnd, "keys", "add", "lab", "--key-type", "secp256k1", "--coin-type", 60, "--keyring-backend", "test", "--output", "json", *common], lab.env, secret=True))
    require(key.get("mnemonic") and key.get("address", "").startswith("d01"), "native Cosmos secp256k1 fixture key required")
    public = str(key.get("pubkey", ""))
    require("secp256k1" in public and "ethsecp256k1" not in public, "Hermes lab requires native SDK secp256k1")
    mnemonic_file = home / "ephemeral-relayer-mnemonic.txt"
    with open(mnemonic_file, "x", encoding="utf8") as output:
        os.chmod(mnemonic_file, 0o600); output.write(key["mnemonic"] + "\n")
    address = key["address"]
    command([mcnd, "genesis", "add-genesis-account", address, str(10**24) + "del", *common], lab.env)
    command([mcnd, "genesis", "reconcile-native-allocation", *common], lab.env)
    command([mcnd, "genesis", "gentx", "lab", str(10**18) + "del", "--keyring-backend", "test", *common], lab.env)
    command([mcnd, "genesis", "collect-gentxs", *common], lab.env)
    if upgrade:
        genesis = home / "config/genesis.json"
        document = json.loads(genesis.read_text())
        params = document["app_state"]["gov"]["params"]
        params["min_deposit"] = [{"denom": "del", "amount": str(UPGRADE_DEPOSIT)}]
        params["voting_period"] = "10s"
        params["expedited_voting_period"] = "5s"
        genesis.write_text(json.dumps(document))
    command([mcnd, "genesis", "validate", *common], lab.env)
    ports = {name: lab.port() for name in ("rpc", "p2p", "grpc")}
    patch_toml(home / "config/config.toml", {
        ("rpc", "laddr"): f"tcp://127.0.0.1:{ports['rpc']}",
        ("p2p", "laddr"): f"tcp://127.0.0.1:{ports['p2p']}",
        ("p2p", "seeds"): "", ("p2p", "persistent_peers"): "", ("p2p", "pex"): False,
        ("consensus", "timeout_commit"): "500ms",
    })
    patch_toml(home / "config/app.toml", {
        ("", "minimum-gas-prices"): "1000000000del",
        ("grpc", "enable"): True, ("grpc", "address"): f"127.0.0.1:{ports['grpc']}",
        ("api", "enable"): False, ("grpc-web", "enable"): False,
        ("json-rpc", "enable"): False,
    })
    start = [mcnd, "start", *common, "--address", f"tcp://127.0.0.1:{lab.port()}", "--log_level", "error"]
    child = lab.spawn(start, "owned")
    lab.wait(lambda: lab.height(ports["rpc"], A_ID) >= 3, "owned node must advance", 60)
    require(amount(ports["rpc"], address, "del") > 10**23, "owned account funding unexpectedly missing")
    require(escrow(ports["rpc"], "del") == 0, "new owned escrow must be zero")
    lab.report["owned_probe"] = {"chain": A_ID, "height": lab.height(ports["rpc"], A_ID), "funded_address": address}
    print("owned production daemon lifecycle: PASS", flush=True)
    return {**ports, "home": home, "address": address, "mnemonic": mnemonic_file, "process": child, "start": start}


def receipts(chain):
    result = rpc(chain["rpc"], "tx_search", query="tx.height > 0", prove=False, page="1", per_page="100", order_by="asc")
    require(int(result["total_count"]) <= 100, "bounded lab receipt limit exceeded")
    return {tx["hash"]: tx for tx in (result.get("txs") or [])}


def new_events(chain, before, kind):
    return [{item["key"]: item["value"] for item in event["attributes"]}
            for tx_hash, tx in receipts(chain).items() if tx_hash not in before
            for event in tx["tx_result"].get("events", []) if event["type"] == kind]


def fees(chain, before, denom):
    paid, records = 0, []
    for tx_hash, tx in receipts(chain).items():
        if tx_hash in before:
            continue
        result = tx["tx_result"]
        require(int(result.get("code", 0)) == 0 and int(result["gas_used"]) > 0, "relayer committed failed/zero-gas transaction")
        fee_events = []
        for event in result.get("events", []):
            if event["type"] == "tx":
                attributes = {item["key"]: item["value"] for item in event["attributes"]}
                if "fee" in attributes:
                    fee_events.append(attributes)
        require(len(fee_events) == 1, "receipt must expose exactly one native fee event")
        event = fee_events[0]
        require(event.get("fee_payer") == chain["address"], "unexpected relayer fee payer")
        match = re.fullmatch(r"([0-9]+)" + re.escape(denom), event["fee"])
        require(match and int(match.group(1)) > 0, "actual positive native fee required")
        value = int(match.group(1)); paid += value
        records.append({"hash": tx_hash, "height": tx["height"], "gas_used": result["gas_used"], "fee": value, "denom": denom})
    return paid, records


def phase_before(a, b):
    return {"a": amount(a["rpc"], a["address"], "del"), "b": amount(b["rpc"], b["address"], "stake"), "a_tx": receipts(a), "b_tx": receipts(b)}


def phase_after(lab, label, a, b, before, native_a_delta=0):
    # Let the independent Comet indexers catch up before checking receipt fees.
    heights = [lab.height(a["rpc"], A_ID), lab.height(b["rpc"], B_ID)]
    lab.wait(lambda: lab.height(a["rpc"], A_ID) > heights[0] and lab.height(b["rpc"], B_ID) > heights[1], "post-transaction indexing block")
    a_fee, a_records = fees(a, before["a_tx"], "del")
    b_fee, b_records = fees(b, before["b_tx"], "stake")
    require(a_records or b_records, "phase must contain real committed relayer transactions")
    require(amount(a["rpc"], a["address"], "del") == before["a"] + native_a_delta - a_fee, "exact owned balance/principal/fee mismatch: " + label)
    require(amount(b["rpc"], b["address"], "stake") == before["b"] - b_fee, "exact counterparty native fee mismatch: " + label)
    lab.report.setdefault("phases", {})[label] = {"status": "PASS", "owned_fees": a_records, "counterparty_fees": b_records}
    print(label + ": PASS", flush=True)


def connect_transfer(lab, mcnd, reference, hermes, upgrade=False):
    b, config = reference_probe(lab, reference, hermes)
    a = prepare_owned(lab, mcnd, upgrade=upgrade)
    config.write_text("[global]\nlog_level = 'warn'\n" + chain_config(A_ID, a["rpc"], a["grpc"], "d0", "del", lab.root / "hermes-keys") + chain_config(B_ID, b["rpc"], b["grpc"], "cosmos", "stake", lab.root / "hermes-keys"))
    run = lambda *args: command([hermes, "--config", config, "--json", *args], lab.env, timeout=120)
    import_hermes_key(hermes, config, A_ID, a["mnemonic"], "m/44'/60'/0'/0/0", lab.env)
    health_check(run)
    before = phase_before(a, b)
    run("create", "channel", "--a-chain", A_ID, "--b-chain", B_ID, "--a-port", "transfer", "--b-port", "transfer", "--new-client-connection", "--channel-version", "ics20-1", "--yes")
    a_channel, a_counterparty = open_channel(a["rpc"])
    b_channel, b_counterparty = open_channel(b["rpc"])
    require(a_counterparty == b_channel and b_counterparty == a_channel, "actual channel counterparties differ")
    phase_after(lab, "client_connection_channel_handshake", a, b, before)
    lab.report["channel"] = {"a": a_channel, "b": b_channel}
    return a, b, run, a_channel, b_channel


def full_suite(lab, mcnd, reference, hermes):
    a, b, run, a_channel, b_channel = connect_transfer(lab, mcnd, reference, hermes)
    voucher = "ibc/" + hashlib.sha256(("transfer/" + b_channel + "/del").encode()).hexdigest().upper()
    value = 10**18

    def transfer(source, dest, channel, denom, count, receiver, seconds=300):
        run("tx", "ft-transfer", "--src-chain", source, "--dst-chain", dest, "--src-port", "transfer", "--src-channel", channel, "--denom", denom, "--amount", count, "--receiver", receiver, "--timeout-seconds", seconds)

    def clear():
        run("clear", "packets", "--chain", A_ID, "--port", "transfer", "--channel", a_channel)
        lab.wait(lambda: not commitments(a["rpc"], a_channel) and not commitments(b["rpc"], b_channel), "all actual packet commitments must clear", 60)

    before = phase_before(a, b)
    transfer(A_ID, B_ID, a_channel, "del", value, b["address"])
    lab.wait(lambda: len(commitments(a["rpc"], a_channel)) == 1, "genuine outbound commitment")
    require(escrow(a["rpc"], "del") == value, "outbound native escrow mismatch")
    clear()
    require(amount(b["rpc"], b["address"], voucher) == value, "actual ICS20 voucher not received")
    phase_after(lab, "native_send", a, b, before, -value)

    before = phase_before(a, b)
    transfer(B_ID, A_ID, b_channel, voucher, value, a["address"])
    lab.wait(lambda: len(commitments(b["rpc"], b_channel)) == 1, "genuine return commitment")
    clear()
    require(amount(b["rpc"], b["address"], voucher) == 0 and escrow(a["rpc"], "del") == 0, "voucher return/unescrow mismatch")
    phase_after(lab, "voucher_return", a, b, before, value)

    before = phase_before(a, b)
    transfer(A_ID, B_ID, a_channel, "del", value, b["address"])
    lab.wait(lambda: len(commitments(a["rpc"], a_channel)) == 1, "pending packet before restart")
    pending = commitments(a["rpc"], a_channel)
    heights = [lab.height(a["rpc"], A_ID), lab.height(b["rpc"], B_ID)]
    lab.stop(a["process"]); lab.stop(b["process"])
    a["process"] = lab.spawn(a["start"], "owned-restarted")
    b["process"] = lab.spawn(b["start"], "reference-restarted")
    lab.wait(lambda: lab.height(a["rpc"], A_ID) > heights[0] and lab.height(b["rpc"], B_ID) > heights[1], "both actual processes must reopen and advance", 60)
    require(commitments(a["rpc"], a_channel) == pending and escrow(a["rpc"], "del") == value, "exact in-flight sequence/commitment hash/escrow did not survive restart")
    clear()
    require(amount(b["rpc"], b["address"], voucher) == value, "pending packet failed after disk reopen")
    phase_after(lab, "pending_packet_restart", a, b, before, -value)
    lab.report["phases"]["pending_packet_restart"]["preserved_packets"] = pending

    before = phase_before(a, b)
    transfer(A_ID, B_ID, a_channel, "del", value, "not-a-valid-bech32-address")
    lab.wait(lambda: len(commitments(a["rpc"], a_channel)) == 1, "error-ACK source commitment")
    error_sequence = commitments(a["rpc"], a_channel)[0]["sequence"]
    clear()
    require(amount(b["rpc"], b["address"], voucher) == value and escrow(a["rpc"], "del") == value, "error acknowledgement must refund only its packet")
    phase_after(lab, "error_ack_refund", a, b, before)
    acks = [event for event in new_events(b, before["b_tx"], "write_acknowledgement") if event.get("packet_sequence") == str(error_sequence)]
    require(len(acks) == 1, "actual error acknowledgement event required")
    ack = json.loads(bytes.fromhex(acks[0]["packet_ack_hex"]))
    require(ack.get("error") and "result" not in ack, "expected an actual error ACK, not a success or inferred timeout")
    lab.report["phases"]["error_ack_refund"]["error_ack_sequence"] = error_sequence

    before = phase_before(a, b)
    transfer(A_ID, B_ID, a_channel, "del", value, b["address"], seconds=3)
    lab.wait(lambda: len(commitments(a["rpc"], a_channel)) == 1, "timeout source commitment")
    timeout_sequence = commitments(a["rpc"], a_channel)[0]["sequence"]
    sends = [event for event in new_events(a, before["a_tx"], "send_packet") if event.get("packet_sequence") == str(timeout_sequence)]
    require(len(sends) == 1, "actual outgoing packet deadline required")
    timeout_timestamp = int(sends[0]["packet_timeout_timestamp"])
    require(timeout_timestamp > 0, "timestamp timeout must be enabled")
    # Hermes may add safety margins. Wait on the actual committed counterparty
    # timestamp and emitted packet deadline, never a guessed wall-clock sleep.
    lab.wait(lambda: lab.committed_time_ns(b["rpc"]) > timeout_timestamp + 10**9, "actual counterparty timestamp must exceed packet deadline", 120)
    clear()
    require(amount(b["rpc"], b["address"], voucher) == value and escrow(a["rpc"], "del") == value, "timeout must refund only its packet")
    phase_after(lab, "timeout_refund", a, b, before)
    timeouts = [event for event in new_events(a, before["a_tx"], "timeout_packet") if event.get("packet_sequence") == str(timeout_sequence)]
    require(len(timeouts) == 1, "actual timeout receipt event required")
    lab.report["phases"]["timeout_refund"]["timeout_sequence"] = timeout_sequence
    lab.report["phases"]["timeout_refund"]["packet_timeout_timestamp"] = timeout_timestamp
    require(len(lab.report.get("phases", {})) == 6, "all real two-process phases are mandatory")
    lab.report["status"] = "TWO_PROCESS_HERMES_PASS"
    lab.report["release_gate"] = True
    lab.report["scope"] = "Actual owned/reference IBC interoperability only; native Decimal economic parity is a separate gate."
    lab.report["upgrade_scope"] = "Same-binary disk reopen only; no module migration, client upgrade or SDK major upgrade is verified."


def client_upgrade_suite(lab, mcnd, replacement, reference, hermes):
    a, b, run, a_channel, b_channel = connect_transfer(lab, mcnd, reference, hermes, upgrade=True)
    source, host = a["rpc"], b["rpc"]
    voucher = "ibc/" + hashlib.sha256(("transfer/" + b_channel + "/del").encode()).hexdigest().upper()
    value = 10**18

    def send(source_id, destination_id, channel, denom, receiver):
        run("tx", "ft-transfer", "--src-chain", source_id, "--dst-chain", destination_id,
            "--src-port", "transfer", "--src-channel", channel, "--denom", denom,
            "--amount", value, "--receiver", receiver, "--timeout-seconds", 300)

    def clear():
        run("clear", "packets", "--chain", A_ID, "--port", "transfer", "--channel", a_channel)
        lab.wait(lambda: not commitments(source, a_channel) and not commitments(host, b_channel),
                 "real packet commitments must clear")

    def app_height():
        return int(rpc(source, "abci_info")["response"]["last_block_height"])

    def proposal_state():
        response = query(source, "/cosmos.gov.v1.Query/Proposals")
        require(len(response.get(1, [])) == 1, "expected exactly one rehearsal proposal")
        proposal = fields(response[1][0])
        return proposal[1][0], proposal[3][0]

    channel = fields(query(host, "/ibc.core.channel.v1.Query/Channel",
                           field(1, "transfer") + field(2, b_channel))[1][0])
    require(len(channel.get(4, [])) == 1, "expected one actual host connection")
    connection = fields(query(host, "/ibc.core.connection.v1.Query/Connection", field(1, channel[4][0]))[1][0])
    client = connection[1][0].decode()
    require(re.fullmatch(r"07-tendermint-\d+", client) is not None, "unexpected host light client type")

    def client_state():
        any_state = fields(query(host, "/ibc.core.client.v1.Query/ClientState", field(1, client))[1][0])
        require(any_state[1] == [b"/ibc.lightclients.tendermint.v1.ClientState"], "expected actual Tendermint client")
        state = fields(any_state[2][0])
        require(state[1] == [A_ID.encode()], "host client must track the original source chain")
        return fields(state[7][0])[2][0], fields(state[4][0])[1][0]

    _, old_unbonding = client_state()
    require(old_unbonding == UPGRADE_UNBONDING - 3600, "rehearsal starts from the original unbonding period")
    gov = fields(query(source, "/cosmos.auth.v1beta1.Query/ModuleAccountByName", field(1, "gov"))[1][0])
    gov_account = fields(fields(gov[2][0])[1][0])[1][0].decode()
    require(gov_account.startswith("d01"), "unexpected governance authority address")

    before = phase_before(a, b)
    send(A_ID, B_ID, a_channel, "del", b["address"])
    lab.wait(lambda: len(commitments(source, a_channel)) == 1, "pending packet before governance")
    pending = commitments(source, a_channel)

    def assert_pending():
        require(commitments(source, a_channel) == pending, "pending packet commitment or sequence changed")
        require(escrow(source, "del") == value and amount(host, b["address"], voucher) == 0,
                "principal moved while packet was still pending")

    assert_pending()
    lab.report["upgrade_phase"] = "governance"
    run("tx", "upgrade-chain", "--reference-chain", A_ID, "--host-chain", B_ID, "--host-client", client,
        "--gov-account", gov_account, "--amount", UPGRADE_DEPOSIT, "--denom", "del", "--height-offset", 45,
        "--new-unbonding", UPGRADE_UNBONDING, "--upgrade-name", UPGRADE_NAME)
    lab.wait(lambda: proposal_state()[1] == 2, "upgrade proposal must enter voting")
    proposal_id, _ = proposal_state()
    vote = json.loads(command([mcnd, "tx", "gov", "vote", proposal_id, "yes", "--home", a["home"],
                               "--chain-id", A_ID, "--evm.evm-chain-id", EVM_ID, "--node", f"tcp://127.0.0.1:{source}",
                               "--from", "lab", "--keyring-backend", "test", "--fees", "600000000000000del",
                               "--gas", 600000, "--broadcast-mode", "sync", "--output", "json", "--yes"], lab.env))
    require(int(vote.get("code", 0)) == 0 and vote.get("txhash"), "signed governance vote was rejected")
    lab.wait(lambda: proposal_state()[1] == 3, "actual governance vote must approve the upgrade")
    plan = fields(query(source, "/cosmos.upgrade.v1beta1.Query/CurrentPlan")[1][0])
    require(plan[1] == [UPGRADE_NAME.encode()], "unexpected approved upgrade plan")
    height = plan[3][0]
    require(height > app_height(), "expected an upcoming halt height")
    lab.report["upgrade"] = {"plan": UPGRADE_NAME, "height": height, "proposal_id": proposal_id,
                             "vote_tx": vote["txhash"], "client": client, "preserved_packets": pending,
                             "old_unbonding_seconds": old_unbonding, "new_unbonding_seconds": UPGRADE_UNBONDING}

    lab.report["upgrade_phase"] = "old_daemon_halt"
    hint_path = a["home"] / "data/upgrade-info.json"
    lab.wait(lambda: hint_path.is_file() and app_height() == height - 1 and
             int(rpc(source, "block", height=str(height))["block"]["header"]["height"]) == height,
             "old daemon must halt before executing the signed boundary block", 90)
    hint = json.loads(hint_path.read_text())
    require(hint["name"] == UPGRADE_NAME and int(hint["height"]) == height, "on-disk upgrade hint differs from approved plan")
    boundary = rpc(source, "block", height=str(height))
    block_hash = boundary["block_id"]["hash"]
    app_hash = boundary["block"]["header"]["app_hash"]
    require(re.fullmatch(r"[0-9A-Fa-f]{64}", app_hash) and re.fullmatch(r"[0-9A-Fa-f]{64}", block_hash),
            "missing boundary AppHash or block hash")
    proof_values = {}
    for name in ("upgradedClient", "upgradedConsState"):
        key = f"upgradedIBCState/{height}/{name}".encode()
        response = rpc(source, "abci_query", path="/store/upgrade/key", data=key.hex(),
                       height=str(height - 1), prove=True)["response"]
        proof = response.get("proofOps") or response.get("proof_ops")
        require(int(response.get("code", 0)) == 0 and int(response["height"]) == height - 1 and
                proof and len(proof["ops"]) == 2 and response.get("value"), "missing committed upgrade proof: " + name)
        proof_values[name] = hashlib.sha256(base64.b64decode(response["value"], validate=True)).hexdigest()
    lab.report["upgrade"].update({"old_application_height": height - 1, "boundary_block_hash": block_hash,
                                  "boundary_app_hash": app_hash, "upgrade_value_sha256": proof_values})
    assert_pending()

    lab.report["upgrade_phase"] = "hermes_client_upgrade"
    run("upgrade", "client", "--host-chain", B_ID, "--client", client, "--upgrade-height", height)
    lab.wait(lambda: client_state() == (height + 1, UPGRADE_UNBONDING), "counterparty client must accept the committed proofs")
    def upgrade_events():
        return [event for event in new_events(b, before["b_tx"], "upgrade_client") if event.get("client_id") == client]

    lab.wait(lambda: len(upgrade_events()) == 1, "expected one committed client upgrade receipt")
    require(app_height() == height - 1, "old daemon must remain halted through proof-based client upgrade")
    assert_pending()

    lab.report["upgrade_phase"] = "replacement_replay"
    lab.stop(a["process"])
    a["process"] = lab.spawn([replacement, *a["start"][1:]], "owned-upgrade-replacement")
    lab.wait(lambda: app_height() >= height + 2, "replacement must replay and advance the same application", 90)
    completed = query(source, "/cosmos.upgrade.v1beta1.Query/AppliedPlan", field(1, UPGRADE_NAME))
    require(completed.get(1) == [height], "replacement did not apply the plan at the scheduled height")
    staking = fields(query(source, "/cosmos.staking.v1beta1.Query/Params")[1][0])
    require(fields(staking[1][0])[1] == [UPGRADE_UNBONDING], "replacement staking parameter differs from upgraded client")
    require(rpc(source, "block", height=str(height))["block_id"]["hash"] == block_hash,
            "replacement changed the existing consensus boundary")
    require(open_channel(source) == (a_channel, b_channel) and open_channel(host) == (b_channel, a_channel),
            "original channel pair did not survive the replacement")
    assert_pending()
    run("update", "client", "--host-chain", B_ID, "--client", client)
    lab.wait(lambda: client_state()[0] > height + 1, "Hermes must verify an actual post-upgrade header")

    lab.report["upgrade_phase"] = "pending_packet_ack"
    clear()
    require(escrow(source, "del") == value and amount(host, b["address"], voucher) == value,
            "pending packet must mint exactly one voucher and preserve native escrow")
    phase_after(lab, "pending_packet_client_upgrade", a, b, before, -value)
    acks = [event for event in new_events(b, before["b_tx"], "write_acknowledgement")
            if event.get("packet_sequence") == str(pending[0]["sequence"])]
    require(len(acks) == 1 and json.loads(bytes.fromhex(acks[0]["packet_ack_hex"])).get("result"),
            "pending packet must have one committed success acknowledgement")

    before = phase_before(a, b)
    send(B_ID, A_ID, b_channel, voucher, a["address"])
    clear()
    require(escrow(source, "del") == 0 and amount(host, b["address"], voucher) == 0,
            "voucher return must release the original principal exactly once")
    phase_after(lab, "post_upgrade_voucher_return", a, b, before, value)

    before = phase_before(a, b)
    send(A_ID, B_ID, a_channel, "del", "not-a-valid-bech32-address")
    lab.wait(lambda: len(commitments(source, a_channel)) == 1, "post-upgrade error ACK packet must be pending")
    failed = commitments(source, a_channel)[0]["sequence"]
    clear()
    require(escrow(source, "del") == 0 and amount(host, b["address"], voucher) == 0,
            "error acknowledgement did not refund principal")
    phase_after(lab, "post_upgrade_error_ack_refund", a, b, before)
    errors = [event for event in new_events(b, before["b_tx"], "write_acknowledgement")
              if event.get("packet_sequence") == str(failed)]
    require(len(errors) == 1 and json.loads(bytes.fromhex(errors[0]["packet_ack_hex"])).get("error"),
            "post-upgrade refund must follow one actual error acknowledgement")
    lab.report["upgrade_phase"] = "complete"
    lab.report["status"] = "HERMES_CLIENT_UPGRADE_PASS"
    lab.report["scope"] = "Same-family replacement and genuine proof-based Hermes upgrade in an isolated loopback lab."
    lab.report["upgrade_scope"] = "Test-only handler changes a staking parameter; no production or SDK major migration is claimed."
    print("signed governance, exact halt, Hermes client proof, replacement replay, packet return and refund: PASS", flush=True)


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=["reference-probe", "full", "client-upgrade"], required=True)
    parser.add_argument("--reference-binary", required=True)
    parser.add_argument("--reference-sha256", required=True)
    parser.add_argument("--reference-source-commit", required=True)
    parser.add_argument("--hermes-binary", required=True)
    parser.add_argument("--report", required=True)
    parser.add_argument("--mcnd-binary")
    parser.add_argument("--mcnd-sha256")
    parser.add_argument("--mcnd-source-commit")
    parser.add_argument("--replacement-binary")
    parser.add_argument("--replacement-sha256")
    args = parser.parse_args(argv)
    report = {"status": "FAIL", "mode": args.mode, "release_gate": False}
    try:
        private_namespace()
        require(re.fullmatch(r"[0-9a-f]{40}", args.reference_source_commit), "full reference source commit required")
        reference, ref_hash = binary(args.reference_binary, args.reference_sha256)
        hermes, hermes_hash = binary(args.hermes_binary, HERMES_SHA256)
        report["binaries"] = {"reference": {"sha256": ref_hash, "source_commit": args.reference_source_commit}, "hermes": {"sha256": hermes_hash, "official_release": "v1.13.3"}}
        mcnd = None
        if args.mode in ("full", "client-upgrade"):
            require(args.mcnd_binary and re.fullmatch(r"[0-9a-f]{40}", args.mcnd_source_commit or ""), "full mcnd binary/source commit required")
            mcnd, mcnd_hash = binary(args.mcnd_binary, args.mcnd_sha256)
            report["binaries"]["mcnd"] = {"sha256": mcnd_hash, "source_commit": args.mcnd_source_commit}
        replacement = None
        if args.mode == "client-upgrade":
            require(args.replacement_binary and args.replacement_sha256, "explicit rehearsal binary and hash required")
            replacement, replacement_hash = binary(args.replacement_binary, args.replacement_sha256)
            require(replacement_hash != mcnd_hash, "rehearsal executable must differ from the default daemon")
            report["binaries"]["replacement"] = {"sha256": replacement_hash, "source_commit": args.mcnd_source_commit,
                                                "scope": "same-family lab-only handler"}
        with tempfile.TemporaryDirectory(prefix="mintcandy-hermes-lab-") as temp:
            root = Path(temp)
            env = private_environment(root)
            lab = Lab(root, env, report, signals)
            try:
                report["hermes_version"] = command([hermes, "version"], env).strip()
                if args.mode == "full":
                    full_suite(lab, mcnd, reference, hermes)
                elif args.mode == "client-upgrade":
                    client_upgrade_suite(lab, mcnd, replacement, reference, hermes)
                else:
                    reference_probe(lab, reference, hermes)
                    report["status"] = "REFERENCE_PROBE_PASS"
                    report["remaining"] = ["owned mcnd CLI", "two-process handshake", "send/return/refund", "pending-packet restart"]
            finally:
                lab.close()
    except Exception as error:
        report["status"], report["release_gate"] = "FAIL", False
        report["error"] = str(error)
    finally:
        # A report contains public addresses/receipts only, never fixture mnemonics.
        with open(args.report, "x", encoding="utf8") as output:
            json.dump(report, output, indent=2)
        print(report["status"])
    return 0 if report["status"] in ("REFERENCE_PROBE_PASS", "TWO_PROCESS_HERMES_PASS", "HERMES_CLIENT_UPGRADE_PASS") else 1


if __name__ == "__main__":
    with SignalGuard() as signals:
        raise SystemExit(main(signals=signals))
