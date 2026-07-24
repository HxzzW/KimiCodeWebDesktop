# KimiCodeWebDesktop

[Kimi Code CLI](https://www.kimi.com/code/docs/en/) 的 Web UI(`kimi web`)Windows 桌面启动器:双击即用,以内嵌 WebView2 窗口打开 Web UI,系统托盘常驻。

## 功能

- **一键启动**:自动拉起隐藏的 `kimi web --no-open` 子进程(或复用已在运行的实例),服务就绪后打开桌面窗口
- **系统托盘**:关闭窗口只是隐藏到托盘,服务继续运行;托盘菜单提供 显示窗口 / 会话可视化(`kimi vis`) / 轮换 Token / 检查更新 / 重启服务 / 退出
- **状态记忆**:固定端口 + 持久化 WebView2 数据目录,页面引导、登录状态等 localStorage 内容跨启动保留
- **复制粘贴可用**:开启了文本选择、右键菜单和 Ctrl+C/V 等快捷键(不开 DevTools)
- **更新检测**:启动时对比本地 `kimi --version` 与 npm 上的最新版(24 小时内最多查一次,支持国内镜像),发现新版本时询问是否升级,可跳过特定版本
- **崩溃守护**:服务进程意外退出时弹窗询问是否重启;启动失败自动附 `kimi doctor` 诊断到日志
- **单实例保护**:重复双击只会提示"已在运行"
- **Kimi 图标**:exe 与托盘均内嵌官方图标

## 环境要求

- Windows 10/11,已安装 WebView2 Runtime(启动时会自动检测,缺失会给出下载链接)
- 已安装 [Kimi Code CLI](https://www.kimi.com/code/docs/en/kimi-code-cli/guides/getting-started.html)(`kimi` 在 PATH 中,或位于 `~\.kimi-code\bin\kimi.exe`)

## 使用

直接运行打包好的 `dist/KimiWeb.exe`(或自行打包,见下)。

- **关闭窗口** = 隐藏到托盘,服务不停;**退出** 请用托盘菜单(退出时会停止本程序拉起的服务;复用的实例不受影响)
- 创建桌面快捷方式:

```powershell
.\make-shortcut.ps1
```

## 自行打包

```sh
python -m venv .venv
.venv/Scripts/pip install pywebview pyinstaller pystray pillow
.venv/Scripts/python -m PyInstaller KimiWeb.spec --noconfirm
```

产物在 `dist/KimiWeb.exe`(单文件、无控制台窗口)。

## 工作原理

- 启动时先检测更新,再查 kimi 的实例注册表 `~/.kimi-code/server/instances/`(校验 pid 存活 + `/openapi.json` 指纹):有活实例直接复用,否则挑一个空闲端口(优先 58627)拉起新实例
- Web UI 通过 `http://127.0.0.1:<端口>/#token=<server.token>` 完成鉴权,token 读自 `~/.kimi-code/server.token`;托盘"轮换 Token"调用 `kimi web rotate-token` 并用新 token 重载页面
- WebView2 数据目录固定在 `%LOCALAPPDATA%\KimiWeb`,关闭 pywebview 默认的隐私模式,localStorage 因此能持久保存
- pywebview 默认只在 debug 模式开启右键菜单和快捷键,这里通过补丁强制开启(`AreDefaultContextMenusEnabled` / `AreBrowserAcceleratorKeysEnabled`)
- 服务日志与状态文件在 `%LOCALAPPDATA%\KimiWeb\`(`kimi-web.log`、`state.json`),看门狗线程每 2 秒检查一次服务进程存活

## 说明

- 升级检测源:npm registry 的 [`@moonshot-ai/kimi-code`](https://www.npmjs.com/package/@moonshot-ai/kimi-code)(备用 npmmirror);升级命令为官方 Windows 安装脚本 `irm https://code.kimi.com/kimi-code/install.ps1 | iex`
- 窗口与已有浏览器标签页访问的是同一个本地服务,会话数据共享(同一 `~/.kimi-code` 家目录)
