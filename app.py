# -*- coding: utf-8 -*-
"""Kimi Web 桌面启动器。

启动 `kimi web --no-open` 作为隐藏子进程,等待本地服务就绪后,
用内嵌浏览器(WebView2)窗口打开 Web UI。

- 端口固定从 58627 起:先查 kimi 的实例注册表(~/.kimi-code/server/instances/),
  有活着的 kimi web 实例就直接复用,否则拉起新实例(端口被占时顺延)。
- 关闭 pywebview 的隐私模式并使用固定数据目录:来源(origin)与
  localStorage 稳定,页面才能跨启动记住“引导已完成”等状态。
- 系统托盘常驻:关闭窗口只是隐藏到托盘,服务继续运行;
  托盘菜单提供 显示窗口 / 会话可视化(kimi vis) / 轮换 Token / 检查更新 / 重启服务 / 退出。
- 启动前检测 Kimi Code CLI 更新(每 24 小时最多一次),由用户选择是否升级。
"""

import ctypes
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import winreg
from ctypes import wintypes

import pystray
import webview
from PIL import Image

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


def resource_path(name):
    """兼容 PyInstaller 单文件运行时的资源定位。"""
    base = getattr(sys, "_MEIPASS", os.path.dirname(os.path.abspath(__file__)))
    return os.path.join(base, name)


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


def check_for_updates(kimi, force=False):
    """检测 Kimi Code CLI 更新(每 24h 最多一次,离线也计入,避免每次启动
    都卡在超时上;force 跳过间隔与跳过记录,并给出结果反馈)。
    有新版本时询问:升级 / 本次跳过 / 跳过此版本。"""
    state = load_state()
    if not force and time.time() - state.get("last_check", 0) < CHECK_INTERVAL:
        return
    new = latest_version()
    state["last_check"] = time.time()
    save_state(state)
    if not new:
        if force:
            message_box("暂时无法检查更新:网络不可用。", MB_ICONWARNING)
        return
    cur = current_version(kimi)
    if not cur or _norm(new) <= _norm(cur):
        if force:
            message_box(f"已是最新版本 v{format_version(cur or new)}。", MB_ICONINFORMATION)
        return
    if not force and format_version(new) == state.get("skip_version"):
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


def run_doctor(kimi):
    """服务启动失败时运行 kimi doctor,把配置校验结果附加到日志。"""
    try:
        result = subprocess.run(
            [kimi, "doctor"],
            capture_output=True,
            text=True,
            timeout=20,
            creationflags=CREATE_NO_WINDOW,
        )
        output = (result.stdout + "\n" + result.stderr).strip()
    except (OSError, subprocess.SubprocessError):
        return
    if output:
        try:
            os.makedirs(WEBVIEW_DATA_DIR, exist_ok=True)
            with open(SERVER_LOG, "a", encoding="utf-8") as f:
                f.write("\n--- kimi doctor ---\n" + output + "\n")
        except OSError:
            pass


def webview2_runtime_available():
    """通过注册表检测 WebView2 Runtime 是否安装。"""
    for root in (winreg.HKEY_LOCAL_MACHINE, winreg.HKEY_CURRENT_USER):
        for scope in (r"SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients",
                      r"SOFTWARE\Microsoft\EdgeUpdate\Clients"):
            try:
                with winreg.OpenKey(root, scope + "\\" + WEBVIEW2_GUID) as key:
                    pv, _ = winreg.QueryValueEx(key, "pv")
                    if pv and pv != "0.0.0.0":
                        return True
            except OSError:
                continue
    return False


def ensure_single_instance():
    """单实例保护:已在运行时提示并退出,避免重复窗口和重复更新弹窗。"""
    ctypes.windll.kernel32.CreateMutexW(None, False, "KimiWebSingleton")
    if ctypes.windll.kernel32.GetLastError() == ERROR_ALREADY_EXISTS:
        message_box("Kimi Web 已在运行,请使用已打开的窗口或托盘图标。", MB_ICONINFORMATION)
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


