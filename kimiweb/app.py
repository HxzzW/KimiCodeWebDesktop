# -*- coding: utf-8 -*-
"""KimiWebApp:管理 kimi web 服务、主窗口、看门狗与系统托盘。"""

import os
import subprocess
import threading

import pystray
import webview
from PIL import Image

from .config import (
    APP_TITLE,
    CREATE_NO_WINDOW,
    IDYES,
    MB_ICONWARNING,
    MB_YESNO,
    SERVER_LOG,
    WEBVIEW_DATA_DIR,
)
from .notify import (
    ANIM_INTERVAL,
    SPINNER_CHARS,
    fetch_activity,
    get_setting,
    make_spinner_frames,
    play_completion_sound,
    set_setting,
)
from .server import find_running_server, pick_port, run_doctor, wait_for_server
from .updater import check_for_updates
from .utils import read_token, resource_path
from .webview_ext import start_webview
from .winapi import message_box


class KimiWebApp:
    def __init__(self, kimi):
        self.kimi = kimi
        self.proc = None  # 本进程拉起的 kimi web;复用别人实例时为 None
        self.log = None
        self.port = None
        self.vis_proc = None
        self.window = None
        self.tray = None
        self.base_icon = None
        self.spinner_frames = None
        self.quitting = threading.Event()
        self.animating = threading.Event()

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

    # ---- 工作状态动画与完成通知 ----

    def activity_monitor(self):
        """轮询会话工作状态:忙时开启动画,忙完发通知,有会话等待交互时提醒。"""
        was_busy = False
        pending = False
        while not self.quitting.wait(2):
            result = fetch_activity(self.port, read_token())
            if result is None:
                continue
            busy, wait_input = result
            if busy != was_busy:
                was_busy = busy
                if busy:
                    self.start_animation()
                else:
                    self.stop_animation()
                    self.on_work_done()
            if wait_input and not pending:
                pending = True
                if get_setting("notify_toast"):
                    self.notify("有会话等待你的操作(审批或提问)")
            elif not wait_input:
                pending = False

    def on_work_done(self):
        if get_setting("notify_toast"):
            self.notify("Kimi Code 已完成当前任务")
        if get_setting("notify_sound"):
            play_completion_sound()

    def start_animation(self):
        if get_setting("anim_busy"):
            self.animating.set()

    def stop_animation(self):
        self.animating.clear()

    def _animate_loop(self):
        """常驻动画线程(整个生命周期只此一个,避免重叠写标题)。
        animating 置位期间驱动标题与托盘帧,清除后恢复静态。"""
        i = 0
        was_active = False
        while not self.quitting.is_set():
            if self.animating.is_set():
                try:
                    self.window.set_title(
                        f"{APP_TITLE} {SPINNER_CHARS[i % len(SPINNER_CHARS)]} 工作中"
                    )
                    if self.tray is not None and self.spinner_frames and i % 2 == 0:
                        # 托盘变化频率减半,太频繁会像在闪
                        self.tray.icon = self.spinner_frames[(i // 2) % len(self.spinner_frames)]
                except Exception:
                    pass
                i += 1
                was_active = True
                # 注意:不能用 self.animating.wait() 当延时——它处于置位状态
                # 时会立即返回(动画全速空转);quitting 未置位,可正常阻塞
                self.quitting.wait(ANIM_INTERVAL)
            else:
                if was_active:
                    was_active = False
                    i = 0
                    try:
                        self.window.set_title(APP_TITLE)
                        if self.tray is not None and self.base_icon is not None:
                            self.tray.icon = self.base_icon
                    except Exception:
                        pass
                self.quitting.wait(0.2)

    def _toggle_setting(self, key):
        set_setting(key, not get_setting(key))

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

    def _run_async(self, func, *args, **kwargs):
        """托盘动作放到独立工作线程:不阻塞托盘消息循环,
        弹窗也在干净的线程里显示(modal 循环不受托盘循环干扰)。"""
        threading.Thread(target=func, args=args, kwargs=kwargs, daemon=True).start()

    def setup_tray(self):
        self.base_icon = Image.open(resource_path("kimi.ico"))
        self.spinner_frames = make_spinner_frames(self.base_icon)
        settings_menu = pystray.Menu(
            pystray.MenuItem(
                "工作时动画",
                lambda icon, item: self._toggle_setting("anim_busy"),
                checked=lambda item: get_setting("anim_busy"),
            ),
            pystray.MenuItem(
                "完成时通知",
                lambda icon, item: self._toggle_setting("notify_toast"),
                checked=lambda item: get_setting("notify_toast"),
            ),
            pystray.MenuItem(
                "完成时提示音",
                lambda icon, item: self._toggle_setting("notify_sound"),
                checked=lambda item: get_setting("notify_sound"),
            ),
        )
        menu = pystray.Menu(
            pystray.MenuItem("显示窗口", lambda icon, item: self.show_window(), default=True),
            pystray.MenuItem("会话可视化", lambda icon, item: self._run_async(self.toggle_vis)),
            pystray.MenuItem("轮换 Token", lambda icon, item: self._run_async(self.rotate_token)),
            pystray.MenuItem(
                "检查更新",
                lambda icon, item: self._run_async(check_for_updates, self.kimi, force=True),
            ),
            pystray.MenuItem("重启服务", lambda icon, item: self._run_async(self.restart_service)),
            pystray.MenuItem("通知设置", settings_menu),
            pystray.Menu.SEPARATOR,
            pystray.MenuItem("退出", lambda icon, item: self.quit()),
        )
        self.tray = pystray.Icon("KimiWeb", self.base_icon, APP_TITLE, menu)
        threading.Thread(target=self.tray.run, daemon=True).start()

    def run(self):
        self.create_window()
        self.setup_tray()
        threading.Thread(target=self.watchdog, daemon=True).start()
        threading.Thread(target=self.activity_monitor, daemon=True).start()
        threading.Thread(target=self._animate_loop, daemon=True).start()
        start_webview()  # 阻塞,直到窗口真正关闭
        return 0
