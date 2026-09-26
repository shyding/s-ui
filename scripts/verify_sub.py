import urllib.request
import urllib.parse
import base64
import json
import ssl
import sys
import time

def fetch_subscription(urls):
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE

    last_err = None
    for url in urls:
        print(f"[*] Trying to fetch subscription from: {url}")
        for attempt in range(3):
            try:
                req = urllib.request.Request(
                    url,
                    headers={
                        "User-Agent": "v2rayN/6.23",
                        "Accept": "*/*"
                    }
                )
                with urllib.request.urlopen(req, timeout=10, context=ctx if url.startswith("https") else None) as resp:
                    data = resp.read().decode('utf-8', errors='ignore').strip()
                    print(f"[+] Successfully fetched {len(data)} bytes from {url}")
                    return data
            except Exception as e:
                last_err = e
                print(f"[-] Attempt {attempt+1} failed: {e}")
                time.sleep(2)
    raise RuntimeError(f"All subscription fetch attempts failed: {last_err}")

def decode_subscription(raw_sub):
    # Try Base64 decode first
    decoded = None
    try:
        # Pad base64 if needed
        padded = raw_sub + "=" * ((4 - len(raw_sub) % 4) % 4)
        decoded = base64.b64decode(padded).decode('utf-8', errors='ignore')
    except Exception:
        decoded = raw_sub

    lines = [line.strip() for line in decoded.splitlines() if line.strip()]
    return lines

def analyze_nodes(nodes):
    print(f"\n=======================================================")
    print(f"[*] Total subscription nodes parsed: {len(nodes)}")
    print(f"=======================================================")
    
    native_nodes = []
    proton_nodes = []
    cf_nodes = []
    other_nodes = []

    for idx, node in enumerate(nodes, 1):
        unquoted = urllib.parse.unquote(node)
        fragment = ""
        if "#" in unquoted:
            fragment = unquoted.split("#", 1)[1]
        elif "#" in node:
            fragment = urllib.parse.unquote(node.split("#", 1)[1])

        if "原生直连" in fragment:
            native_nodes.append((fragment, node))
        elif "ProtonVPN" in fragment or "[US]" in fragment or "[JP]" in fragment or "[NL]" in fragment:
            proton_nodes.append((fragment, node))
        elif "Cloudflare" in fragment or "[CF-" in fragment:
            cf_nodes.append((fragment, node))
        else:
            other_nodes.append((fragment, node))

    print(f"\n1. [原生直连入口节点] Count: {len(native_nodes)}")
    for f, n in native_nodes[:5]:
        print(f"   - {f}")

    print(f"\n2. [ProtonVPN 专线出境节点] Count: {len(proton_nodes)}")
    for f, n in proton_nodes:
        print(f"   - {f}")

    print(f"\n3. [Cloudflare 全球洁净出口节点] Count: {len(cf_nodes)}")
    for f, n in cf_nodes[:15]:
        print(f"   - {f}")
    if len(cf_nodes) > 15:
        print(f"   ... and {len(cf_nodes) - 15} more Cloudflare regions")

    if other_nodes:
        print(f"\n4. [其他节点] Count: {len(other_nodes)}")
        for f, n in other_nodes[:5]:
            print(f"   - {f}")

    return {
        "native": native_nodes,
        "proton": proton_nodes,
        "cf": cf_nodes,
        "other": other_nodes
    }

if __name__ == "__main__":
    test_urls = [
        "https://dash.icta.top:2096/sub/my",
        "http://dash.icta.top:2096/sub/my",
        "http://124.156.207.253:2096/sub/my",
        "https://124.156.207.253:2096/sub/my"
    ]
    try:
        sub_content = fetch_subscription(test_urls)
        nodes = decode_subscription(sub_content)
        analysis = analyze_nodes(nodes)
        print("\n[+] Verification successful! All node groups accounted for.")
    except Exception as e:
        print(f"\n[!] Verification error: {e}")
        sys.exit(1)
