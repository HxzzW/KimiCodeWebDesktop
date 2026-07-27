package main

import (
	"os"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// ---- MessageBox(统一置顶并抢占前台:无控制台程序里弹窗容易被主窗口挡住) ----

const (
	mbYesNoCancel   = 0x3
	mbYesNo         = 0x4
	mbIconQuestion  = 0x20
	mbIconWarning   = 0x30
	mbIconInfo      = 0x40
	mbSetForeground = 0x10000
	mbTopMost       = 0x40000

	mbIconInformation = mbIconInfo
	idCancel          = 2
	idYes             = 6
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	winmm    = windows.NewLazySystemDLL("winmm.dll")

	procMessageBoxW        = user32.NewProc("MessageBoxW")
	procShowWindow         = user32.NewProc("ShowWindow")
	procSetForegroundWnd   = user32.NewProc("SetForegroundWindow")
	procSetWindowLongPtrW  = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW    = user32.NewProc("CallWindowProcW")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	procPostMessageW       = user32.NewProc("PostMessageW")
	procFlashWindowEx      = user32.NewProc("FlashWindowEx")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procGetWindowPlacement = user32.NewProc("GetWindowPlacement")
	procSetWindowPos       = user32.NewProc("SetWindowPos")
	procIsIconic           = user32.NewProc("IsIconic")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procCreateMutexW       = kernel32.NewProc("CreateMutexW")
	procPlaySoundW         = winmm.NewProc("PlaySoundW")
)

func messageBox(text string, flags uintptr) int {
	t, _ := windows.UTF16PtrFromString(text)
	title, _ := windows.UTF16PtrFromString(appTitle)
	r, _, _ := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(title)),
		flags|mbSetForeground|mbTopMost,
	)
	return int(r)
}

// ---- 进程/互斥 ----

// pidAlive 判断进程是否存活
func pidAlive(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if h == 0 || err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// ensureSingleInstance 单实例保护(与 Python 版同名互斥锁,两个版本不能同时跑)
func ensureSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString("KimiWebSingleton")
	_, _, e := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if e == windows.ERROR_ALREADY_EXISTS {
		messageBox("Kimi Web 已在运行,请使用已打开的窗口或托盘图标。", mbIconInformation)
		return false
	}
	return true
}

// ---- WebView2 运行时检测 ----

