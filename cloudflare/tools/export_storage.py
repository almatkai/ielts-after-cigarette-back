"""Private read-only storage transfer. Remote credentials stay in remote memory."""

import hashlib, json, os, pathlib, subprocess

# This program runs on the selected VPS only when the parent exporter is explicitly invoked.
REMOTE = r"""
import base64,datetime,hashlib,hmac,json,socket,struct,subprocess,sys,urllib.parse,urllib.request,xml.etree.ElementTree as ET
BACKEND='ielts-guest-dev-backend-1'
def inspect(name):return json.loads(subprocess.check_output(['docker','inspect',name]))[0]
backend=inspect(BACKEND)
if backend['Image']!='sha256:3cc32d093c0898032ab50a8d62972f606f09f91e4e111661950630cee063edba':raise RuntimeError('DEV image changed')
env=dict(s.split('=',1) for s in backend['Config']['Env'])
def ip(name):
 x=inspect(name);network=next(n for n in backend['NetworkSettings']['Networks'] if n in x['NetworkSettings']['Networks']);return x['NetworkSettings']['Networks'][network]['IPAddress']
def header(record):
 b=json.dumps(record,separators=(',',':')).encode();sys.stdout.buffer.write(struct.pack('>I',len(b))+b);sys.stdout.buffer.flush()
# S3 Signature V4 over the DEV MinIO private Docker address; credentials never leave the VPS.
host=ip('ielts-guest-dev-minio-1')+':9000';bucket=env.get('OBJECT_STORAGE_BUCKET','ielts-media');access=env['OBJECT_STORAGE_ACCESS_KEY'];secret=env['OBJECT_STORAGE_SECRET_KEY'];region=env.get('OBJECT_STORAGE_REGION','us-east-1')
def s3(key=None,query=None):
 path='/'+urllib.parse.quote(bucket,safe='')+('/'+urllib.parse.quote(key,safe='/~') if key is not None else '')
 query='&'.join(urllib.parse.quote(k,safe='~')+'='+urllib.parse.quote(v,safe='~') for k,v in sorted((query or {}).items()))
 now=datetime.datetime.now(datetime.timezone.utc);stamp=now.strftime('%Y%m%dT%H%M%SZ');day=stamp[:8];bodyhash=hashlib.sha256(b'').hexdigest();headers='host:'+host+'\nx-amz-content-sha256:'+bodyhash+'\nx-amz-date:'+stamp+'\n';signed='host;x-amz-content-sha256;x-amz-date';canonical='GET\n'+path+'\n'+query+'\n'+headers+'\n'+signed+'\n'+bodyhash;scope=day+'/'+region+'/s3/aws4_request';message='AWS4-HMAC-SHA256\n'+stamp+'\n'+scope+'\n'+hashlib.sha256(canonical.encode()).hexdigest()
 def sign(key,value):return hmac.new(key,value.encode(),hashlib.sha256).digest()
 k=sign(sign(sign(sign(('AWS4'+secret).encode(),day),region),'s3'),'aws4_request');signature=hmac.new(k,message.encode(),hashlib.sha256).hexdigest();authorization='AWS4-HMAC-SHA256 Credential='+access+'/'+scope+', SignedHeaders='+signed+', Signature='+signature
 return urllib.request.urlopen(urllib.request.Request('http://'+host+path+('?'+query if query else ''),headers={'Host':host,'x-amz-content-sha256':bodyhash,'x-amz-date':stamp,'Authorization':authorization}),timeout=120)
token=None
while True:
 q={'list-type':'2','max-keys':'1000'}
 if token:q['continuation-token']=token
 with s3(query=q) as response:root=ET.fromstring(response.read())
 ns={'s':'http://s3.amazonaws.com/doc/2006-03-01/'}
 for obj in root.findall('s:Contents',ns):
  key=obj.findtext('s:Key',namespaces=ns);size=int(obj.findtext('s:Size',namespaces=ns))
  with s3(key) as response:
   if int(response.headers['Content-Length'])!=size:raise RuntimeError('Object changed during export')
   header({'kind':'object','key':key,'size':size,'contentType':response.headers.get('Content-Type','application/octet-stream')});remaining=size
   while remaining:
    b=response.read(min(1<<20,remaining))
    if not b:raise RuntimeError('Truncated object')
    sys.stdout.buffer.write(b);remaining-=len(b)
   sys.stdout.buffer.flush()
 token=root.findtext('s:NextContinuationToken',namespaces=ns)
 if not token:break
# Minimal RESP reader, authenticated using the existing DEV URL, without redis-cli argv secrets.
url=urllib.parse.urlparse(env['REDIS_URL']);sock=socket.create_connection((ip('ielts-guest-dev-redis-1'),url.port or 6379),timeout=120);stream=sock.makefile('rb')
def receive():
 marker=stream.read(1);line=stream.readline()[:-2]
 if marker==b'+':return line
 if marker==b'-':raise RuntimeError('Redis command rejected')
 if marker==b':':return int(line)
 if marker==b'$':
  n=int(line)
  if n<0:return None
  b=stream.read(n);stream.read(2);return b
 if marker==b'*':
  n=int(line);return None if n<0 else [receive() for _ in range(n)]
 raise RuntimeError('Malformed Redis response')
def redis(*args):
 values=[a if isinstance(a,bytes) else str(a).encode() for a in args];sock.sendall(b'*'+str(len(values)).encode()+b'\r\n'+b''.join(b'$'+str(len(v)).encode()+b'\r\n'+v+b'\r\n' for v in values));return receive()
if url.password:
 credentials=[urllib.parse.unquote(url.password)]
 if url.username:credentials.insert(0,urllib.parse.unquote(url.username))
 redis('AUTH',*credentials)
if url.path.strip('/'):redis('SELECT',int(url.path.strip('/')))
cursor=0;keys=set()
while True:
 cursor,batch=redis('SCAN',cursor,'COUNT',1000);keys.update(batch)
 if cursor==b'0':break
for key in sorted(keys):
 typ=redis('TYPE',key).decode();ttl=redis('PTTL',key)
 if typ=='none' or ttl==-2:continue
 now=redis('TIME');expires=int(now[0])*1000+int(now[1])//1000+ttl if ttl>=0 else None
 if typ=='string':
  raw=redis('GET',key)
  if raw.startswith(b'HYLL'):value={'hll_raw':base64.b64encode(raw).decode(),'count':redis('PFCOUNT',key)}
  else:value=raw.decode()
 elif typ in ('hash','zset'):
  raw=redis('HGETALL',key) if typ=='hash' else redis('ZRANGE',key,0,-1,'WITHSCORES');value={raw[i].decode():raw[i+1].decode() if typ=='hash' else float(raw[i+1]) for i in range(0,len(raw),2)}
 elif typ=='list':value=[x.decode() for x in redis('LRANGE',key,0,-1)]
 else:raise RuntimeError('Unmapped Redis type; export requires explicit coverage')
 header({'kind':'redis','key':key.decode(),'type':typ,'value':value,'expires':expires,'dump':base64.b64encode(redis('DUMP',key)).decode()})
header({'kind':'end'})
"""


