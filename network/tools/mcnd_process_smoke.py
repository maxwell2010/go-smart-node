"""Disposable daemon lifecycle gate; importing this module never runs a node."""
import argparse, hashlib, json, os, pathlib, signal, socket, subprocess, tempfile, time, urllib.request
from contextlib import contextmanager, nullcontext
HTTP = urllib.request.build_opener(urllib.request.ProxyHandler({}))

class TerminationRequested(RuntimeError):
    pass

class SignalGuard:

    def __init__(self):
        self.previous = {}
        self.requested = None
        self.protected, self.unwinding = 0, False

    def handle(self, number, _frame):
        if self.requested is None:
            self.requested = number
        self.check()

    def check(self):
        if self.requested is not None and not self.protected and not self.unwinding:
            self.unwinding = True
            raise TerminationRequested('termination requested: signal ' + str(self.requested))

    @contextmanager
    def finish_owned_operation(self):
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

def private_namespace():
    if os.name != 'posix' or not pathlib.Path('/proc/self/ns/net').exists():
        raise RuntimeError('Linux network namespace required')
    if os.readlink('/proc/self/ns/net') == os.readlink('/proc/1/ns/net'):
        raise RuntimeError('private network namespace required; use unshare --net and bring loopback up')
    if sorted((name for _, name in socket.if_nameindex())) != ['lo']:
        raise RuntimeError('only loopback interface is permitted')
    if len(pathlib.Path('/proc/net/route').read_text().splitlines()) != 1:
        raise RuntimeError('routing table must be empty')

def private_environment(root):
    home = root / 'tool-home'
    temporary = root / 'tmp'
    home.mkdir(mode=0o700)
    temporary.mkdir(mode=0o700)
    return {'PATH': os.environ.get('PATH', '/usr/bin:/bin'), 'LANG': 'C.UTF-8', 'TZ': 'UTC', 'HOME': str(home), 'TMPDIR': str(temporary), 'TMP': str(temporary), 'TEMP': str(temporary), 'GOMAXPROCS': '2', 'NO_PROXY': '127.0.0.1,localhost'}

class ProcessOwner:

    def __init__(self, signals=None):
        self.children = []
        self.handles = []
        self.signals = signals

    def spawn(self, args, path, env):
        with self.signals.finish_owned_operation() if self.signals else nullcontext():
            log = open(path, 'w', encoding='utf8')
            self.handles.append(log)
            child = subprocess.Popen(args, env=env, stdout=log, stderr=log)
            self.children.append(child)
        return child

    def stop(self, child):
        if child.poll() is None:
            child.send_signal(signal.SIGINT)
        try:
            child.wait(timeout=20)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait(timeout=10)
            raise

    def close(self):
        with self.signals.finish_owned_operation() if self.signals else nullcontext():
            self._close_all()

    def _close_all(self):
        errors = []
        for child in reversed(self.children):
            try:
                self.stop(child)
            except Exception as error:
                errors.append('child cleanup: ' + type(error).__name__)
                try:
                    if child.poll() is None:
                        child.kill()
                        child.wait(timeout=10)
                except Exception as fallback:
                    errors.append('child kill/wait: ' + type(fallback).__name__)
        for handle in self.handles:
            try:
                handle.close()
            except Exception as error:
                errors.append('log cleanup: ' + type(error).__name__)
        if errors:
            raise RuntimeError('owned process cleanup failed: ' + '; '.join(errors))

def run_command(binary, home, args, env, secret=False):
    try:
        p = subprocess.run([binary, *args, '--home', str(home)], capture_output=True, text=True, timeout=60, env=env)
    except (OSError, subprocess.SubprocessError) as error:
        if secret:
            raise RuntimeError('private key command failed (' + type(error).__name__ + '); output suppressed') from None
        raise
    if p.returncode:
        detail = 'key generation output suppressed' if secret else (p.stdout + p.stderr)[-5000:]
        raise RuntimeError(f'command {args[:2]} failed ({p.returncode}): ' + detail)
    return p.stdout

