import hashlib,io,json,os,struct,zlib
from pathlib import Path
import sqlite3,tempfile,unittest,zipfile
from unittest.mock import patch
from mcnd_media.store import Asset,MediaStore,cid,restore,decode_image,strict,MAX_BLOB

OWNER='0x'+'11'*20
OTHER='0x'+'22'*20
ASSET=Asset('mintcandy_9905401-1','0x'+'33'*20,'erc20')
# Store tests avoid image parsing. Full CI separately invokes the pinned decoder.
CLEAN=b'normalized-image-fixture'

class StoreTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.root=Path(self.tmp.name)
  self.store=MediaStore(self.root/'primary',lambda asset,actor,action: asset==ASSET and actor==OWNER)
  self.decoder=patch('mcnd_media.store.decode_image',return_value=CLEAN);self.decoder.start()
 def tearDown(self): self.decoder.stop();self.store.close();self.tmp.cleanup()
 def publish(self,**kw):
  return self.store.publish(ASSET,kw.get('actor',OWNER),kw.get('metadata',{'name':'Avatar'}),b'input','image/png')
 def test_ownership_boundary_and_unknown_fields(self):
  for actor in (OTHER,):
   with self.assertRaisesRegex(ValueError,'authorization'): self.publish(actor=actor)
  noauth=MediaStore(self.root/'noauth')
  try:
   with self.assertRaisesRegex(ValueError,'authorization'): noauth.publish(ASSET,OWNER,{'name':'x'},b'x','image/png')
  finally:noauth.close()
  for field in ('image','private_key','external_url'):
   with self.assertRaises(ValueError): self.publish(metadata={'name':'x',field:'javascript:alert(1)'})
  self.assertEqual(self.store.records(),[])
 def test_publish_deduplicate_version_and_restart(self):
  one=self.publish();two=self.publish()
  self.assertEqual(one['image_uri'],two['image_uri']);self.assertEqual(two['version'],2)
  self.assertEqual(len(list((self.store.root/'objects').iterdir())),2)
  self.store.close();self.store=MediaStore(self.root/'primary',lambda *a:False)
  rows,_=self.store.verify();self.assertEqual([r['owner'] for r in rows],[OWNER,OWNER])
  with self.assertRaises(ValueError):self.publish(actor=OTHER)
 def test_quota_and_rejected_publication_leave_no_partial_state(self):
  self.store.quota=1
  with self.assertRaisesRegex(ValueError,'quota'): self.publish()
  self.assertFalse(list((self.store.root/'objects').iterdir()));self.assertFalse(self.store.records())
  self.store.quota=1024
  with patch.object(self.store,'_put',side_effect=[(cid(CLEAN),False),OSError('fixture disk failure')]):
   with self.assertRaises(OSError):self.publish()
  self.assertFalse(self.store.records())
 def test_backup_restore_and_loss_recovery(self):
  original=self.publish();backup=self.root/'backup.zip';digest=self.store.backup(backup)
  restored=restore(backup,self.root/'restored',digest,lambda *a:False)
  try:
   self.assertEqual(restored.verify()[0],self.store.verify()[0])
   doc=strict((restored.root/'objects'/original['metadata_uri'][7:]).read_bytes())
   self.assertEqual(doc['image'],original['image_uri'])
   self.assertEqual((restored.root/'objects'/original['image_uri'][7:]).read_bytes(),CLEAN)
  finally:restored.close()
  with self.assertRaises(ValueError):restore(backup,self.root/'restored',digest)
 def test_tampered_backup_and_object_rejected(self):
  result=self.publish();backup=self.root/'backup.zip';digest=self.store.backup(backup)
  raw=bytearray(backup.read_bytes());raw[-1]^=1;backup.write_bytes(raw)
  with self.assertRaisesRegex(ValueError,'SHA256'):restore(backup,self.root/'unused',digest)
  self.assertFalse((self.root/'unused').exists())
  (self.store.root/'objects'/result['image_uri'][7:]).write_bytes(b'tampered')
  with self.assertRaisesRegex(ValueError,'hash mismatch'):self.store.verify()
 def test_unsafe_backup_entries_rejected_even_with_correct_outer_hash(self):
  for name in ('../escape','objects/../escape','objects/not-a-cid'):
   p=self.root/'unsafe.zip'
   with zipfile.ZipFile(p,'w') as z:z.writestr('index.json','{"schema":1,"records":[]}');z.writestr(name,b'bad')
   with self.assertRaises(ValueError):restore(p,self.root/'unsafe-destination',hashlib.sha256(p.read_bytes()).hexdigest())
   self.assertFalse((self.root/'unsafe-destination').exists())
 def test_asset_namespace_metadata_and_symlink_limits(self):
  for asset in (Asset('../../x',ASSET.contract,'erc20'),Asset(ASSET.chain_id,ASSET.contract,'erc721',str(2**256))):
   with self.assertRaises(ValueError):asset.key()
  for metadata in ({'name':'x\x00'}, {'name':'x','description':'x'*4097}):
   with self.assertRaises(ValueError):self.publish(metadata=metadata)
  if os.name=='posix':
   (self.root/'link').symlink_to(self.store.root,target_is_directory=True)
   with self.assertRaises(ValueError):MediaStore(self.root/'link'/'child')
 def test_local_store_cid_matches_raw_sha256(self):
  self.assertEqual(cid(b''),'bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku')