func webview2RuntimeAvailable() bool {
	roots := []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER}
	scopes := []string{
		`SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + webview2GUID,
		`SOFTWARE\Microsoft\EdgeUpdate\Clients\` + webview2GUID,
	}
	for _, root := range roots {
		for _, scope := range scopes {
			k, err := registry.OpenKey(root, scope, registry.READ)
			if err != nil {
				continue
			}
			pv, _, err := k.GetStringValue("pv")
			k.Close()
			if err == nil && pv != "" && pv != "0.0.0.0" {
				return true
			}
		}
	}
	return false
}

// ---- 窗口操作 ----

const (
	swHide    = 0
	swRestore = 9
	wmClose   = 0x0010
)

func showWindow(hwnd uintptr, cmdShow int) {
	_, _, _ = procShowWindow.Call(hwnd, uintptr(cmdShow))
}

func setForeground(hwnd uintptr) {
	_, _, _ = procSetForegroundWnd.Call(hwnd)
}

func postClose(hwnd uintptr) {
	_, _, _ = procPostMessageW.Call(hwnd, wmClose, 0, 0)
}

// ---- DWM 标题栏颜色 ----

var dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

var procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")

const (
	dwmwaUseImmersiveDarkMode = 20
	dwmwaBorderColor          = 34
	dwmwaCaptionColor         = 35
	dwmwaTextColor            = 36
)

// setCaptionColor 把标题栏/边框染成指定颜色(Win11),文本色按亮度选黑白;
// 不支持时(Win10)退回沉浸式深色标题栏
func setCaptionColor(hwnd uintptr, r, g, b uint8) {
	colorref := uint32(r) | uint32(g)<<8 | uint32(b)<<16
	ret, _, _ := procDwmSetWindowAttribute.Call(
		hwnd, dwmwaCaptionColor, uintptr(unsafe.Pointer(&colorref)), 4)
	if ret != 0 {
		v := uint32(1)
		_, _, _ = procDwmSetWindowAttribute.Call(
			hwnd, dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&v)), 4)
		return
	}
	lum := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	textColor := uint32(0xFFFFFF)
	if lum > 140 {
		textColor = 0
	}
	_, _, _ = procDwmSetWindowAttribute.Call(
		hwnd, dwmwaTextColor, uintptr(unsafe.Pointer(&textColor)), 4)
	_, _, _ = procDwmSetWindowAttribute.Call(
		hwnd, dwmwaBorderColor, uintptr(unsafe.Pointer(&colorref)), 4)
}

// ---- 任务栏闪烁 ----

const flashwAll = 0x3 // FLASHW_CAPTION | FLASHW_TRAY

type flashWInfo struct {
	CbSize  uint32
	Hwnd    uintptr
	Flags   uint32
	Count   uint32
	Timeout uint32
}

// flashWindow 闪烁窗口标题栏与任务栏按钮(窗口隐藏时无任务栏按钮可闪,无妨)
func flashWindow(hwnd uintptr, count uint32) {
	f := flashWInfo{
		CbSize: uint32(unsafe.Sizeof(flashWInfo{})),
		Hwnd:   hwnd,
		Flags:  flashwAll,
		Count:  count,
	}
	_, _, _ = procFlashWindowEx.Call(uintptr(unsafe.Pointer(&f)))
}

// ---- 窗口几何 ----

type winRect struct{ Left, Top, Right, Bottom int32 }

// windowPlacement 对应 WINDOWPLACEMENT 结构
type windowPlacement struct {
	Length, Flags, ShowCmd uint32
	PtMin, PtMax           [2]int32
	RcNormal               winRect
}

const swMaximize = 3

// getWindowPlacement 返回 showCmd 与正常状态(还原时)的窗口矩形,
// 最大化/最小化时也能拿到正确的还原矩形
func getWindowPlacement(hwnd uintptr) (showCmd, x, y, w, h int, ok bool) {
	var wp windowPlacement
	wp.Length = uint32(unsafe.Sizeof(wp))
	ret, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	if ret == 0 {
		return 0, 0, 0, 0, 0, false
	}
	r := wp.RcNormal
	return int(wp.ShowCmd), int(r.Left), int(r.Top), int(r.Right - r.Left), int(r.Bottom - r.Top), true
}

func setWindowRect(hwnd uintptr, x, y, w, h int) {
	const swpNoZOrder = 0x4
	_, _, _ = procSetWindowPos.Call(hwnd, 0,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder)
}

// virtualScreenBounds 所有显示器的联合区域(用于校验恢复的位置没掉出屏幕)
func virtualScreenBounds() (x, y, w, h int) {
	vx, _, _ := procGetSystemMetrics.Call(76) // SM_XVIRTUALSCREEN
	vy, _, _ := procGetSystemMetrics.Call(77) // SM_YVIRTUALSCREEN
	vw, _, _ := procGetSystemMetrics.Call(78) // SM_CXVIRTUALSCREEN
	vh, _, _ := procGetSystemMetrics.Call(79) // SM_CYVIRTUALSCREEN
	return int(vx), int(vy), int(vw), int(vh)
}

// ---- 关窗拦截(关窗改为隐藏到托盘) ----

const (
	gwlpWndProc    = -4
	wmExitSizeMove = 0x0232
	wmSize         = 0x0005
)

var oldWndProc uintptr

// subclassForCloseHide 子类化窗口过程:拦截 WM_CLOSE,平时隐藏,
// quitting 置位后才走原流程真正关闭;拖动/缩放/最大化结束时回调 onSizeMoveEnd
func subclassForCloseHide(hwnd uintptr, quitting *atomic.Int32, onSizeMoveEnd func()) {
	cb := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		switch msg {
		case wmClose:
			if quitting.Load() == 0 {
				showWindow(hwnd, swHide)
				return 0
			}
		case wmExitSizeMove, wmSize:
			if onSizeMoveEnd != nil {
				onSizeMoveEnd()
			}
		}
		r, _, _ := procCallWindowProcW.Call(oldWndProc, hwnd, uintptr(msg), wp, lp)
		return r
	})
	nIndex := int32(gwlpWndProc) // 变量才能转 uintptr(常量负值不允许)
	r, _, _ := procSetWindowLongPtrW.Call(hwnd, uintptr(nIndex), cb)
	oldWndProc = r
}

// ---- 托盘左键单击(左键显示窗口,右键维持弹菜单) ----

const (
	wmSystrayCallback = 0x0401 // systray 库的图标回调消息(WM_USER+1)
	wmLButtonUp       = 0x0202
)

var (
	procFindWindowExW            = user32.NewProc("FindWindowExW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
)

var oldTrayWndProc uintptr

// subclassTrayLeftClick 子类化 systray 的隐藏窗口:左键点托盘图标交给
// onLeftClick,其余消息(含右键)走原流程。systray v1.2.2 对左右键都弹菜单
// 且没有左键回调;其内部细节(类名 SystrayClass、回调消息 WM_USER+1)
// 该版本已冻结,可安全依赖
func subclassTrayLeftClick(onLeftClick func()) {
	class, _ := windows.UTF16PtrFromString("SystrayClass")
	var hwnd, prev uintptr
	for {
		h, _, _ := procFindWindowExW.Call(0, prev, uintptr(unsafe.Pointer(class)), 0)
		if h == 0 {
			return // 没找到(理论不会发生):维持原行为
		}
		var pid uint32
		_, _, _ = procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
		if pid == uint32(os.Getpid()) { // 同名类可能属于别的进程,只认自己的
			hwnd = h
			break
		}
		prev = h
	}
	cb := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		if msg == wmSystrayCallback && lp == wmLButtonUp {
			onLeftClick()
			return 0
		}
		r, _, _ := procCallWindowProcW.Call(oldTrayWndProc, hwnd, uintptr(msg), wp, lp)
		return r
	})
	nIndex := int32(gwlpWndProc)
	r, _, _ := procSetWindowLongPtrW.Call(hwnd, uintptr(nIndex), cb)
	oldTrayWndProc = r
}

// ---- 提示音 ----

func playCompletionSound() {
	alias, _ := windows.UTF16PtrFromString("SystemAsterisk")
	const sndAlias = 0x00010000
	const sndAsync = 0x0001
	_, _, _ = procPlaySoundW.Call(uintptr(unsafe.Pointer(alias)), 0, sndAlias|sndAsync)
}

func setProcessDPIAware() {
	_, _, _ = procSetProcessDPIAware.Call()
}
