import socket
import uuid
import json
import base64
import urllib.parse
import urllib.request
import ssl
import sys
import time

sys.stdout.reconfigure(encoding='utf-8', errors='replace')

def fetch_nodes_from_sub():
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE
    opener = urllib.request.build_opener(
        urllib.request.HTTPSHandler(context=ctx)
    )
    candidate_urls = [
        "https://127.0.0.1:2096/sub/my",
        "https://dash.icta.top:2096/sub/my",
        "http://127.0.0.1:2096/sub/my",
        "http://dash.icta.top:2096/sub/my",
    ]
    data = None
    for url in candidate_urls:
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "v2rayN/6.23", "Host": "dash.icta.top"})
            data = opener.open(req, timeout=10).read().decode('utf-8')
            if data:
                break
        except Exception:
            continue
    if not data:
        raise RuntimeError("Failed to fetch subscription from all candidate URLs")
    data = base64.b64decode(data.strip() + '===').decode('utf-8')
    
    nodes = {}
    for line in data.splitlines():
        line = line.strip()
        if not line or not line.startswith("vless://"):
            continue
        unquoted = urllib.parse.unquote(line)
        # Parse uuid from vless://uuid@host:port
        parsed = urllib.parse.urlparse(line)
        uid = parsed.username
        port = parsed.port or 54142
        
        # Tag from fragment
        tag = urllib.parse.unquote(parsed.fragment)
        
        if "54142" not in tag:
            continue
        if "原生直连" in tag and "native" not in nodes:
            nodes["native"] = (tag, uid, port)
        elif "[US]" in tag and "us" not in nodes:
            nodes["us"] = (tag, uid, port)
        elif "[JP]" in tag and "jp" not in nodes:
            nodes["jp"] = (tag, uid, port)
        elif "[NL]" in tag and "nl" not in nodes:
            nodes["nl"] = (tag, uid, port)
        elif "[CF-" in tag:
            start = tag.find("[CF-")
            end = tag.find("]", start)
            if start != -1 and end != -1:
                cf_code = tag[start+1:end].lower()
                if cf_code not in nodes:
                    nodes[cf_code] = (tag, uid, port)
            
    return nodes

def test_vless_egress(uid_str, port=54142, server_ip=""):
    candidate_ips = []
    if server_ip:
        candidate_ips.append(server_ip)
    else:
        candidate_ips = ["127.0.0.1", "124.156.207.253"]
    last_err = None
    for ip in candidate_ips:
        try:
            u = uuid.UUID(uid_str)
            s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            s.settimeout(16)
            s.connect((ip, port))
        
        # Query ip-api.com for IP + Country + Org in one shot
        target = "ip-api.com"
        target_port = 80
        
        req = bytearray()
        req.append(0) # version 0
        req.extend(u.bytes)
        req.append(0) # addon length
        req.append(1) # command TCP
        req.extend(target_port.to_bytes(2, 'big'))
        req.append(2) # domain
        req.append(len(target))
        req.extend(target.encode('ascii'))
        
        payload = f"GET /json HTTP/1.1\r\nHost: {target}\r\nUser-Agent: curl/7.68.0\r\nConnection: close\r\n\r\n".encode('ascii')
        s.sendall(req + payload)
        
        resp = b""
        while True:
            chunk = s.recv(4096)
            if not chunk:
                break
            resp += chunk
        s.close()
        
            if len(resp) > 2 and resp[0] == 0:
                addon_len = resp[1]
                http_data = resp[2 + addon_len:].decode('utf-8', errors='ignore')
                lines = http_data.split('\r\n\r\n', 1)
                body = lines[1].strip() if len(lines) > 1 else ""
                try:
                    info = json.loads(body)
                    return {
                        "ip": info.get("query", "Unknown"),
                        "country": info.get("country", "Unknown"),
                        "countryCode": info.get("countryCode", "Unknown"),
                        "org": info.get("org", info.get("isp", "Unknown"))
                    }
                except Exception:
                    return {"ip": body[:50], "country": "Raw", "org": "N/A"}
            else:
                last_err = f"Invalid response: {resp[:20]}"
        except Exception as e:
            last_err = e
    return {"error": str(last_err)}

def main():
    print("[*] Fetching candidate nodes from https://dash.icta.top:2096/sub/my ...")
    nodes = fetch_nodes_from_sub()
    print(f"[+] Found {len(nodes)} distinct test nodes in subscription.")
    
    print("\n" + "="*80)
    print(f"{'Node Type':<16} | {'Tag':<30} | {'Egress IP':<16} | {'Country':<12} | {'Org/ISP'}")
    print("="*80)
    
    order = ["native", "us", "jp", "nl"] + [k for k in sorted(nodes.keys()) if k not in ("native", "us", "jp", "nl")]
    for key in order:
        if key not in nodes:
            continue
        tag, uid, port = nodes[key]
        clean_tag = tag.split(" - ")[0].replace(" ♾", "").strip()
        res = test_vless_egress(uid, port)
        if "error" in res:
            print(f"{key:<16} | {clean_tag:<30} | {'ERROR':<16} | {'N/A':<12} | {res['error']}", flush=True)
        else:
            print(f"{key:<16} | {clean_tag:<30} | {res['ip']:<16} | {res['countryCode']+' ('+res['country']+')':<12} | {res['org']}", flush=True)
        time.sleep(0.5)

    print("="*80 + "\n", flush=True)

if __name__ == "__main__":
    main()
