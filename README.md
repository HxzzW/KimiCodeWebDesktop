# KimiCodeWebDesktop

[Kimi Code CLI](https://www.kimi.com/code/docs/en/) 的 Web UI(`kimi web`)Windows 桌面启动器:双击即用,以内嵌 WebView2 窗口打开 Web UI,系统托盘常驻。

Go 实现,单文件约 9 MB、无控制台窗口、无 CGO/运行时依赖(WebView2Loader 已内嵌)。

## 功能

- **一键启动**:自动拉起隐藏的 `kimi web --no-open` 子进程(或复用已在运行的实例),服务就绪后打开桌面窗口
- **系统托盘**:关闭窗口只是隐藏到托盘,服务继续运行;托盘菜单提供 显示窗口 / 会话可视化(`kimi vis`) / 轮换 Token / 检查更新 / 重启服务 / 通知设置 / 退出
- **状态记忆**:固定端口 + 持久化 WebView2 数据目录,页面引导、登录状态等 localStorage 内容跨启动保留
- **复制粘贴可用**:WebView2 默认开启文本选择、右键菜单与快捷键
- **更新检测**:启动时对比本地 `kimi --version` 与 npm 上的最新版(24 小时内最多查一次,支持国内镜像),发现新版本时询问是否升级,可跳过特定版本
- **崩溃守护**:服务进程意外退出时弹窗询问是否重启;启动失败自动附 `kimi doctor` 诊断到日志
- **工作状态动画**:Kimi Code 工作时,窗口标题显示与浏览器一致的 ◐◓◑◒ 转轮动画、托盘图标带环绕圆点;单个会话完成时弹出带会话标题的完成通知并闪烁任务栏,有会话等待审批/提问时同样提醒(托盘"通知设置"里可分别开关动画/通知/提示音)
- **单实例保护**:重复双击只会提示"已在运行"
- **位置记忆**:窗口位置、大小、最大化状态跨启动恢复(掉出屏幕区域自动回退居中)
- **标题栏同色**:自动读取页面顶部背景色染到标题栏与边框,窗口和 Web 内容融为一体,主题切换自动跟随(Win11;Win10 退回深色标题栏)
- **Kimi 图标**:exe、窗口、托盘均为官方图标

## 环境要求

- Windows 10/11,已安装 WebView2 Runtime(启动时会自动检测,缺失会给出下载链接)
- 已安装 [Kimi Code CLI](https://www.kimi.com/code/docs/en/kimi-code-cli/guides/getting-started.html)(`kimi` 在 PATH 中,或位于 `~\.kimi-code\bin\kimi.exe`)

## 使用

直接运行 `kimiweb.exe`(或按下方说明自行编译)。

- **关闭窗口** = 隐藏到托盘,服务不停;**退出** 请用托盘菜单(退出时会停止本程序拉起的服务;复用的实例不受影响)
- **托盘图标**:左键单击 = 显示窗口,右键单击 = 弹出菜单
- 托盘"检查更新"升级 CLI 完成后会自动重启服务,让新版本生效
- 创建桌面快捷方式:

```powershell
.\make-shortcut.ps1
```

## 自行编译

无需系统安装 Go:下载 [go1.26+ windows-amd64 zip](https://golang.google.cn/dl/) 解压到项目内 `.toolchain/` 即可。

```sh
go mod download
go run github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest -o rsrc.syso versioninfo.json
CGO_ENABLED=0 go build -ldflags="-s -w -H windowsgui" -o kimiweb.exe .
```

## 项目结构

```
main.go           入口与全局异常处理
app.go            App:服务、窗口、看门狗、状态监视、动画
tray.go           系统托盘与菜单
config.go         常量与 state.json 存取
kimi.go           kimi web 服务的发现、拉起、就绪等待、kimi doctor
updater.go        CLI 版本检测与升级
monitor.go        会话工作状态轮询
win32.go          Windows 原语:消息框、互斥锁、窗口子类化、提示音等
icon.go           内嵌图标与托盘动画帧生成
notify.go         Windows 通知(go-toast)
kimi.ico/kimi.png 图标资源(内嵌)
versioninfo.json  exe 版本信息(goversioninfo)
```

## 工作原理

- 启动时先检测更新,再查 kimi 的实例注册表 `~/.kimi-code/server/instances/`(校验 pid 存活 + `/openapi.json` 指纹):有活实例直接复用,否则挑一个空闲端口(优先 58627)拉起新实例
- Web UI 通过 `http://127.0.0.1:<端口>/#token=<server.token>` 完成鉴权,token 读自 `~/.kimi-code/server.token`;托盘"轮换 Token"调用 `kimi web rotate-token` 并用新 token 重载页面
- WebView2 数据目录固定在 `%LOCALAPPDATA%\KimiWeb`,localStorage 因此能持久保存
- 服务日志与状态文件在 `%LOCALAPPDATA%\KimiWeb\`(`kimi-web.log`、`state.json`),看门狗每 2 秒检查一次服务进程存活
- 工作状态通过 `GET /api/v1/sessions` 轮询(`busy` / `main_turn_active` / `pending_interaction` 字段),忙时切换标题与托盘动画帧,忙完按需发 Windows toast 通知或提示音(系统音)
- 关窗隐藏通过对窗口过程做子类化拦截 `WM_CLOSE` 实现;托盘"退出"才真正停止服务并关闭

## 说明

- 升级检测源:npm registry 的 [`@moonshot-ai/kimi-code`](https://www.npmjs.com/package/@moonshot-ai/kimi-code)(备用 npmmirror);升级命令为官方 Windows 安装脚本 `irm https://code.kimi.com/kimi-code/install.ps1 | iex`
- 窗口与已有浏览器标签页访问的是同一个本地服务,会话数据共享(同一 `~/.kimi-code` 家目录)
- 主要依赖:[go-webview2](https://github.com/jchv/go-webview2)(WebView2 绑定)、[getlantern/systray](https://github.com/getlantern/systray)(托盘)、[go-toast](https://github.com/go-toast/toast)(通知)
