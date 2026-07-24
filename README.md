# KimiCodeWebDesktop

[Kimi Code CLI](https://www.kimi.com/code/docs/en/) 的 Web UI(`kimi web`)Windows 桌面启动器:双击即用,以内嵌 WebView2 窗口打开 Web UI,关掉窗口即停止服务。

## 功能

- **一键启动**:自动拉起隐藏的 `kimi web --no-open` 子进程,服务就绪后打开桌面窗口;关闭窗口自动停止服务
- **状态记忆**:固定端口(58627,已有实例则直接复用)+ 持久化 WebView2 数据目录,页面引导、登录状态等 localStorage 内容跨启动保留
- **复制粘贴可用**:开启了文本选择、右键菜单和 Ctrl+C/V 等快捷键(不开 DevTools)
- **更新检测**:启动时对比本地 `kimi --version` 与 npm 上的最新版,发现新版本时询问是否升级(走官方安装脚本,可选跳过)
- **Kimi 图标**:exe 内嵌官方图标

## 环境要求

- Windows 10/11(自带 WebView2 Runtime)
- 已安装 [Kimi Code CLI](https://www.kimi.com/code/docs/en/kimi-code-cli/guides/getting-started.html)(`kimi` 在 PATH 中,或位于 `~\.kimi-code\bin\kimi.exe`)

## 使用

直接运行打包好的 `dist/KimiWeb.exe`(或自行打包,见下)。创建桌面快捷方式:

```powershell
.\make-shortcut.ps1
```

## 自行打包

```sh
python -m venv .venv
.venv/Scripts/pip install pywebview pyinstaller
.venv/Scripts/python -m PyInstaller KimiWeb.spec --noconfirm
```

产物在 `dist/KimiWeb.exe`(单文件、无控制台窗口)。

## 工作原理

- `app.py` 启动时先检测更新,再探测 58627 端口:若已有 kimi web 实例(用 `/openapi.json` 指纹识别)则直接复用,否则挑一个空闲端口(优先 58627)拉起新实例
- Web UI 通过 `http://127.0.0.1:<端口>/#token=<server.token>` 完成鉴权,token 读自 `~/.kimi-code/server.token`
- WebView2 数据目录固定在 `%LOCALAPPDATA%\KimiWeb`,关闭 pywebview 默认的隐私模式,localStorage 因此能持久保存
- pywebview 默认只在 debug 模式开启右键菜单和快捷键,这里通过补丁强制开启(`AreDefaultContextMenusEnabled` / `AreBrowserAcceleratorKeysEnabled`)

## 说明

- 升级检测源:npm registry 的 [`@moonshot-ai/kimi-code`](https://www.npmjs.com/package/@moonshot-ai/kimi-code);升级命令为官方 Windows 安装脚本 `irm https://code.kimi.com/kimi-code/install.ps1 | iex`
- 窗口与已有浏览器标签页访问的是同一个本地服务,会话数据共享(同一 `~/.kimi-code` 家目录)