@unittest.skipUnless(os.environ.get('MCND_MEDIA_TEST_FULL')=='1','pinned Linux decoder CI only')
class DecoderTests(unittest.TestCase):
 def image(self,fmt='PNG',size=(3,4)):
  from PIL import Image
  out=io.BytesIO();Image.new('RGB',size,'red').save(out,format=fmt);return out.getvalue()
 def test_actual_supported_formats_and_metadata_removal(self):
  from PIL import Image
  for fmt,mime in [('PNG','image/png'),('JPEG','image/jpeg'),('WEBP','image/webp')]:
   data=self.image(fmt);result=decode_image(data,mime,True)
   with Image.open(io.BytesIO(result)) as im:
    self.assertEqual(im.format,'PNG');self.assertEqual(im.size,(3,4));self.assertFalse(im.getexif());self.assertFalse(im.info)
 def test_bad_formats_mime_truncation_and_pixel_limit(self):
  png=self.image();ihdr=struct.pack('>IIBBBBB',100000,100000,8,2,0,0,0)
  bomb=png[:8]+struct.pack('>I',13)+b'IHDR'+ihdr+struct.pack('>I',zlib.crc32(b'IHDR'+ihdr))+png[33:]
  cases=[(bomb,'image/png'),(b'<svg onload="bad"/>','image/png'),(b'<html>bad</html>','image/png'),(self.image(),'image/jpeg'),(self.image()[:20],'image/png'),(self.image(size=(4097,1)),'image/png'),(b'x'*(MAX_BLOB+1),'image/png')]
  for data,mime in cases:
   with self.assertRaises(ValueError):decode_image(data,mime,False)
 def test_full_decoded_store_roundtrip(self):
  with tempfile.TemporaryDirectory() as t:
   root=Path(t);store=MediaStore(root/'primary',lambda asset,actor,action:actor==OWNER)
   first=store.publish(ASSET,OWNER,{'name':'Real avatar'},self.image(),'image/png')
   digest=store.backup(root/'backup.zip');store.close()
   recovered=restore(root/'backup.zip',root/'recovered',digest)
   try:
    self.assertEqual(recovered.verify()[0][0]['metadata'],first['metadata_uri'][7:])
    with self.assertRaises(ValueError):recovered.publish(ASSET,OTHER,{'name':'bad'},self.image(),'image/png')
   finally:recovered.close()

if __name__=='__main__':unittest.main()