def execute(args, binary, home, env, report, owner):
    chain = 'mintcandy_990001-1'
    evm = '990001'

    def run(values, secret=False):
        return run_command(binary, home, values, env, secret)
    run(['init', 'disposable', '--chain-id', chain, '--evm.evm-chain-id', evm])
    if args.history_role:
        from configure_history import configure
        configure(home, apply=True, role=args.history_role)
        report['historyRole'] = args.history_role
        report['checks'].append('fresh-home retention configuration applied; block pruning window not exercised')
    genesis = json.loads((home / 'config/genesis.json').read_text())
    state = genesis['app_state']
    assert genesis['chain_id'] == chain
    assert state['staking']['params']['bond_denom'] == 'del'
    assert state['mint']['params']['mint_denom'] == 'del'
    assert float(state['mint']['params']['inflation_max']) == 0
    assert state['bank']['balances'] == []
    report['checks'].append('owned empty-balance del genesis and inert generic mint')
    key = json.loads(run(['keys', 'add', 'validator', '--coin-type', '60', '--keyring-backend', 'test', '--output', 'json'], True))
    address = key['address']
    assert address.startswith('d0')
    public = key['pubkey']
    public = json.loads(public) if isinstance(public, str) else public
    assert 'ethsecp256k1' in public['@type'], public['@type']
    alphabet = 'qpzry9x8gf2tvdw0s3jn54khce6mua7l'
    hrp, encoded = address.rsplit('1', 1)
    assert hrp == 'd0'
    bits = ''.join((f'{alphabet.index(c):05b}' for c in encoded[:-6]))
    assert len(bits) == 160
    eth_address = '0x' + bytes((int(bits[n:n + 8], 2) for n in range(0, 160, 8))).hex()
    report['operatorAddress'] = address
    report['operatorEthereumAddress'] = eth_address
    report['operatorKeyType'] = 'eth_secp256k1'
    report['relayerKeyType'] = 'secp256k1'
    receiver = json.loads(run(['keys', 'add', 'receiver', '--key-type', 'secp256k1', '--coin-type', '60', '--keyring-backend', 'test', '--output', 'json'], True))['address']
    run(['genesis', 'add-genesis-account', address, '1000000000000000000000000del'])
    run(['genesis', 'reconcile-native-allocation'])
    run(['genesis', 'gentx', 'validator', '1000000000000000000del', '--chain-id', chain, '--keyring-backend', 'test'])
    run(['genesis', 'collect-gentxs'])
    run(['genesis', 'validate-genesis'])
    report['checks'].append('Ethereum operator key with coin60, funded genesis and native signed validator gentx')
    start = ['start', '--chain-id', chain, '--evm.evm-chain-id', evm, '--rpc.laddr', 'tcp://127.0.0.1:28657', '--p2p.laddr', 'tcp://127.0.0.1:28656', '--grpc.address', '127.0.0.1:9190', '--api.enable=false', '--grpc-web.enable=false', '--json-rpc.enable=true', '--json-rpc.address', '127.0.0.1:8645', '--json-rpc.ws-address', '127.0.0.1:8646', '--log_level', 'error']

    def bank_balance(owner):
        balances = json.loads(run(['query', 'bank', 'balances', owner, '--node', 'tcp://127.0.0.1:28657', '--output', 'json']))
        return next((int(c['amount']) for c in balances['balances'] if c['denom'] == 'del'), 0)

    def account_sequence(owner):
        account = json.loads(run(['query', 'auth', 'account-info', owner, '--node', 'tcp://127.0.0.1:28657', '--output', 'json']))['info']
        return int(account.get('sequence', '0'))

    def evm_nonce(owner):
        req = urllib.request.Request('http://127.0.0.1:8645', data=json.dumps({'jsonrpc': '2.0', 'id': 1, 'method': 'eth_getTransactionCount', 'params': [owner, 'latest']}).encode(), headers={'Content-Type': 'application/json'})
        with HTTP.open(req, timeout=2) as response:
            return int(json.load(response)['result'], 16)

    def operate(target, label):
        effective = list(start)
        if label == 'restart':
            for flag in ['--chain-id', '--evm.evm-chain-id']:
                position = effective.index(flag)
                del effective[position:position + 2]
        p = owner.spawn([binary, *effective, '--home', str(home)], home / (label + '.log'), env)
        try:
            deadline = time.monotonic() + 100
            while True:
                if p.poll() is not None:
                    raise RuntimeError('node exited ' + str(p.returncode) + ': ' + (home / (label + '.log')).read_text()[-5000:])
                if time.monotonic() >= deadline:
                    raise RuntimeError('node height deadline')
                try:
                    with HTTP.open('http://127.0.0.1:28657/status', timeout=2) as response:
                        status = json.load(response)
                except (OSError, json.JSONDecodeError):
                    time.sleep(0.3)
                    continue
                height = int(status['result']['sync_info']['latest_block_height'])
                if height >= target:
                    break
                time.sleep(0.3)
            # Readiness retries stop here. Signed transactions and assertion
            # failures must never be retried as transport-readiness errors.
            req = urllib.request.Request('http://127.0.0.1:8645', data=b'{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}', headers={'Content-Type': 'application/json'})
            with HTTP.open(req, timeout=2) as response:
                assert int(json.load(response)['result'], 16) == int(evm)
            report['checks'].append(label + ' actual height ' + str(height) + ' and eth_chainId ' + evm)
            native_params = json.loads(run(['query', 'coin', 'params', '--node', 'tcp://127.0.0.1:28657', '--output', 'json']))['params']
            assert native_params['base_denom'] == 'del' and int(native_params['base_volume']) == 1000000000000000000000000
            native_collections = json.loads(run(['query', 'nft', 'collections', '--node', 'tcp://127.0.0.1:28657', '--output', 'json']))
            assert not native_collections.get('collections')
            report['checks'].append(label + ' mounted native coin allocation and empty NFT query routes')
            if label == 'first-start':
                sender_before = bank_balance(address)
                nonce_before = account_sequence(address)
                assert evm_nonce(eth_address) == nonce_before, 'native and EVM APIs do not identify the same operator account'
                sent = json.loads(run(['tx', 'bank', 'send', 'validator', receiver, '123del', '--chain-id', chain, '--evm.evm-chain-id', evm, '--node', 'tcp://127.0.0.1:28657', '--keyring-backend', 'test', '--fees', '1000000000000000000del', '--gas', '200000', '--broadcast-mode', 'sync', '--output', 'json', '--yes']))
                assert int(sent.get('code', 0)) == 0, sent
                txhash = sent['txhash']
                committed = False
                for _ in range(100):
                    try:
                        with HTTP.open('http://127.0.0.1:28657/tx?hash=0x' + txhash, timeout=2) as response:
                            receipt = json.load(response)
                    except OSError:
                        time.sleep(0.3)
                        continue
                    if 'result' in receipt:
                        assert int(receipt['result']['tx_result'].get('code', 0)) == 0, receipt
                        committed = True
                        break
                    time.sleep(0.3)
                assert committed, 'native CLI bank transaction was not committed'
                assert bank_balance(receiver) == 123
                assert sender_before - bank_balance(address) == 1000000000000000000 + 123, 'operator debit must equal exact native fee plus transferred value'
                assert account_sequence(address) == nonce_before + 1
                assert evm_nonce(eth_address) == nonce_before + 1, 'native signature must advance the same Ethereum operator account nonce'
                report['checks'].append('Ethereum operator signs native bank tx; exact fee/value debit, receiver balance and SDK/EVM shared nonce')
            return height
        finally:
            owner.stop(p)
    height = operate(3, 'first-start')
    operate(height + 2, 'restart')

    def snapshot():
        return {str(p.relative_to(home)): {'sha256': hashlib.sha256(p.read_bytes()).hexdigest(), 'size': p.stat().st_size, 'mtimeNs': p.stat().st_mtime_ns, 'mode': p.stat().st_mode} for p in sorted(home.rglob('*')) if p.is_file()}
    before = snapshot()
    bad = subprocess.run([binary, 'start', '--home', str(home), '--chain-id', 'mintcandy_990002-1', '--evm.evm-chain-id', '990002'], capture_output=True, text=True, timeout=20, env=env)
    report['negativeRestart'] = {'exitCode': bad.returncode, 'stderr': bad.stderr[-1000:]}
    assert bad.returncode != 0 and 'genesis' in bad.stderr
    assert snapshot() == before, 'rejected restart modified node home'
    report['checks'].append('mismatched restart rejected without modifying home')
    report['status'] = 'PASS'

