# -*- coding: utf-8 -*-
"""Kimi Web 桌面启动器。

启动 `kimi web --no-open` 作为隐藏子进程,等待本地服务就绪后,
用内嵌浏览器(WebView2)窗口打开 Web UI;关闭窗口即停止服务。

端口固定从 58627 起:先查 kimi 的实例注册表(~/.kimi-code/server/instances/),
有活着的 kimi web 实例就直接复用,否则拉起新实例(端口被占时顺延)。
同时关闭 pywebview 的隐私模式并使用固定数据目录:来源(origin)与
localStorage 稳定,页面才能跨启动记住“引导已完成”等状态。
启动前还会检测 Kimi Code CLI 更新(每 24 小时最多一次),由用户选择是否升级。
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
from ctypes import wintypes

import webview

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

MB_YESNOCANCEL = 0x3
MB_YESNO = 0x4
MB_ICONQUESTION = 0x20
MB_ICONWARNING = 0x30
MB_ICONINFORMATION = 0x40
IDCANCEL = 2
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
    """查询最新版本(npm registry,失败走国内镜像);网络失败返回空元组。"""
    for url in NPM_LATEST_URLS:
        try:
            with urllib.request.urlopen(url, timeout=6) as resp:
                data = json.loads(resp.read().decode("utf-8"))
            version = parse_version(data.get("version", ""))
            if version:
                return version
        except Exception:
            continue
    return ()


def message_box(text, flags):
    return ctypes.windll.user32.MessageBoxW(0, text, APP_TITLE, flags)


def check_for_updates(kimi):
    """启动前检测 Kimi Code CLI 更新(每 24h 最多一次,离线也计入,
    避免每次启动都卡在超时上);有新版本时询问:升级 / 本次跳过 / 跳过此版本。"""
    state = load_state()
    if time.time() - state.get("last_check", 0) < CHECK_INTERVAL:
        return
    new = latest_version()
    state["last_check"] = time.time()
    save_state(state)
    if not new:
        return
    cur = current_version(kimi)
    if not cur or _norm(new) <= _norm(cur):
        return
    if format_version(new) == state.get("skip_version"):
        return
    answer = message_box(
        f"检测到 Kimi Code CLI 新版本 v{format_version(new)}"
        f"(当前 v{format_version(cur)})。\n\n"
        "是:立即升级\n否:本次跳过\n取消:跳过此版本,不再提示",
        MB_YESNOCANCEL | MB_ICONQUESTION,
    )
    if answer == IDCANCEL:
        state["skip_version"] = format_version(new)
        save_state(state)
        return
    if answer != IDYES:
        return
    # 官方 Windows 安装/升级脚本;开可见控制台窗口显示下载进度
    code = subprocess.call(
        ["powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", UPGRADE_COMMAND]
    )
    if code == 0:
        state.pop("skip_version", None)
        save_state(state)
        message_box("升级完成,将以新版本启动。", MB_ICONINFORMATION)
    else:
        message_box("升级未完成(可能被取消或网络失败),将继续使用当前版本。", MB_ICONWARNING)


def probe_existing_server(port):
    """探测指定端口是否是 kimi web:用 /openapi.json 做指纹识别,
    避免误连占用同一端口的无关服务。"""
    req = urllib.request.Request(f"http://127.0.0.1:{port}/openapi.json")
    token = read_token()
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=2) as resp:
            return b'"openapi"' in resp.read(4096)
    except Exception:
        return False


def pid_alive(pid):
    """判断进程是否存活(注意:Windows 上不能用 os.kill(pid, 0),那会杀进程)。"""
    handle = ctypes.windll.kernel32.OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, False, pid)
    if not handle:
        return False
    try:
        code = wintypes.DWORD()
        if not ctypes.windll.kernel32.GetExitCodeProcess(handle, ctypes.byref(code)):
            return False
        return code.value == STILL_ACTIVE
    finally:
        ctypes.windll.kernel32.CloseHandle(handle)


def find_running_server():
    """从 kimi 的实例注册表找一个活着的 kimi web 实例(校验 pid 存活 +
    openapi 指纹),返回其端口;没有则返回 None。"""
    instances_dir = os.path.expanduser(r"~\.kimi-code\server\instances")
    try:
        files = sorted(
            (
                os.path.join(instances_dir, f)
                for f in os.listdir(instances_dir)
                if f.endswith(".json")
            ),
            key=os.path.getmtime,
            reverse=True,
        )
    except OSError:
        return None
    for path in files:
        try:
            with open(path, "r", encoding="utf-8") as f:
                info = json.load(f)
        except (OSError, ValueError):
            continue
        pid, host, port = info.get("pid"), info.get("host"), info.get("port")
        if not isinstance(pid, int) or not isinstance(port, int):
            continue
        if host != "127.0.0.1":
            continue
        if pid_alive(pid) and probe_existing_server(port):
            return port
    return None


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
    """轮询直到服务就绪(用 openapi 指纹确认,避免误判无关服务)。
    kimi web 在端口被占时会自动 +1,所以探测一小段连续端口。
    返回实际端口,失败返回 None。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        if proc.poll() is not None:
            return None  # 进程已退出
        for p in range(port, port + 5):
            if probe_existing_server(p):
                return p
        time.sleep(0.3)
    return None


def ensure_single_instance():
    """单实例保护:已在运行时提示并退出,避免重复窗口和重复更新弹窗。"""
    ctypes.windll.kernel32.CreateMutexW(None, False, "KimiWebSingleton")
    if ctypes.windll.kernel32.GetLastError() == ERROR_ALREADY_EXISTS:
        message_box("Kimi Web 已在运行,请使用已打开的窗口。", MB_ICONINFORMATION)
        return False
    return True


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
        html=f"<h3 style='font-family:sans-serif'>{message.replace(chr(10), '<br>')}</h3>",
        width=640,
        height=300,
        text_select=True,
    )
    start_webview()


def main():
    patch_webview_settings()
    if not ensure_single_instance():
        return 0

    kimi = find_kimi()
    if not kimi:
        show_error("未找到 kimi 命令,请先安装 Kimi Code CLI。")
        return 1

    check_for_updates(kimi)

    proc = None
    log = None
    port = find_running_server()
    if port is None:
        port = pick_port()
        os.makedirs(WEBVIEW_DATA_DIR, exist_ok=True)
        log = open(SERVER_LOG, "wb")  # 子进程输出留存,便于诊断启动失败
        proc = subprocess.Popen(
            [kimi, "web", "--no-open", "--port", str(port)],
            stdout=log,
            stderr=log,
            creationflags=CREATE_NO_WINDOW,
        )

        port = wait_for_server(port, proc)
        if port is None:
            proc.terminate()
            show_error(f"kimi web 服务启动失败或超时。\n日志见: {SERVER_LOG}")
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
        if log is not None:
            log.close()

    window.events.closed += on_closed
    start_webview()
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        # 无控制台窗口,异常不能静默吞掉,弹窗告知
        import traceback

        message_box(f"发生未处理的错误:\n\n{traceback.format_exc(limit=3)}", MB_ICONWARNING)
        sys.exit(1)
