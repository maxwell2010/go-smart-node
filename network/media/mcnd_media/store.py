"""Local public-media store. Trusted authorization adapter is mandatory.

No DNS, URL downloads, IPFS daemon, private keys or consensus writes exist here.
A host process must supply an adapter that verifies current on-chain ownership.
"""
import base64
from dataclasses import dataclass
import hashlib
import io
import json
import os
from pathlib import Path
import re
import sqlite3
import stat
import subprocess
import sys
import tempfile
import zipfile

MAX_BLOB=10<<20
MAX_TOTAL=64<<20
MAX_INDEX=1<<20
MAX_RECORDS=512
CID=re.compile(r'bafkrei[a-z2-7]{52}')
ADDRESS=re.compile(r'0x[0-9a-f]{40}')

def require(ok,message):
    if not ok: raise ValueError(message)

def cid(data):
    return 'b'+base64.b32encode(b'\x01\x55\x12\x20'+hashlib.sha256(data).digest()).decode().lower().rstrip('=')

def canonical(value):
    return json.dumps(value,ensure_ascii=False,sort_keys=True,separators=(',',':'),allow_nan=False).encode()

def unique(pairs):
    d={}
    for k,v in pairs:
        require(k not in d,'duplicate JSON field');d[k]=v
    return d

def strict(data):
    return json.loads(data.decode('utf-8-sig'),object_pairs_hook=unique,parse_constant=lambda x: (_ for _ in ()).throw(ValueError('nonfinite JSON')))

@dataclass(frozen=True)
class Asset:
    chain_id:str
    contract:str
    kind:str
    token_id:str='0'
    def key(self):
        require(re.fullmatch(r'mintcandy_[1-9][0-9]{0,18}-[1-9][0-9]{0,8}',self.chain_id),'invalid chain')
        require(ADDRESS.fullmatch(self.contract) and int(self.contract,16)!=0,'invalid contract')
        require(self.kind in ('erc20','erc721','erc1155'),'invalid asset kind')
        require(re.fullmatch(r'0|[1-9][0-9]{0,77}',self.token_id) and int(self.token_id)<2**256,'invalid token ID')
        require(self.kind!='erc20' or self.token_id=='0','fungible token ID must be zero')
        return ':'.join((self.chain_id,self.contract,self.kind,self.token_id))

def text(value,maximum):
    require(isinstance(value,str) and len(value.encode('utf-8'))<=maximum and all(ord(c)>=32 and ord(c)!=127 for c in value),'invalid metadata text')
    return value

def metadata_record(data):
    require(isinstance(data,dict) and set(data)<= {'name','description','attributes'} and 'name' in data,'unsupported metadata fields; image is assigned from uploaded bytes')
    result={'name':text(data['name'],256)}
    if 'description' in data: result['description']=text(data['description'],4096)
    if 'attributes' in data:
        require(isinstance(data['attributes'],list) and len(data['attributes'])<=32,'attribute limit')
        result['attributes']=[]
        for a in data['attributes']:
            require(isinstance(a,dict) and set(a)=={'trait_type','value'},'invalid attribute')
            result['attributes'].append({'trait_type':text(a['trait_type'],128),'value':text(a['value'],512)})
    require(len(canonical(result))<=64<<10,'metadata size limit')
    return result

def decode_image(data,mime,avatar):
    require(sys.platform=='linux','image worker requires Linux resource limits')
    require(isinstance(data,bytes) and 0<len(data)<=((1<<20) if avatar else MAX_BLOB),'upload byte limit')
    require(mime in ('image/png','image/jpeg','image/webp'),'unsupported image MIME')
    worker=Path(__file__).with_name('decoder.py')
    result=subprocess.run([sys.executable,'-I','-B',str(worker)],input=mime.encode()+b'\n'+data,capture_output=True,timeout=12)
    require(result.returncode==0,'image decoder rejected input')
    header,sep,encoded=result.stdout.partition(b'\n')
    require(sep and len(header)<=256 and 0<len(encoded)<=MAX_BLOB,'invalid bounded decoder output')
    info=strict(header)
    require(info['mime']=='image/png' and 0<info['width']<=4096 and 0<info['height']<=4096,'decoder dimensions differ')
    return encoded

