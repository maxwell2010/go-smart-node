"""Install already verified genesis bytes in a stopped initial validator home."""

import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import tomllib

from check_public_launch_draft import require, unique_object
from configure_history import validate_fresh_home
from native_launch_allocation import MAX_GENESIS_BYTES, allocation_report
from native_launch_package import sha256


COMET_PATHS = {
    'db_dir': 'data',
    'genesis_file': 'config/genesis.json',
    'priv_validator_key_file': 'config/priv_validator_key.json',
    'priv_validator_state_file': 'data/priv_validator_state.json',
    'priv_validator_laddr': '',
    'node_key_file': 'config/node_key.json',
}


def regular(path, limit):
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode) and 0 < info.st_size <= limit, f'invalid {path.name} in validator home')
    return info


def validate_prepared_home(home, chain_id, evm_id):
    require(home.is_dir() and not home.is_symlink(), 'a real initialized validator home is required')
    require(not (home / 'config').is_symlink() and not (home / 'data').is_symlink(),
            'validator home must use local configuration and data directories')
    for name, limit in (
        ('config/genesis.json', MAX_GENESIS_BYTES), ('config/config.toml', 1024**2),
        ('config/app.toml', 1024**2), ('config/client.toml', 1024**2), ('config/node_key.json', 8192),
        ('config/priv_validator_key.json', 8192), ('data/priv_validator_state.json', 8192),
    ):
        regular(home / name, limit)
    validate_fresh_home(home)
    comet = tomllib.loads((home / 'config/config.toml').read_text(encoding='utf8'))
    require(all(comet.get(key) == value for key, value in COMET_PATHS.items()),
            'validator must use the initialized Comet paths and local signer')
    app = tomllib.loads((home / 'config/app.toml').read_text(encoding='utf8'))
    client = tomllib.loads((home / 'config/client.toml').read_text(encoding='utf8'))
    require(str(app.get('evm', {}).get('evm-chain-id')) == evm_id and client.get('chain-id', '') in ('', chain_id),
            'validator application or client signing domain differs from genesis')


def run_offline(binary, home, scratch, *args):
    command = [str(binary), *map(str, args), '--home', str(home)]
    result = subprocess.run(command, cwd=scratch, env={'HOME': str(scratch), 'LANG': 'C.UTF-8'},
                            capture_output=True, text=True, timeout=90)
    require(result.returncode == 0, f'mcnd {args[0]} {args[1]} failed during validator preparation')
    return result.stdout


def install_operator_genesis(binary, home, operator, genesis, verified):
    require(verified.get('status') == 'PUBLIC_GENESIS_SIGNATURE_VERIFIED', 'a verified genesis manifest is required')
    allocation = allocation_report(genesis)
    require(all(verified[key] == allocation[key] for key in ('chainId', 'evmChainId', 'genesisSha256')),
            'verified genesis bytes or signing domain differ')
    matches = [row for row in allocation['validators'] if row['operatorAddress'] == operator]
    require(len(matches) == 1 and matches[0]['declaredOnline'] is True and int(matches[0]['initialStakeAtomic']) > 0,
            'operator has no active starting stake in the verified genesis')
    validator = matches[0]
    chain_id, evm_id = allocation['chainId'], allocation['evmChainId']

    require(not home.is_symlink(), 'a symbolic link cannot be used as the validator home')
    home = home.resolve(strict=True)
    binary = binary.resolve(strict=True)
    require(sha256(binary) == verified['software']['binarySha256'], 'mcnd binary changed after manifest verification')
    validate_prepared_home(home, chain_id, evm_id)
    target = home / 'config/genesis.json'
    original = target.read_bytes()
    # Retain the operator's existing node key and signer. Read their public
    # consensus key through mcnd without replicating the private key to scratch.
    with tempfile.TemporaryDirectory(prefix='mcnd-genesis-check-') as directory:
        scratch = Path(directory)
        public = json.loads(run_offline(binary, home, scratch, 'comet', 'show-validator'), object_pairs_hook=unique_object)
        require(public == {'@type': '/cosmos.crypto.ed25519.PubKey', 'key': validator['consensusPublicKeyBase64']},
                'local consensus key does not match the operator in the verified genesis')

        reference = scratch / 'reference'
        run_offline(binary, reference, scratch, 'init', 'genesis-check', '--chain-id', chain_id, '--evm.evm-chain-id', evm_id)
        if original != genesis:
            local, stock = (json.loads(data, object_pairs_hook=unique_object) for data in (
                original, (reference / 'config/genesis.json').read_bytes(),
            ))
            del local['genesis_time'], stock['genesis_time']
            require(local == stock, 'existing genesis differs from the initialized template; refusing replacement')

        candidate = scratch / 'candidate.json'
        candidate.write_bytes(genesis)
        run_offline(binary, reference, scratch, 'genesis', 'validate-genesis', candidate)

    if original != genesis:
        import fcntl

        staged = tempfile.NamedTemporaryFile(dir=target.parent, prefix='.public-genesis-', delete=False)
        temporary = Path(staged.name)
        try:
            with staged:
                staged.write(genesis)
                staged.flush()
                os.fsync(staged.fileno())
            config_dir = os.open(target.parent, os.O_DIRECTORY | os.O_RDONLY)
            try:
                fcntl.flock(config_dir, fcntl.LOCK_EX)
                validate_prepared_home(home, chain_id, evm_id)
                require(target.read_bytes() == original, 'genesis changed during preparation; refusing replacement')
                os.replace(temporary, target)
                os.fsync(config_dir)
            finally:
                os.close(config_dir)
        finally:
            temporary.unlink(missing_ok=True)

    return {'status': 'VALIDATOR_GENESIS_INSTALLED', 'changed': original != genesis,
            'operatorAddress': operator, 'consensusAddressHex': validator['consensusAddressHex'],
            'genesisSha256': allocation['genesisSha256'], 'binarySha256': verified['software']['binarySha256'],
            'manifestSha256': verified['manifestSha256'], 'signerFingerprint': verified['signerFingerprint'],
            'networkLaunchAuthorized': False}