def read_exact(stream, size):
    result = bytearray()
    while len(result) < size:
        b = stream.read(size - len(result))
        if not b:
            raise RuntimeError("Truncated private transfer")
        result.extend(b)
    return bytes(result)


def export_storage(output, ssh):
    import struct

    script = output / "storage-query.py"
    script.write_text(REMOTE)
    os.chmod(script, 0o600)
    objects = []
    redis = []
    directory = output / "objects"
    directory.mkdir(mode=0o700)
    with open(script, "rb") as source:
        # python3 consumes the script from stdin; its data output is length framed.
        p = subprocess.Popen(
            ssh[:-2] + ["python3", "-"],
            stdin=source,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        try:
            while True:
                record = json.loads(
                    read_exact(
                        p.stdout, struct.unpack(">I", read_exact(p.stdout, 4))[0]
                    )
                )
                kind = record["kind"]
                if kind == "end":
                    break
                if kind == "redis":
                    redis.append(record)
                    continue
                if kind != "object":
                    raise RuntimeError("Unknown transfer record")
                filename = hashlib.sha256(record["key"].encode()).hexdigest()
                checksum = hashlib.sha256()
                remaining = record["size"]
                with os.fdopen(
                    os.open(
                        str(directory / filename),
                        os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                        0o600,
                    ),
                    "wb",
                ) as target:
                    while remaining:
                        b = read_exact(p.stdout, min(remaining, 1 << 20))
                        target.write(b)
                        checksum.update(b)
                        remaining -= len(b)
                record.update(file=filename, sha256=checksum.hexdigest())
                objects.append(record)
            if p.wait(timeout=30) != 0:
                raise RuntimeError("Private storage transfer failed")
        finally:
            if p.poll() is None:
                p.terminate()
                try:
                    p.communicate(timeout=5)
                except subprocess.TimeoutExpired:
                    p.kill()
                    p.communicate()
    script.unlink()
    for name, value in [
        ("objects.json", sorted(objects, key=lambda r: r["key"])),
        ("redis.json", sorted(redis, key=lambda r: r["key"])),
    ]:
        with os.fdopen(
            os.open(str(output / name), os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600),
            "w",
        ) as target:
            json.dump(
                value, target, sort_keys=True, separators=(",", ":"), ensure_ascii=False
            )
            target.write("\n")
    print(
        "DEV storage export complete:",
        len(objects),
        "objects;",
        len(redis),
        "coordination keys. Values are private.",
    )