def prepare_media(asset, metadata, image, mime):
    asset.key()
    doc = metadata_record(metadata)
    clean = decode_image(image, mime, asset.kind == 'erc20')
    doc['image'] = 'ipfs://' + cid(clean)
    return clean, canonical(doc)

def regular_read(path,limit):
    path=Path(path)
    require(not path.is_symlink(),'symlink input rejected')
    with path.open('rb') as f:
        st=os.fstat(f.fileno());require(stat.S_ISREG(st.st_mode) and 0<st.st_size<=limit,'invalid file size/type')
        data=f.read(limit+1);require(len(data)<=limit,'file grew beyond bound')
    return data

class MediaStore:
    def __init__(self,root,authorizer=None,quota=MAX_TOTAL,*,commitment_reader=None):
        self.root=Path(root)
        require(self.root.is_absolute() and not self.root.is_symlink(),'absolute non-symlink root required')
        require('..' not in self.root.parts and all(not p.is_symlink() for p in self.root.parents),'symlink parent rejected')
        self.root.mkdir(parents=True,exist_ok=True)
        objects = self.root / 'objects'
        database = self.root / 'index.sqlite3'
        require(not objects.is_symlink() and not database.is_symlink(), 'symlink store rejected')
        objects.mkdir(exist_ok=True)
        has_objects = next(objects.iterdir(), None) is not None
        self.authorizer=authorizer
        self.commitment_reader = commitment_reader
        require(type(quota) is int and 0<quota<=MAX_TOTAL,'invalid store quota')
        self.quota=quota
        if has_objects:
            require(database.exists(), 'media index missing for existing objects; restore a verified backup')
            self.db = sqlite3.connect(database.as_uri() + '?mode=rw', uri=True)
        else:
            self.db = sqlite3.connect(database)
        try:
            if has_objects:
                require(self.db.execute("SELECT 1 FROM sqlite_schema WHERE type='table' AND name='records'").fetchone(),
                        'media index missing for existing objects; restore a verified backup')
            self.db.execute('PRAGMA journal_mode=DELETE')
            self.db.execute('CREATE TABLE IF NOT EXISTS records(asset TEXT,version INTEGER,owner TEXT,metadata TEXT,image TEXT,PRIMARY KEY(asset,version))')
            self.db.commit()
        except BaseException:
            self.db.close()
            raise
    def close(self): self.db.close()

    def _recover_object_usage(self):
        # The caller holds BEGIN IMMEDIATE so another publisher cannot be staging files.
        require(self.db.in_transaction, 'media recovery requires the writer transaction')
        rows = self.db.execute('SELECT metadata,image FROM records LIMIT ?', (MAX_RECORDS + 1,)).fetchall()
        require(len(rows) <= MAX_RECORDS, 'record quota')
        referenced = {name for row in rows for name in row}
        require(all(isinstance(name, str) and CID.fullmatch(name) for name in referenced), 'invalid recorded CID')
        present = set()
        orphans = []
        used = 0
        for path in (self.root / 'objects').iterdir():
            require(len(present) < 2 * MAX_RECORDS + 2, 'object count exceeds recovery limit')
            info = path.lstat()
            require(stat.S_ISREG(info.st_mode) and CID.fullmatch(path.name), 'invalid object store entry')
            present.add(path.name)
            if path.name in referenced:
                require(0 < info.st_size <= MAX_BLOB, 'invalid committed object size')
                used += info.st_size
            else:
                orphans.append(path)
        require(referenced <= present, 'committed media is missing; restore a verified backup')
        for path in orphans:
            path.unlink()
        return used

    def _sync_objects(self):
        if sys.platform == 'linux':
            directory = os.open(self.root / 'objects', os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)

    def _put(self,data):
        name=cid(data);path=self.root/'objects'/name
        if path.exists() or path.is_symlink():
            require(regular_read(path,MAX_BLOB)==data,'existing content mismatch');return name,False
        fd=os.open(path,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        created=os.fstat(fd)
        try:
            with os.fdopen(fd,'wb') as f: f.write(data);f.flush();os.fsync(f.fileno())
        except BaseException:
            try:
                current=path.lstat()
                if (current.st_dev,current.st_ino)==(created.st_dev,created.st_ino):path.unlink()
            except FileNotFoundError:pass
            raise
        return name,True
    def publish(self,asset,actor,metadata,image,mime):
        key=asset.key()
        require(ADDRESS.fullmatch(actor) is not None,'invalid actor')
        require(self.authorizer is not None and self.authorizer(asset,actor,'publish') is True,'verified ownership authorization required')
        clean, payload = prepare_media(asset, metadata, image, mime)
        return self._publish(key, actor, clean, payload)

    def publish_committed(self, asset, metadata, image, mime):
        require(self.commitment_reader is not None, 'verified chain commitment reader required')
        clean, payload = prepare_media(asset, metadata, image, mime)
        return self._publish(asset.key(), None, clean, payload, asset=asset)

    def _publish(self, key, actor, clean, payload, *, asset=None):
        image_cid, metadata_cid = cid(clean), cid(payload)
        created=[]
        try:
            self.db.execute('BEGIN IMMEDIATE')
            previous = None
            if asset is not None:
                actor = self.commitment_reader(asset, 'ipfs://' + metadata_cid)
                require(isinstance(actor, str) and ADDRESS.fullmatch(actor) and int(actor, 16),
                        'chain commitment did not identify an asset authority')
                previous = self.db.execute(
                    'SELECT version,owner,metadata,image FROM records WHERE asset=? ORDER BY version DESC LIMIT 1',
                    (key,),
                ).fetchone()
            used = self._recover_object_usage()
            if previous is not None and previous[1:] == (actor, metadata_cid, image_cid):
                # Check the already published objects before acknowledging a retry.
                objects = self.root / 'objects'
                require(regular_read(objects / image_cid, MAX_BLOB) == clean and
                        regular_read(objects / metadata_cid, 64 << 10) == payload,
                        'committed publication is missing stored objects')
                version = previous[0]
                self.db.commit()
                return {'asset':key,'version':version,'owner':actor,'metadata_uri':'ipfs://'+metadata_cid,'image_uri':'ipfs://'+image_cid}
            records=self.db.execute('SELECT COUNT(*) FROM records').fetchone()[0]
            version=self.db.execute('SELECT COALESCE(MAX(version),0)+1 FROM records WHERE asset=?',(key,)).fetchone()[0]
            require(records<MAX_RECORDS and version<=16,'record/version quota')
            needed=sum(len(data) for data in (clean,payload) if not (self.root/'objects'/cid(data)).exists())
            require(used+needed<=self.quota,'storage quota exceeded')
            for data in (clean,payload):
                name,new=self._put(data)
                if new: created.append(self.root/'objects'/name)
            if created:
                self._sync_objects()
            self.db.execute('INSERT INTO records VALUES(?,?,?,?,?)',(key,version,actor,metadata_cid,image_cid))
            self.db.commit()
        except BaseException:
            try:
                if self.db.in_transaction:
                    # Keep the writer lock until the staged files are gone.
                    for path in created:
                        path.unlink(missing_ok=True)
                elif created:
                    # SQLite may already have ended the transaction. A second
                    # writer can have reused these files while the lock was free.
                    self.db.execute('BEGIN IMMEDIATE')
                    for path in created:
                        referenced = self.db.execute(
                            'SELECT 1 FROM records WHERE metadata=? OR image=? LIMIT 1',
                            (path.name, path.name),
                        ).fetchone()
                        if referenced is None:
                            path.unlink(missing_ok=True)
            finally:
                self.db.rollback()
            raise
        return {'asset':key,'version':version,'owner':actor,'metadata_uri':'ipfs://'+metadata_cid,'image_uri':'ipfs://'+image_cid}
    def records(self):
        return [dict(zip(('asset','version','owner','metadata','image'),row)) for row in self.db.execute('SELECT * FROM records ORDER BY asset,version')]
    def verify(self):
        rows=self.records();require(len(rows)<=MAX_RECORDS,'record quota')
        required=set();previous={}
        for row in rows:
            parts=row['asset'].split(':');require(len(parts)==4,'invalid namespace')
            Asset(*parts).key();require(ADDRESS.fullmatch(row['owner']) is not None,'invalid recorded owner')
            require(type(row['version']) is int and row['version']==previous.get(row['asset'],0)+1 and row['version']<=16,'invalid version history')
            previous[row['asset']]=row['version']
            for field in ('metadata','image'):
                name=row[field];require(isinstance(name,str) and CID.fullmatch(name),'invalid CID')
                value=regular_read(self.root/'objects'/name,MAX_BLOB)
                require(cid(value)==name,'content hash mismatch');required.add(name)
            raw=regular_read(self.root/'objects'/row['metadata'],64<<10)
            doc=strict(raw);require(doc.pop('image',None)=='ipfs://'+row['image'],'metadata image reference mismatch')
            metadata_record(doc)
        actual={p.name for p in (self.root/'objects').iterdir()}
        require(required==actual,'unreferenced or missing media objects')
        require(sum((self.root/'objects'/n).stat().st_size for n in required)<=self.quota,'storage quota exceeded')
        return rows,required
    def backup(self,destination):
        destination=Path(destination);require(destination.is_absolute(),'absolute backup path required')
        # Lock writers during the consistent index/object snapshot.
        self.db.execute('BEGIN IMMEDIATE')
        try:
            self._recover_object_usage()
            rows,names=self.verify();index=canonical({'schema':1,'records':rows})
            require(len(index)<=MAX_INDEX,'backup index limit')
            with destination.open('xb') as handle:
                with zipfile.ZipFile(handle,'w',compression=zipfile.ZIP_STORED) as z:
                    z.writestr('index.json',index)
                    for name in sorted(names): z.writestr('objects/'+name,regular_read(self.root/'objects'/name,MAX_BLOB))
            return hashlib.sha256(regular_read(destination,MAX_TOTAL+MAX_INDEX+(1<<20))).hexdigest()
        finally: self.db.rollback()


def restore(archive,destination,expected_sha256,authorizer=None):
    """Restore only into a new directory. Expected hash is an external trust anchor."""
    require(re.fullmatch(r'[0-9a-f]{64}',expected_sha256) is not None,'trusted SHA256 required')
    raw=regular_read(archive,MAX_TOTAL+MAX_INDEX+(1<<20))
    require(hashlib.sha256(raw).hexdigest()==expected_sha256,'backup SHA256 mismatch')
    destination=Path(destination)
    require(destination.is_absolute() and not destination.exists() and not destination.is_symlink(),'restore destination must be new')
    # Validate whole backup in a sibling staging directory before publication.
    destination.parent.mkdir(parents=True,exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='media-restore-',dir=destination.parent) as temporary:
        stage=Path(temporary)/'store';store=MediaStore(stage,authorizer)
        try:
            with zipfile.ZipFile(io.BytesIO(raw)) as z:
                infos=z.infolist();require(1<=len(infos)<=MAX_RECORDS*2+1,'backup entry count')
                names=[i.filename for i in infos];require(len(names)==len(set(names)) and 'index.json' in names,'duplicate or missing index')
                total=0
                for info in infos:
                    valid=info.filename=='index.json' or info.filename.startswith('objects/') and CID.fullmatch(info.filename[8:])
                    limit=MAX_INDEX if info.filename=='index.json' else MAX_BLOB
                    mode=info.external_attr>>16
                    require(valid and not info.is_dir() and not stat.S_ISLNK(mode) and not info.flag_bits&1 and info.compress_type==zipfile.ZIP_STORED and 0<info.file_size<=limit,'unsafe backup entry')
                    total+=info.file_size;require(total<=MAX_TOTAL+MAX_INDEX,'backup expanded limit')
                    with z.open(info) as source: data=source.read(limit+1)
                    require(len(data)==info.file_size and len(data)<=limit,'truncated backup')
                    if info.filename=='index.json': index=strict(data)
                    else:
                        require(cid(data)==info.filename[8:],'backup object CID mismatch')
                        store._put(data)
                require(set(index)=={'schema','records'} and index['schema']==1 and isinstance(index['records'],list) and len(index['records'])<=MAX_RECORDS,'invalid backup index')
                for row in index['records']:
                    require(isinstance(row,dict) and set(row)=={'asset','version','owner','metadata','image'} and type(row['version']) is int,'invalid record fields')
                    store.db.execute('INSERT INTO records VALUES(?,?,?,?,?)',tuple(row[n] for n in ('asset','version','owner','metadata','image')))
                store.db.commit();store.verify()
        finally: store.close()
        # mkdir refuses replacing another process's newly created destination.
        destination.mkdir()
        try:
            for p in stage.iterdir(): os.rename(p,destination/p.name)
        except BaseException:
            # Keep a partial new destination for operator inspection; never delete unrelated paths.
            raise
    return MediaStore(destination,authorizer)
