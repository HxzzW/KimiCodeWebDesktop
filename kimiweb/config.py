# -*- coding: utf-8 -*-
"""全局常量与本地状态(state.json)存取。"""

import json
import os

APP_TITLE = "Kimi Web"
PREFERRED_PORT = 58627  # kimi web 默认端口
PORT_SCAN_LIMIT = 20
STARTUP_TIMEOUT = 60  # 秒
CHECK_INTERVAL = 24 * 3600  # 更新检测间隔(秒)
WEBVIEW_DATA_DIR = os.path.join(
    os.environ.get("LOCALAPPDATA", os.path.expanduser("~")), "KimiWeb"
)
STATE_FILE = os.path.join(WEBVIEW_DATA_DIR, "state.json")
SERVER_LOG = os.path.join(WEBVIEW_DATA_DIR, "kimi-web.log")

CREATE_NO_WINDOW = 0x08000000
PROCESS_QUERY_LIMITED_INFORMATION = 0x1000
STILL_ACTIVE = 259
ERROR_ALREADY_EXISTS = 183

NPM_LATEST_URLS = [
    "https://registry.npmjs.org/@moonshot-ai/kimi-code/latest",
    "https://registry.npmmirror.com/@moonshot-ai/kimi-code/latest",
]
UPGRADE_COMMAND = "irm https://code.kimi.com/kimi-code/install.ps1 | iex"
WEBVIEW2_GUID = r"{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"

MB_YESNOCANCEL = 0x3
MB_YESNO = 0x4
MB_ICONQUESTION = 0x20
MB_ICONWARNING = 0x30
MB_ICONINFORMATION = 0x40
MB_SETFOREGROUND = 0x10000
MB_TOPMOST = 0x40000
IDCANCEL = 2
IDYES = 6


def load_state():
    try:
        with open(STATE_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    except (OSError, ValueError):
        return {}


def save_state(state):
    try:
        os.makedirs(WEBVIEW_DATA_DIR, exist_ok=True)
        with open(STATE_FILE, "w", encoding="utf-8") as f:
            json.dump(state, f)
    except OSError:
        pass
