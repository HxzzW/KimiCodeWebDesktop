# -*- coding: utf-8 -*-
"""工作状态监视支持:会话轮询、托盘动画帧、完成提示音、开关设置。"""

import json
import math
import urllib.request
import winsound

from PIL import ImageDraw

from .config import load_state, save_state

# 通知/动画开关(存于 state.json)
SETTINGS_DEFAULTS = {
    "anim_busy": True,  # 工作时标题与托盘动画
    "notify_toast": True,  # 完成时 Windows 通知
    "notify_sound": False,  # 完成时提示音
}

SPINNER_CHARS = "⠋⠙⠚⠞⠖⠦⠴⠲⠳⠏"
ANIM_INTERVAL = 0.5  # 标题动画帧间隔(秒);托盘图标按其 2 倍速减半,避免闪烁感


def get_setting(key):
    return load_state().get(key, SETTINGS_DEFAULTS[key])


def set_setting(key, value):
    state = load_state()
    state[key] = value
    save_state(state)


def fetch_activity(port, token):
    """查询所有会话的工作状态。
    返回 (是否有会话在忙, 是否有会话等待交互);请求失败返回 None。"""
    req = urllib.request.Request(f"http://127.0.0.1:{port}/api/v1/sessions")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=3) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        items = (data.get("data") or {}).get("items") or []
    except Exception:
        return None
    busy = any(it.get("busy") or it.get("main_turn_active") for it in items)
    pending = any((it.get("pending_interaction") or "none") != "none" for it in items)
    return busy, pending


def make_spinner_frames(base_image, count=8, color=(59, 130, 246, 255)):
    """以基础托盘图标为底,生成带环绕小圆点的动画帧。"""
    base = base_image.convert("RGBA")
    w, h = base.size
    radius = min(w, h) * 0.36
    dot = max(3, min(w, h) // 6)
    cx, cy = w / 2, h / 2
    frames = []
    for i in range(count):
        angle = math.radians(i * 360 / count - 90)
        x = cx + radius * math.cos(angle)
        y = cy + radius * math.sin(angle)
        frame = base.copy()
        draw = ImageDraw.Draw(frame)
        draw.ellipse([x - dot / 2, y - dot / 2, x + dot / 2, y + dot / 2], fill=color)
        frames.append(frame)
    return frames


def play_completion_sound():
    """完成提示音(系统音,异步播放)。"""
    try:
        winsound.PlaySound("SystemAsterisk", winsound.SND_ALIAS | winsound.SND_ASYNC)
    except Exception:
        pass
