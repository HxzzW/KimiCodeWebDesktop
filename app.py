# -*- coding: utf-8 -*-
"""Kimi Web 桌面启动器。

启动 `kimi web --no-open` 作为隐藏子进程,等待本地服务就绪后,
用内嵌浏览器(WebView2)窗口打开 Web UI;关闭窗口即停止服务。

端口固定从 58627 起:若该端口已有 kimi web 实例在运行则直接复用,
否则拉起新实例(被占用时顺延)。同时关闭 pywebview 的隐私模式并
使用固定数据目录:来源(origin)与 localStorage 稳定,页面才能
跨启动记住“引导已完成”等状态,否则每次启动都会重新引导。
启动前还会检测 Kimi Code CLI 是否有新版本,由用户选择是否升级。
"""

import ctypes
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.request

import webview

APP_TITLE = "Kimi Web"
PREFERRED_PORT = 58627  # kimi web 默认端口
PORT_SCAN_LIMIT = 20
STARTUP_TIMEOUT = 60  # 秒
WEBVIEW_DATA_DIR = os.path.join(
    os.environ.get("LOCALAPPDATA", os.path.expanduser("~")), "KimiWeb"
)

CREATE_NO_WINDOW = 0x08000000

NPM_LATEST_URL = "https://registry.npmjs.org/@moonshot-ai/kimi-code/latest"
UPGRADE_COMMAND = "irm https://code.kimi.com/kimi-code/install.ps1 | iex"

MB_YESNO = 0x4
MB_ICONQUESTION = 0x20
MB_ICONWARNING = 0x30
MB_ICONINFORMATION = 0x40
IDYES = 6


def find_kimi():
    """定位 kimi 可执行文件:优先 PATH,其次默认安装位置。"""
    path = shutil.which("kimi")
    if path:
        return path
    default = os.path.expanduser(r"~\.kimi-code\bin\kimi.exe")
    if os.path.isfile(default):
        return default
    return None


def read_token():
    token_file = os.path.expanduser(r"~\.kimi-code\server.token")
    try:
        with open(token_file, "r", encoding="utf-8") as f:
            return f.read().strip()
    except OSError:
        return ""


def parse_version(text):
    """从字符串里提取 (major, minor, patch) 数字版本号。"""
    nums = re.findall(r"\d+", text)
    return tuple(int(n) for n in nums[:3])


def _norm(version):
    return (tuple(version) + (0, 0, 0))[:3]


def format_version(version):
    return ".".join(str(n) for n in _norm(version))


def current_version(kimi):
    """运行 kimi --version 取当前版本;失败返回空元组。"""
    try:
        result = subprocess.run(
            [kimi, "--version"],
            capture_output=True,
            text=True,
            timeout=15,
            creationflags=CREATE_NO_WINDOW,
        )
        return parse_version(result.stdout or result.stderr)
    except (OSError, subprocess.SubprocessError):
        return ()


def latest_version():
    """查询 npm registry 上的最新版本;网络失败返回空元组(静默跳过检测)。"""
    try:
        with urllib.request.urlopen(NPM_LATEST_URL, timeout=6) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        return parse_version(data.get("version", ""))
    except Exception:
        return ()


def message_box(text, flags):
    return ctypes.windll.user32.MessageBoxW(0, text, APP_TITLE, flags)


def check_for_updates(kimi):
    """启动前检测 Kimi Code CLI 更新;有新版本时询问用户是否升级。"""
    cur = current_version(kimi)
    new = latest_version()
    if not cur or not new or _norm(new) <= _norm(cur):
        return
    answer = message_box(
        f"检测到 Kimi Code CLI 新版本 v{format_version(new)}"
        f"(当前 v{format_version(cur)})。\n\n是否现在升级?",
        MB_YESNO | MB_ICONQUESTION,
    )
    if answer != IDYES:
        return
    # 官方 Windows 安装/升级脚本;开可见控制台窗口显示下载进度
    code = subprocess.call(
        ["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", UPGRADE_COMMAND]
    )
    if code == 0:
        message_box("升级完成,将以新版本启动。", MB_ICONINFORMATION)
    else:
        message_box("升级未完成(可能被取消或网络失败),将继续使用当前版本。", MB_ICONWARNING)


