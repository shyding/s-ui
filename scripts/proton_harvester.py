"""
ProtonVPN Automated Browser Harvester for S-UI
Supports automated login with username & password, interactive browser mode, and silent background refreshes.
Zero manual token/cookie copy-paste required.
"""

import sys
import os
import json
import time
from pathlib import Path
from playwright.sync_api import sync_playwright

PROFILE_DIR = os.path.join(str(Path.home()), ".sui_proton_profile")
CONFIGS_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "harvested_configs")
WIREGUARD_PAGE = "https://account.proton.me/u/0/vpn/wireguard"
LOGIN_PAGE = "https://account.proton.me/login"

def harvest_nodes(username="", password="", headless=False, timeout_seconds=120):
    os.makedirs(PROFILE_DIR, exist_ok=True)
    os.makedirs(CONFIGS_DIR, exist_ok=True)

    results = {
        "success": False,
        "servers": [],
        "message": ""
    }

    with sync_playwright() as p:
        # Launch Chromium (fallback gracefully if chrome channel unavailable)
        launch_kwargs = {
            "user_data_dir": PROFILE_DIR,
            "headless": headless,
            "args": [
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--disable-infobars",
                "--disable-dev-shm-usage"
            ],
            "viewport": {"width": 1280, "height": 850}
        }
        if sys.platform == "win32":
            launch_kwargs["channel"] = "chrome"

        try:
            context = p.chromium.launch_persistent_context(**launch_kwargs)
        except Exception:
            # Fallback to default chromium
            launch_kwargs.pop("channel", None)
            context = p.chromium.launch_persistent_context(**launch_kwargs)

        page = context.pages[0] if context.pages else context.new_page()

        captured_logicals = None

        def handle_response(response):
            nonlocal captured_logicals
            if "logicals" in response.url and response.status == 200:
                try:
                    data = response.json()
                    if "LogicalServers" in data and len(data["LogicalServers"]) > 0:
                        captured_logicals = data["LogicalServers"]
                        print(f"Captured {len(captured_logicals)} logical servers from response stream!", flush=True)
                except Exception:
                    pass

        page.on("response", handle_response)

        print(f"Opening ProtonVPN portal (Headless: {headless}, AutoLogin: {bool(username)})...", flush=True)
        try:
            page.goto("https://account.proton.me/vpn", wait_until="domcontentloaded", timeout=40000)
        except Exception as e:
            print(f"Navigation warning: {e}", flush=True)

        page.wait_for_timeout(2000)

        # Handle 2-Step Login if needed
        is_login = ("login" in page.url) or (page.url.endswith("/vpn"))
        if is_login and username:
            print("Executing automated 2-step credentials login...", flush=True)
            try:
                # Step 1: Fill username
                u_input = page.query_selector("input#username, input[autocomplete='username'], input[name='username']")
                if u_input and u_input.is_visible():
                    u_input.fill(username)
                    sub_btn = page.query_selector("button[type='submit']")
                    if sub_btn:
                        sub_btn.click()
                        print("Submitted username...", flush=True)
                        page.wait_for_timeout(2000)

                # Step 2: Fill password
                p_input = page.wait_for_selector("input#password, input[type='password']", timeout=12000)
                if p_input and password:
                    p_input.fill(password)
                    sub_btn2 = page.query_selector("button[type='submit']")
                    if sub_btn2:
                        sub_btn2.click()
                        print("Submitted password...", flush=True)

                # Wait for session redirect to /u/
                for _ in range(15):
                    page.wait_for_timeout(2000)
                    if "u/" in page.url:
                        break
            except Exception as e:
                print(f"Login error: {e}", flush=True)

        # Now navigate to wireguard config page to trigger logicals fetch
        if "u/" in page.url:
            wg_url = page.url.rstrip("/") + "/wireguard"
            print(f"Navigating to WireGuard page: {wg_url}...", flush=True)
            try:
                page.goto(wg_url, wait_until="domcontentloaded", timeout=30000)
                page.wait_for_timeout(6000)
            except Exception as e:
                print(f"WireGuard page navigation warning: {e}", flush=True)
        elif not headless:
            print("Waiting for user interaction in browser...", flush=True)
            for _ in range(30):
                page.wait_for_timeout(2000)
                if captured_logicals:
                    break

        # If not captured via response stream, trigger in-page evaluate
        if not captured_logicals and "account.proton.me" in page.url:
            print("Triggering in-page fetch for logicals...", flush=True)
            for retry in range(3):
                try:
                    logicals_data = page.evaluate("""
                        async () => {
                            const res = await fetch('/api/vpn/v1/logicals?WithIpV6=1', {
                                headers: {
                                    'Accept': 'application/vnd.protonmail.v1+json'
                                }
                            });
                            if (res.ok) {
                                const json = await res.json();
                                return json.LogicalServers || null;
                            }
                            return null;
                        }
                    """)
                    if logicals_data and len(logicals_data) > 0:
                        captured_logicals = logicals_data
                        print(f"Successfully retrieved {len(captured_logicals)} logical servers via evaluate!", flush=True)
                        break
                except Exception as e:
                    print(f"Evaluate retry {retry+1}: {e}", flush=True)
                page.wait_for_timeout(2000)

        context.close()

        cache_file = os.path.join(os.path.dirname(os.path.abspath(__file__)), "cached_logicals.json")
        if captured_logicals:
            results["success"] = True
            results["servers"] = captured_logicals
            results["message"] = f"Successfully harvested {len(captured_logicals)} ProtonVPN servers."
            try:
                with open(cache_file, "w", encoding="utf-8") as f:
                    json.dump(captured_logicals, f)
            except Exception:
                pass
        elif os.path.exists(cache_file):
            try:
                with open(cache_file, "r", encoding="utf-8") as f:
                    cached_data = json.load(f)
                if cached_data and len(cached_data) > 0:
                    results["success"] = True
                    results["servers"] = cached_data
                    results["message"] = f"Successfully loaded {len(cached_data)} cached ProtonVPN servers."
            except Exception:
                pass
        else:
            results["message"] = "Login attempted, but server listing was not captured."

    return results

if __name__ == "__main__":
    is_headless = "--headless" in sys.argv
    user = ""
    pwd = ""
    for i, a in enumerate(sys.argv):
        if a == "--username" and i + 1 < len(sys.argv):
            user = sys.argv[i + 1]
        elif a == "--password" and i + 1 < len(sys.argv):
            pwd = sys.argv[i + 1]

    res = harvest_nodes(username=user, password=pwd, headless=is_headless)
    # Output single clean JSON line for caller to parse
    print("---SUI_HARVEST_START---")
    print(json.dumps(res))
    print("---SUI_HARVEST_END---")
