import hashlib,json,subprocess,tempfile
from pathlib import Path
out=Path('/mnt/c/MintCandyBuild/mcnd-install-acceptance-8cf619d-20261103')
binary=out/'ci-bin/mcnd'
commit='8cf619da1c77ee5efffc80a678272010fd2263dd'
result={'sourceCommit':commit,'binarySha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'scope':'Published binary ordinary informational/missing-domain CLI regression only; no node start, RPC, transactions, snapshots mutation or financial activation','checks':{}}
def run(args):
    return subprocess.run([str(binary),*args],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=60)
with tempfile.TemporaryDirectory(prefix='mcnd-8cf-cli-',dir='/tmp') as temporary:
    base=Path(temporary)
    for name,args in [('version',['version','--long','--output','json']),('completion',['completion'])]:
        home=base/(name+'-new')
        process=run([*args,'--home',str(home)])
        assert process.returncode==0,(name,process.stderr.decode())
        assert process.stdout and not home.exists()
        if name=='version':
            version=json.loads(process.stdout)
            assert version['commit']==commit and version['server_name']=='mcnd'
        result['checks'][name+'NewHome']={'exitCode':0,'homeCreated':False,'outputBytes':len(process.stdout)}
        (home/'config').mkdir(parents=True,mode=0o700)
        config=home/'config/client.toml'
        config.write_bytes(b'invalid fixture TOML [\n')
        config.chmod(0o600)
        before=config.read_bytes()
        process=run([*args,'--home',str(home)])
        assert process.returncode==0 and config.read_bytes()==before
        assert sorted(p.relative_to(home).as_posix() for p in home.rglob('*'))==['config','config/client.toml']
        result['checks'][name+'UnrelatedFixture']={'exitCode':0,'fixtureUnchanged':True,'noOtherFilesCreated':True}
    home=base/'missing-domain-snapshots'
    process=run(['snapshots','list','--home',str(home)])
    assert process.returncode!=0 and not home.exists()
    result['checks']['snapshotsMissingDomain']={'exitCode':process.returncode,'homeCreated':False,'rejectedBeforeSnapshotStore':True}
(out/'cli-regression.json').write_bytes((json.dumps(result,sort_keys=True,indent=2)+'\n').encode())
print('Published8cf informational and snapshots missing-domain CLI regression PASS; temporary nativeLinux fixtures only')
