import base64, re, sys
sys.path.insert(0, 'work')
from pool_remote_inspect import connect

src = open('work/scan_hproxy_remote.py', encoding='utf-8').read()
remote = re.search(r"script=r'''(.*?)'''", src, re.S).group(1)
remote = remote.replace('max_workers=40', 'max_workers=100')
remote = remote.replace('timeout=5', 'timeout=2.5')
remote = remote.replace("json.dumps({'total':len(out)", "json.dumps({'total':len(out)")
payload = base64.b64encode(remote.encode()).decode()
cmd = f"echo {payload} | base64 -d | sudo -n tee /tmp/hproxy_scan_fast.py >/dev/null; sudo -n sh -c 'nohup python3 /tmp/hproxy_scan_fast.py >/tmp/hproxy_scan_fast.out 2>&1 & echo $!'"
with connect() as c:
    _, out, err = c.exec_command(cmd, timeout=30)
    print(out.read().decode(errors='replace'))
    print(err.read().decode(errors='replace'))
