"""Commit reserve trades on the private network and compare EVM, bank and native coin state."""
import concurrent.futures
from collections import defaultdict
import json
import subprocess
import time

from del_delegation_smoke import read_words
from native_consensus_smoke import wait_replicas
from native_launch_media import commit_media_transaction
from withdrawal_genesis_smoke import check, bech32_payload, ethereum, native, private_bytes, request

UNIT = 10**18


def rehearse_trades(signer, root, nodes, expected, env, report, token, other):
    check(expected['nativeDenom'] == 'tmsp' and report['loopbackOnlyNamespace'], 'isolated testnet required')
    source, observer = nodes[0], nodes[-1]
    directory = root / 'reserve-trades'
    directory.mkdir(mode=0o700)
    founder = expected['actor'].lower()
    other = other.lower()
    owners = [founder, other]
    nonce = [str(int(ethereum('eth_getTransactionCount', [a, 'latest'], base=source.evm), 16)) for a in owners]
    subprocess.run([str(signer), '-test.run=^TestSignTestnetTrades$'], check=True, timeout=35,
                   env=dict(env, MCND_TRADE_FIXTURE=str(directory), MCND_TRADE_TOKEN=token,
                            MCND_TESTNET_TARIFF_FOUNDER_FILE=str(root.parent / 'tariff-founder.key'),
                            MCND_TRADE_NONCE_A=nonce[0], MCND_TRADE_NONCE_B=nonce[1]))
    ops = json.loads(private_bytes(directory/'trades.json', 70000))
    results = []

    def state(node, height):
        addr = token.lower()
        def words(data):
            return read_words(addr, data, height, 1, node.evm)[0]
        def balance(account):
            return int(ethereum('eth_getBalance', [account, hex(height)], base=node.evm), 16)
        registry, _ = request(node.rest + f'/mintcandy/markets/v1/reserve?token={addr}&height={height}')
        vol, res = words('0x18160ddd'), words('0xcd3293de')
        check(registry['height'] == height and registry['token'].lower() == addr and
              registry['supply_atoms'] == str(vol) and registry['reserve_atoms'] == str(res) and balance(addr) == res,
              'registry, EVM and bank reserve snapshot differ')
        check(registry['max_supply_atoms'] == str(1000*UNIT) and registry['min_supply_atoms'] == str(UNIT)
              and registry['min_reserve_atoms'] == str(UNIT) and registry['crr'] == 100, 'unexpected curve bounds')
        supply = int(native('/cosmos/bank/v1beta1/supply/by_denom?denom=tmsp', height, base=node.rest)['amount']['amount'])
        return dict(volume=vol, reserve=res, native_supply=supply, wallets={a: balance(a) for a in owners},
                    tokens={a: words('0x70a08231' + a[2:].rjust(64,'0')) for a in owners})

    def verify(batch):
        height = int(batch[0][1]['blockNumber'], 16)
        before = state(observer, height-1)
        after = state(observer, height)
        check(state(source,height)==after, 'validator and observer disagree on trade')
        for event in observer.rpc(f'block_results?height={height}').get('finalize_block_events') or []:
            if event['type']=='decimal_native_fee_reward':
                fields={a['key']:a['value'] for a in event['attributes']}
                addr=bech32_payload(fields['recipient'])
                if addr in owners:
                    check(fields['amount'].endswith('tmsp'),'wrong payout denom')
                    before['wallets'][addr]+=int(fields['amount'][:-4])
        for op, rc in sorted(batch,key=lambda item:int(item[1]['transactionIndex'],16)):
            buyer,recipient=op['creator'].lower(),op['recipient'].lower()
            buy=op['side']=='buy'
            amount=int(op['input'])
            selector='0x6cc94a2b' if buy else '0xc193d62e'
            quote=read_words(token,selector+f'{amount:064x}',height-1,1,observer.evm)[0]
            check(quote==(amount*10 if buy else amount//10),'curve quote differs from exact fixture')
            status=int(rc['status'],16)
            before['wallets'][buyer]-=int(rc['gasUsed'],16)*int(rc['effectiveGasPrice'],16)
            if status:
                out=quote
                check(out>=int(op['min_out']) and len(rc['logs'])==2,'trade minOut or event count mismatch')
                if buy:
                    before['wallets'][buyer]-=amount
                    before['tokens'][recipient]+=out
                    before['volume']+=out
                    before['reserve']+=amount
                else:
                    before['tokens'][buyer]-=amount
                    before['wallets'][recipient]+=out
                    before['volume']-=amount
                    before['reserve']-=out
            else:
                check(not rc['logs'],'failed trade left logs')
            results.append({'case':op['label'],'height':height,'hash':op['hash'],'status':status,'quote_atoms':str(quote)})
        check(before==after, f'balance, reserve, native supply or token volume mismatch after block {height}')
        if any(op['label'].startswith('race-') and int(rc['status'],16) for op,rc in batch):
            check(after['volume']==1000*UNIT,'successful boundary trade must reach the exact cap')
    for op in ops[:-2]:
        report['phase']='market-'+op['label']
        rc=commit_media_transaction(source,nodes,op,op['nonce'],report,status=int(op['success']))
        verify([(op,rc)])
    report['phase']='market-race'
    last=ops[-2:]
    def submit(op):
        return ethereum('eth_sendRawTransaction',[op['raw']],base=source.evm)
    with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
        check(list(pool.map(submit,last))==[op['hash'] for op in last],'competing trades missing')
    groups=defaultdict(list)
    statuses=[]
    for op in last:
        deadline=time.monotonic()+45
        rc=None
        while time.monotonic()<deadline and rc is None:
            rc=ethereum('eth_getTransactionReceipt',[op['hash']],base=source.evm)
            if rc is None: time.sleep(.25)
        check(rc is not None,'trade receipt unavailable')
        statuses.append(int(rc['status'],16))
        groups[int(rc['blockNumber'],16)].append((op,rc))
    wait_replicas(nodes,max(groups)+2,65,report)
    check(sorted(statuses)==[0,1],'remaining supply was consumed twice or never filled')
    for batch in groups.values():
        verify(batch)
    report['reserveTrades']={'token':token,'outcomes':results,'unit':'tmsp','historicalProjectionVerified':True}

