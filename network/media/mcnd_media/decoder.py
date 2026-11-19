"""Resource-bounded Linux image decoder. Input: MIME newline encoded image."""
import io,json,resource,sys,warnings
resource.setrlimit(resource.RLIMIT_AS,(512<<20,512<<20))
resource.setrlimit(resource.RLIMIT_CPU,(6,6))
resource.setrlimit(resource.RLIMIT_FSIZE,(10<<20,10<<20))
from PIL import Image, __version__
if __version__ != '13.0.0': raise ValueError('pinned Pillow 13.0.0 required')
Image.MAX_IMAGE_PIXELS=4096*4096
warnings.simplefilter('error',Image.DecompressionBombWarning)
mime=sys.stdin.buffer.readline(80).decode().strip()
data=sys.stdin.buffer.read((10<<20)+1)
if not data or len(data)>10<<20: raise ValueError('image size limit')
formats={'image/png':'PNG','image/jpeg':'JPEG','image/webp':'WEBP'}
if mime not in formats: raise ValueError('unsupported image MIME')
with Image.open(io.BytesIO(data)) as im:
 if im.format!=formats[mime] or getattr(im,'n_frames',1)!=1 or min(im.size)<1 or max(im.size)>4096: raise ValueError('format, frame or pixel limit')
 im.verify()
with Image.open(io.BytesIO(data)) as im:
 im.load()
 clean=im.convert('RGBA' if 'A' in im.getbands() else 'RGB')
 # Copy pixels only. No EXIF, ICC, comments or arbitrary input metadata survive.
 pixels=Image.frombytes(clean.mode,clean.size,clean.tobytes())
 out=io.BytesIO();pixels.save(out,format='PNG')
 encoded=out.getvalue()
 if len(encoded)>10<<20: raise ValueError('normalized image exceeds output bound')
 sys.stdout.buffer.write(json.dumps({'mime':'image/png','width':im.width,'height':im.height}).encode()+b'\n'+encoded)
