import base64, sys
sys.path.insert(0, 'work')
from pool_remote_inspect import connect

REMOTE = r'''
import asyncio, json, sqlite3, ssl, time

DB = '/usr/local/s-ui/db/s-ui.db'
db = sqlite3.connect('file:' + DB + '?mode=ro', uri=True)
rows = []
for tag, typ, options in db.execute("select tag,type,options from outbounds where tag like 'hproxy-%'"):
    try:
        o = json.loads(options or '{}')
        rows.append({'tag': tag, 'typ': typ, 'server': o.get('server'), 'port': int(o.get('server_port', 0)),
                     'user': o.get('username', ''), 'password': o.get('password', '')})
    except Exception:
        pass

async def recv_until(reader, marker, limit=8192):
    data = b''
    while marker not in data and len(data) < limit:
        data += await reader.read(1024)
        if not data:
            break
    return data

async def one(row, sem):
    t0 = time.monotonic()
    try:
        async with sem:
            async with asyncio.timeout(4.0):
                reader, writer = await asyncio.open_connection(row['server'], row['port'])
                if row['typ'] == 'socks':
                    if row['user'] or row['password']:
                        writer.write(b'\x05\x02\x00\x02'); await writer.drain()
                        h = await reader.readexactly(2)
                        if h[1] == 2:
                            u = row['user'].encode(); p = row['password'].encode()
                            writer.write(bytes([1, len(u)]) + u + bytes([len(p)]) + p); await writer.drain()
                            if (await reader.readexactly(2))[1] != 0: raise OSError('auth')
                        elif h[1] != 0: raise OSError('method')
                    else:
                        writer.write(b'\x05\x01\x00'); await writer.drain()
                        if (await reader.readexactly(2))[1] != 0: raise OSError('method')
                    host = b'www.google.com'
                    writer.write(b'\x05\x01\x00\x03' + bytes([len(host)]) + host + (443).to_bytes(2, 'big')); await writer.drain()
                    h = await reader.readexactly(4)
                    if h[1] != 0: raise OSError('connect')
                    if h[3] == 1: await reader.readexactly(6)
                    elif h[3] == 3: await reader.readexactly((await reader.readexactly(1))[0] + 2)
                    elif h[3] == 4: await reader.readexactly(18)
                elif row['typ'] == 'http':
                    writer.write(b'CONNECT www.google.com:443 HTTP/1.1\r\nHost: www.google.com:443\r\nConnection: close\r\n\r\n'); await writer.drain()
                    if b' 200 ' not in (await recv_until(reader, b'\r\n\r\n')).split(b'\r\n', 1)[0]: raise OSError('http')
                else:
                    raise OSError('type')
                ctx = ssl.create_default_context(); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
                loop = asyncio.get_running_loop()
                transport = writer.transport
                protocol = transport.get_protocol()
                tls_transport = await loop.start_tls(transport, protocol, ctx, server_hostname='www.google.com')
                tls_writer = asyncio.StreamWriter(tls_transport, tls_transport.get_protocol(), reader, loop)
                tls_writer.write(b'HEAD /generate_204 HTTP/1.1\r\nHost: www.google.com\r\nConnection: close\r\n\r\n'); await tls_writer.drain()
                data = await reader.read(128)
                parts = data.split(None, 2)
                status = parts[1].decode() if len(parts) > 1 else ''
                tls_writer.close()
                return dict(row, ok=(status == '204'), ms=round((time.monotonic()-t0)*1000, 1), status=status, error='')
    except Exception as exc:
        return dict(row, ok=False, ms=round((time.monotonic()-t0)*1000, 1), status='', error=type(exc).__name__)

async def main():
    sem = asyncio.Semaphore(100)
    out = await asyncio.gather(*(one(row, sem) for row in rows))
    json.dump(out, open('/tmp/hproxy_scan_async.json', 'w'), ensure_ascii=False)
    ok = [x for x in out if x['ok']]
    print(json.dumps({'total': len(out), 'tls_ok': len(ok), 'under650': sum(x['ms'] <= 650 for x in ok),
                      'min_ms': min((x['ms'] for x in ok), default=None), 'max_ms': max((x['ms'] for x in ok), default=None)}, ensure_ascii=False), flush=True)

asyncio.run(main())
'''
payload = base64.b64encode(REMOTE.encode()).decode()
with connect() as c:
    cmd = f"echo {payload} | base64 -d | sudo -n tee /tmp/hproxy_scan_async.py >/dev/null; sudo -n sh -c 'nohup python3 /tmp/hproxy_scan_async.py >/tmp/hproxy_scan_async.out 2>&1 & echo $!'"
    _, o, e = c.exec_command(cmd, timeout=30)
    print(o.read().decode(errors='replace')); print(e.read().decode(errors='replace'))
