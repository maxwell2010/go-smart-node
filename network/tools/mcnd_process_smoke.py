import argparse,hashlib,json,os,pathlib,signal,subprocess,tempfile,time,urllib.request
parser=argparse.ArgumentParser(description="Bounded mcnd lifecycle gate for a new disposable home in a private Linux network namespace")
parser.add_argument("--binary",required=True)
parser.add_argument("--report",required=True)
parser.add_argument("--home-parent",default=tempfile.gettempdir())
args=parser.parse_args()
pathlib.Path(args.report).parent.mkdir(parents=True,exist_ok=True)
if os.readlink("/proc/self/ns/net")==os.readlink("/proc/1/ns/net"):
 raise SystemExit("private network namespace required; use unshare --net and bring loopback up")
binary=str(pathlib.Path(args.binary).resolve(strict=True))
pathlib.Path(args.home_parent).mkdir(parents=True,exist_ok=True)
home=pathlib.Path(tempfile.mkdtemp(prefix='mcnd-smoke-',dir=args.home_parent))
chain='mintcandy_990001-1'; evm='990001'
def run(args, secret=False):
 p=subprocess.run([binary,*args,'--home',str(home)],capture_output=True,text=True,timeout=60)
 if p.returncode:
  detail='key generation output suppressed' if secret else (p.stdout+p.stderr)[-5000:]
  raise RuntimeError(f'command {args[:2]} failed ({p.returncode}): '+detail)
 return p.stdout
report={'binarySha256':hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),'home':str(home),'chainID':chain,'evmChainID':int(evm),'checks':[]}
run(['init','disposable','--chain-id',chain,'--evm.evm-chain-id',evm])
genesis=json.loads((home/'config/genesis.json').read_text());state=genesis['app_state']
assert genesis['chain_id']==chain
assert state['staking']['params']['bond_denom']=='del'
assert state['mint']['params']['mint_denom']=='del'
assert float(state['mint']['params']['inflation_max'])==0
assert state['bank']['balances']==[]
report['checks'].append('owned empty-balance del genesis and inert generic mint')
key=json.loads(run(['keys','add','validator','--key-type','secp256k1','--coin-type','60','--keyring-backend','test','--output','json'],True))
address=key['address']; assert address.startswith('d0')
receiver=json.loads(run(['keys','add','receiver','--key-type','secp256k1','--coin-type','60','--keyring-backend','test','--output','json'],True))['address']
run(['genesis','add-genesis-account',address,'1000000000000000000000000del'])
run(['genesis','gentx','validator','1000000000000000000del','--chain-id',chain,'--keyring-backend','test'])
run(['genesis','collect-gentxs'])
run(['genesis','validate-genesis'])
report['checks'].append('native secp256k1 key, funded genesis and signed validator gentx')
start=['start','--chain-id',chain,'--evm.evm-chain-id',evm,'--rpc.laddr','tcp://127.0.0.1:28657','--p2p.laddr','tcp://127.0.0.1:28656','--grpc.address','127.0.0.1:9190','--api.enable=false','--grpc-web.enable=false','--json-rpc.enable=true','--json-rpc.address','127.0.0.1:8645','--json-rpc.ws-address','127.0.0.1:8646','--log_level','error']
def operate(target, label):
 log=open(home/(label+'.log'),'w')
 p=subprocess.Popen([binary,*start,'--home',str(home)],stdout=log,stderr=log)
 try:
  deadline=time.monotonic()+100
  while time.monotonic()<deadline:
   if p.poll() is not None: raise RuntimeError('node exited '+str(p.returncode)+': '+(home/(label+'.log')).read_text()[-5000:])
   try:
    with urllib.request.urlopen('http://127.0.0.1:28657/status',timeout=2) as response: status=json.load(response)
    height=int(status['result']['sync_info']['latest_block_height'])
    if height>=target:
     req=urllib.request.Request('http://127.0.0.1:8645',data=b'{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}',headers={'Content-Type':'application/json'})
     with urllib.request.urlopen(req,timeout=2) as response: assert int(json.load(response)['result'],16)==int(evm)
     report['checks'].append(label+' actual height '+str(height)+' and eth_chainId '+evm)
     if label=='first-start':
      sent=json.loads(run(['tx','bank','send','validator',receiver,'123del','--chain-id',chain,'--evm.evm-chain-id',evm,'--node','tcp://127.0.0.1:28657','--keyring-backend','test','--fees','1000000000000000000del','--gas','200000','--broadcast-mode','sync','--output','json','--yes']))
      assert int(sent.get('code',0))==0,sent
      txhash=sent['txhash']; committed=False
      for _ in range(100):
       try:
        with urllib.request.urlopen('http://127.0.0.1:28657/tx?hash=0x'+txhash,timeout=2) as response: receipt=json.load(response)
        if 'result' in receipt:
         assert int(receipt['result']['tx_result']['code'])==0,receipt
         committed=True;break
       except OSError:pass
       time.sleep(.3)
      assert committed,'native CLI bank transaction was not committed'
      balances=json.loads(run(['query','bank','balances',receiver,'--node','tcp://127.0.0.1:28657','--output','json']))
      assert next(int(c['amount']) for c in balances['balances'] if c['denom']=='del')==123,balances
      report['checks'].append('native bank CLI signing/broadcast/commit and stateless AutoCLI balance query')
     return height
   except (OSError,ValueError,KeyError): pass
   time.sleep(.3)
  raise RuntimeError('node height deadline')
 finally:
  if p.poll() is None:p.send_signal(signal.SIGINT)
  try:p.wait(timeout=20)
  except subprocess.TimeoutExpired:p.kill();p.wait();raise
  log.close()
height=operate(3,'first-start')
operate(height+2,'restart')
def snapshot():return {str(p.relative_to(home)):{'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'size':p.stat().st_size,'mtimeNs':p.stat().st_mtime_ns,'mode':p.stat().st_mode} for p in sorted(home.rglob('*')) if p.is_file()}
before=snapshot()
bad=subprocess.run([binary,'start','--home',str(home),'--chain-id','mintcandy_990002-1','--evm.evm-chain-id','990002'],capture_output=True,text=True,timeout=20)
report['negativeRestart']={'exitCode':bad.returncode,'stderr':bad.stderr[-1000:]}
pathlib.Path(args.report).write_text(json.dumps(report,indent=2)+'\n')
assert bad.returncode!=0 and 'genesis' in bad.stderr
assert snapshot()==before,'rejected restart modified node home'
report['checks'].append('mismatched restart rejected without modifying home')
report['status']='PASS'
pathlib.Path(args.report).write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report,indent=2))
