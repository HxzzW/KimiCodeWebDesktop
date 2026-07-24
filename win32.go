package main

import (
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

// ---- 关窗拦截(关窗改为隐藏到托盘) ----

const gwlpWndProc = -4

var oldWndProc uintptr

// subclassForCloseHide 子类化窗口过程:拦截 WM_CLOSE,平时隐藏,
// quitting 置位后才走原流程真正关闭
func subclassForCloseHide(hwnd uintptr, quitting *atomic.Int32) {
	cb := syscall.NewCallback(func(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
		if msg == wmClose && quitting.Load() == 0 {
			showWindow(hwnd, swHide)
			return 0
		}
		r, _, _ := procCallWindowProcW.Call(oldWndProc, hwnd, uintptr(msg), wp, lp)
		return r
	})
	nIndex := int32(gwlpWndProc) // 变量才能转 uintptr(常量负值不允许)
	r, _, _ := procSetWindowLongPtrW.Call(hwnd, uintptr(nIndex), cb)
	oldWndProc = r
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