class KimiWebApp:
    """管理 kimi web 服务、主窗口、看门狗与系统托盘。"""

    def __init__(self, kimi):
        self.kimi = kimi
        self.proc = None  # 本进程拉起的 kimi web;复用别人实例时为 None
        self.log = None
        self.port = None
        self.vis_proc = None
        self.window = None
        self.tray = None
        self.quitting = threading.Event()

    # ---- 服务管理 ----

    def build_url(self):
        url = f"http://127.0.0.1:{self.port}/"
        token = read_token()
        if token:
            url += f"#token={token}"
        return url

    def _spawn(self, port):
        os.makedirs(WEBVIEW_DATA_DIR, exist_ok=True)
        if self.log is None or self.log.closed:
            self.log = open(SERVER_LOG, "wb")  # 子进程输出留存,便于诊断
        self.proc = subprocess.Popen(
            [self.kimi, "web", "--no-open", "--port", str(port)],
            stdout=self.log,
            stderr=self.log,
            creationflags=CREATE_NO_WINDOW,
        )

    def start_service(self):
        """复用或拉起 kimi web;成功返回端口,失败返回 None。"""
        port = find_running_server()
        if port is not None:
            self.port = port
            return port
        port = pick_port()
        self._spawn(port)
        ready = wait_for_server(port, self.proc)
        if ready is None:
            self.proc.terminate()
            return None
        self.port = ready
        return ready

    def stop_service(self):
        # 只停止本进程拉起的服务;复用的实例不动
        if self.proc is not None and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.proc.kill()

    def restart_service(self):
        self.stop_service()
        if self.start_service() is None:
            run_doctor(self.kimi)
            message_box(f"服务重启失败。\n日志见: {SERVER_LOG}", MB_ICONWARNING)
            return
        self.window.load_url(self.build_url())

    def watchdog(self):
        """监视本进程拉起的服务,意外退出时询问是否重启。"""
        while not self.quitting.wait(2):
            if self.proc is None or self.proc.poll() is None:
                continue
            if self.quitting.is_set():
                return
            answer = message_box(
                "kimi web 服务意外退出。\n\n是否重启服务?", MB_YESNO | MB_ICONWARNING
            )
            if answer == IDYES:
                self.restart_service()
            else:
                return  # 用户不重启就不再监视

    # ---- 窗口 ----

    def create_window(self):
        self.window = webview.create_window(
            APP_TITLE,
            self.build_url(),
            width=1280,
            height=840,
            min_size=(800, 600),
            text_select=True,
        )
        self.window.events.closing += self.on_closing
        self.window.events.closed += self.on_closed

    def on_closing(self):
        # 返回 False 即取消关闭:平时关窗只是隐藏到托盘,
        # 托盘“退出”置位 quitting 后才真正关闭
        if self.quitting.is_set():
            return
        self.window.hide()
        return False

    def on_closed(self):
        self.quitting.set()
        self.stop_service()
        if self.vis_proc is not None and self.vis_proc.poll() is None:
            self.vis_proc.terminate()
        if self.tray is not None:
            self.tray.stop()
        if self.log is not None and not self.log.closed:
            self.log.close()

    def show_window(self):
        self.window.show()
        self.window.restore()

    # ---- 托盘 ----

    def notify(self, message):
        try:
            if self.tray is not None:
                self.tray.notify(message, APP_TITLE)
        except Exception:
            pass

    def toggle_vis(self):
        """启动/停止 kimi vis 会话可视化器。"""
        if self.vis_proc is not None and self.vis_proc.poll() is None:
            self.vis_proc.terminate()
            self.notify("会话可视化已停止")
            return
        try:
            self.vis_proc = subprocess.Popen(
                [self.kimi, "vis"],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                creationflags=CREATE_NO_WINDOW,
            )
            self.notify("会话可视化已启动,浏览器将自动打开")
        except OSError as e:
            message_box(f"kimi vis 启动失败: {e}", MB_ICONWARNING)

    def rotate_token(self):
        """轮换 web UI 的 bearer token,并用新 token 重载页面。"""
        try:
            result = subprocess.run(
                [self.kimi, "web", "rotate-token"],
                capture_output=True,
                text=True,
                timeout=15,
                creationflags=CREATE_NO_WINDOW,
            )
        except (OSError, subprocess.SubprocessError) as e:
            message_box(f"Token 轮换失败: {e}", MB_ICONWARNING)
            return
        if result.returncode == 0:
            self.window.load_url(self.build_url())
            self.notify("Token 已轮换,页面已用新令牌重新加载")
        else:
            message_box(f"Token 轮换失败: {result.stderr or result.stdout}", MB_ICONWARNING)

    def quit(self):
        if self.quitting.is_set():
            return
        self.quitting.set()
        if self.tray is not None:
            self.tray.stop()
        if self.window is not None:
            self.window.destroy()

    def setup_tray(self):
        image = Image.open(resource_path("kimi.ico"))
        menu = pystray.Menu(
            pystray.MenuItem("显示窗口", lambda icon, item: self.show_window(), default=True),
            pystray.MenuItem("会话可视化", lambda icon, item: self.toggle_vis()),
            pystray.MenuItem("轮换 Token", lambda icon, item: self.rotate_token()),
            pystray.MenuItem("检查更新", lambda icon, item: check_for_updates(self.kimi, force=True)),
            pystray.MenuItem("重启服务", lambda icon, item: self.restart_service()),
            pystray.Menu.SEPARATOR,
            pystray.MenuItem("退出", lambda icon, item: self.quit()),
        )
        self.tray = pystray.Icon("KimiWeb", image, APP_TITLE, menu)
        threading.Thread(target=self.tray.run, daemon=True).start()

    def run(self):
        self.create_window()
        self.setup_tray()
        threading.Thread(target=self.watchdog, daemon=True).start()
        start_webview()  # 阻塞,直到窗口真正关闭
        return 0


def main():
    patch_webview_settings()
    if not ensure_single_instance():
        return 0

    kimi = find_kimi()
    if not kimi:
        show_error("未找到 kimi 命令,请先安装 Kimi Code CLI。")
        return 1

    if not webview2_runtime_available():
        message_box(
            "未检测到 WebView2 运行时,本程序无法运行。\n\n"
            "请从微软官网下载安装 WebView2 Runtime:\n"
            "https://developer.microsoft.com/microsoft-edge/webview2/",
            MB_ICONWARNING,
        )
        return 1

    check_for_updates(kimi)

    app = KimiWebApp(kimi)
    if app.start_service() is None:
        run_doctor(kimi)
        show_error(f"kimi web 服务启动失败或超时。\n日志见: {SERVER_LOG}")
        return 1

    return app.run()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception:
        # 无控制台窗口,异常不能静默吞掉,弹窗告知
        import traceback

        message_box(f"发生未处理的错误:\n\n{traceback.format_exc(limit=3)}", MB_ICONWARNING)
        sys.exit(1)