def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description='Bounded mcnd lifecycle gate for a disposable home in a private Linux network namespace')
    parser.add_argument('--binary', required=True)
    parser.add_argument('--report', required=True)
    parser.add_argument('--home-parent', default=tempfile.gettempdir())
    parser.add_argument('--history-role', choices=['rpc-ibc', 'consensus'])
    args = parser.parse_args(argv)
    output = pathlib.Path(args.report)
    output.parent.mkdir(parents=True, exist_ok=True)
    report = {'status': 'FAIL', 'checks': [], 'chainID': 'mintcandy_990001-1', 'evmChainID': 990001}
    with output.open('x', encoding='utf8') as stream:
        try:
            private_namespace()
            binary = str(pathlib.Path(args.binary).resolve(strict=True))
            report['binarySha256'] = hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest()
            parent = pathlib.Path(args.home_parent)
            parent.mkdir(parents=True, exist_ok=True)
            with tempfile.TemporaryDirectory(prefix='mcnd-smoke-', dir=parent) as directory:
                root = pathlib.Path(directory)
                home = root / 'node'
                report['home'] = str(home)
                env = private_environment(root)
                owner = ProcessOwner(signals)
                try:
                    execute(args, binary, home, env, report, owner)
                finally:
                    owner.close()
        except Exception as error:
            report['status'] = 'FAIL'
            report['error'] = str(error)
        finally:
            json.dump(report, stream, indent=2)
    print(json.dumps(report, indent=2))
    return 0 if report['status'] == 'PASS' else 1
if __name__ == '__main__':
    with SignalGuard() as signals:
        raise SystemExit(main(signals=signals))