def pick_port():
    """优先使用固定端口(保持 localStorage 有效);被占用则顺延,
    都不空闲再交给系统随机分配。"""
    for port in range(PREFERRED_PORT, PREFERRED_PORT + PORT_SCAN_LIMIT):
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            try:
                s.bind(("127.0.0.1", port))
                return port
            except OSError:
                continue
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def wait_for_server(port, proc, timeout=STARTUP_TIMEOUT):
    """轮询直到服务有响应(哪怕 401/404 也说明已起来)。
    kimi web 在端口被占时会自动 +1,所以探测一小段连续端口。
    返回实际端口,失败返回 None。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if proc.poll() is not None:
            return None  # 进程已退出
        for p in range(port, port + 5):
            try:
                urllib.request.urlopen(f"http://127.0.0.1:{p}/", timeout=2)
                return p
            except urllib.error.HTTPError:
                return p  # 服务在线,只是路由/鉴权返回了错误码
            except OSError:
                continue
        time.sleep(0.3)
    return None


def probe_existing_server(port):
    """若首选端口上已有一个 kimi web 实例,直接复用,保持 origin 稳定。
    用 /openapi.json 做指纹识别,避免误连占用同一端口的无关服务。"""
    req = urllib.request.Request(f"http://127.0.0.1:{port}/openapi.json")
    token = read_token()
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=2) as resp:
            return b'"openapi"' in resp.read(4096)
    except Exception:
        return False


def patch_webview_settings():
    """pywebview 只在 debug 模式下开启右键菜单和 Ctrl+C/V 等快捷键,
    这里打补丁强制开启(仍不开启 DevTools)。"""
    try:
        from webview.platforms import edgechromium
    except Exception:
        return
    original = edgechromium.EdgeChrome.on_webview_ready

    def patched(self, sender, args):
        original(self, sender, args)
        try:
            if args.IsSuccess:
                settings = sender.CoreWebView2.Settings
                settings.AreDefaultContextMenusEnabled = True
                settings.AreBrowserAcceleratorKeysEnabled = True
        except Exception:
            pass

    edgechromium.EdgeChrome.on_webview_ready = patched


def start_webview():
    """统一启动参数:关闭隐私模式并指定固定数据目录,
    localStorage/cookies 才能跨启动保留。"""
    webview.start(private_mode=False, storage_path=WEBVIEW_DATA_DIR)


def show_error(message):
    webview.create_window(
        APP_TITLE,
        html=f"<h3 style='font-family:sans-serif'>{message}</h3>",
        width=640,
        height=300,
        text_select=True,
    )
    start_webview()


def main():
    patch_webview_settings()

    kimi = find_kimi()
    if not kimi:
        show_error("未找到 kimi 命令,请先安装 Kimi Code CLI。")
        return 1

    check_for_updates(kimi)

    proc = None
    if probe_existing_server(PREFERRED_PORT):
        # 已有 kimi web 实例在跑,直接挂上去;窗口关闭时不停止它
        port = PREFERRED_PORT
    else:
        port = pick_port()
        proc = subprocess.Popen(
            [kimi, "web", "--no-open", "--port", str(port)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            creationflags=CREATE_NO_WINDOW,
        )

        port = wait_for_server(port, proc)
        if port is None:
            proc.terminate()
            show_error("kimi web 服务启动失败或超时。")
            return 1

    token = read_token()
    url = f"http://127.0.0.1:{port}/"
    if token:
        url += f"#token={token}"

    window = webview.create_window(
        APP_TITLE, url, width=1280, height=840, min_size=(800, 600), text_select=True
    )

    def on_closed():
        # 窗口关闭即停止 kimi web 服务(仅当服务是本进程拉起的)
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()

    window.events.closed += on_closed
    start_webview()
    return 0


if __name__ == "__main__":
    sys.exit(main())
