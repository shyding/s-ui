"""
ProtonVPN Automated Browser Harvester for S-UI
Uses Playwright with real Chrome to authenticate and harvest ProtonVPN servers and WireGuard configs.
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

def harvest_nodes(headless=False, timeout_seconds=180):
    os.makedirs(PROFILE_DIR, exist_ok=True)
    os.makedirs(CONFIGS_DIR, exist_ok=True)

    results = {
        "success": False,
        "servers": [],
        "message": ""
    }

    with sync_playwright() as p:
        # Launch Chrome with persistent context to retain login session forever
        context = p.chromium.launch_persistent_context(
            user_data_dir=PROFILE_DIR,
            channel="chrome",
            headless=headless,
            args=[
                "--disable-blink-features=AutomationControlled",
                "--no-sandbox",
                "--disable-infobars"
            ],
            viewport={"width": 1280, "height": 850}
        )

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

        print(f"Opening ProtonVPN portal (Headless: {headless})...", flush=True)
        try:
            page.goto(WIREGUARD_PAGE, wait_until="domcontentloaded", timeout=45000)
        except Exception as e:
            print(f"Navigation warning: {e}", flush=True)

        start_time = time.time()
        logged_in = False

        while time.time() - start_time < timeout_seconds:
            current_url = page.url
            title = page.title()

            # Check if user needs to log in
            is_login_page = ("login" in current_url) or ("登录" in title) or ("Login" in title)
            
            if is_login_page:
                if headless:
                    results["message"] = "Proton session not authenticated. Please launch in browser mode (without --headless) to log in once."
                    print(results["message"], flush=True)
                    context.close()
                    return results
                print("Waiting for user to log in via opened Chrome window...", flush=True)
                page.wait_for_timeout(2000)
            elif "account.proton.me" in current_url:
                # User is logged in to account.proton.me
                logged_in = True
                break
            page.wait_for_timeout(1000)

        if not logged_in:
            results["message"] = "Timeout waiting for user authentication in browser."
            context.close()
            return results

        print(f"Proton account session authenticated at {page.url}!", flush=True)
        page.wait_for_timeout(4000)

        # Attempt in-page API execution with valid browser credentials
        if not captured_logicals:
            for retry in range(5):
                try:
                    logicals_data = page.evaluate("""
                        async () => {
                            const appVersions = ['linux-vpn@4.14.1', 'web-vpn@5.0.421.0', 'web-account@5.0.421.0'];
                            for (const ver of appVersions) {
                                try {
                                    const res = await fetch('/api/vpn/logicals', {
                                        headers: {
                                            'Accept': 'application/vnd.protonmail.v1+json',
                                            'x-pm-appversion': ver
                                        }
                                    });
                                    if (res.ok) {
                                        const json = await res.json();
                                        if (json.LogicalServers && json.LogicalServers.length > 0) {
                                            return json.LogicalServers;
                                        }
                                    }
                                } catch (e) {}
                            }
                            return null;
                        }
                    """)
                    if logicals_data and len(logicals_data) > 0:
                        captured_logicals = logicals_data
                        print(f"Successfully retrieved {len(captured_logicals)} logical servers via browser fetch!", flush=True)
                        break
                except Exception as e:
                    print(f"Evaluate retry {retry+1} warning: {e}", flush=True)
                page.wait_for_timeout(2000)

        context.close()

        if captured_logicals:
            results["success"] = True
            results["servers"] = captured_logicals
            results["message"] = f"Successfully harvested {len(captured_logicals)} ProtonVPN servers."
        else:
            results["message"] = "Authenticated, but server listing was not returned by API."

    return results

if __name__ == "__main__":
    is_headless = "--headless" in sys.argv
    res = harvest_nodes(headless=is_headless)
    # Output single clean JSON line for caller to parse
    print("---SUI_HARVEST_START---")
    print(json.dumps(res))
    print("---SUI_HARVEST_END---")
