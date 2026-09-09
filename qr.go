package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---- 二维码弹窗 ----
// 用纯 Win32 窗口 + 内存位图实现,不开第二个 WebView2:
// 实测同进程再嵌一个 WebView2(共享用户数据目录)会让主窗口输入失效。

var qrWinHwnd atomic.Uint64 // 非 0 表示弹窗已打开

// qrShowQRCode 弹出二维码窗口(单实例;已打开则置前)
func qrShowQRCode(pngPath, url string) {
	if h := qrWinHwnd.Load(); h != 0 {
		r, _, _ := procIsWindowW.Call(uintptr(h))
		if r != 0 {
			setForeground(uintptr(h))
			return
		}
	}
	qrRunWindow(pngPath, url)
}

// ---- 窗口实现(每开一次各一套状态,随窗口销毁释放) ----

type qrWin struct {
	dibBits  uintptr // DIB 像素指针(DrawImage 需要)
	width    int     // 窗口客户区宽高
	height   int
	imgW     int
	imgH     int
	url      string
	hFont1   uintptr
	hFont2   uintptr
	memDC    uintptr
	memBmp   uintptr
	oldBmp   uintptr
	imgOK    bool
}

var (
	gdi32 = windows.NewLazySystemDLL("gdi32.dll")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procGetStockObject     = gdi32.NewProc("GetStockObject")
	procSetTextColor       = gdi32.NewProc("SetTextColor")
	procCreateFontW        = gdi32.NewProc("CreateFontW")

	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procPostQuitMessage  = user32.NewProc("PostQuitMessage")
	procBeginPaint       = user32.NewProc("BeginPaint")
	procEndPaint         = user32.NewProc("EndPaint")
	procFillRect         = user32.NewProc("FillRect")
	procDrawTextW        = user32.NewProc("DrawTextW")
	procGetClientRect    = user32.NewProc("GetClientRect")
	procAdjustWindowRect = user32.NewProc("AdjustWindowRect")
	procLoadImageW       = user32.NewProc("LoadImageW")
	procLoadCursorW      = user32.NewProc("LoadCursorW")
	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	procIsWindowW        = user32.NewProc("IsWindow")
	procStretchBlt       = gdi32.NewProc("StretchBlt")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
)

const (
	qrClassName   = "KimiWebQR"
	qrWinWidth    = 480
	qrWinHeight   = 600
	qrImgSize     = 360 // 二维码绘制边长
	qrImgMarginX  = (qrWinWidth - qrImgSize) / 2
	qrImgTop      = 24
	wmPaint       = 0x000F
	wmDestroy     = 0x0002
	wmEraseBkgnd  = 0x0014
	srcCopy       = 0x00CC0020
	whiteBrush    = 0
	dtCenter     = 0x1
	dtVCenter    = 0x4
	dtSingleLine = 0x20
	dtWordBreak  = 0x10
)

type wndClassExW struct {
	CbSize, Style            uint32
	LpfnWndProc              uintptr
	CbClsExtra, CbWndExtra   int32
	HInstance                uintptr
	HIcon, HCursor, HbrBack  uintptr
	LpszMenuName             *uint16
	LpszClassName            *uint16
	HIconSm                  uintptr
}

type winMsg struct {
	Hwnd   uintptr
	Msg    uint32
	_      uint32
	WParam uintptr
	LParam uintptr
	Time   uint32
	Pt     [2]int32
}

type paintStruct struct {
	HDC         uintptr
	FErase      int32
	RcPaint     [4]int32
	FRestore    int32
	FIncUpdate  int32
	RgbReserved [32]byte
}

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

var qrStateMap sync.Map // hwnd(uintptr) -> *qrWin

// qrRunWindow 创建并运行二维码窗口(阻塞,直到窗口关闭)
func qrRunWindow(pngPath, url string) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := &qrWin{width: qrWinWidth, height: qrWinHeight, url: url}
	if !w.loadPNG(pngPath) {
		messageBox("二维码图片加载失败,请直接使用链接:\n"+url, mbIconInformation)
		return
	}
	defer w.cleanup()

	hInstance, _, _ := procGetModuleHandleW.Call(0)
	icon, _, _ := procLoadImageW.Call(hInstance, 1, 1, 0, 0, 0x8040) // IMAGE_ICON, LR_DEFAULTSIZE|LR_SHARED
	cursor, _, _ := procLoadCursorW.Call(0, 32512)                   // IDC_ARROW
	className, _ := windows.UTF16PtrFromString(qrClassName)
	wc := wndClassExW{
		CbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		HInstance:     hInstance,
		HIcon:         icon,
		HIconSm:       icon,
		HCursor:       cursor,
		LpfnWndProc:   windows.NewCallback(qrWndProc),
		LpszClassName: className,
	}
	_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	const style = 0xC00000 | 0x80000 | 0x20000 // WS_CAPTION | WS_SYSMENU | WS_MINIMIZEBOX
	rc := [4]int32{0, 0, qrWinWidth, qrWinHeight}
	_, _, _ = procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&rc)), style, 0)
	sw, _, _ := procGetSystemMetrics.Call(0)
	sh, _, _ := procGetSystemMetrics.Call(1)
	ww, wh := rc[2]-rc[0], rc[3]-rc[1]
	x := (int32(sw) - ww) / 2
	y := (int32(sh) - wh) / 2

	title, _ := windows.UTF16PtrFromString("远程操控 · 扫码连接")
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(ww), uintptr(wh),
		0, 0, hInstance, 0,
	)
	if hwnd == 0 {
		return
	}
	qrStateMap.Store(hwnd, w)
	qrWinHwnd.Store(uint64(hwnd))
	defer func() {
		qrStateMap.Delete(hwnd)
		qrWinHwnd.Store(0)
	}()
	showWindow(hwnd, swRestore)

	var m winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 { // WM_QUIT
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// loadPNG 解码 PNG 到位图,并生成 top-down 32bpp DIB(StretchBlt 直接用)
func (w *qrWin) loadPNG(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return false
	}
	b := src.Bounds()
	w.imgW, w.imgH = b.Dx(), b.Dy()
	// 统一转成不透明 NRGBA(白底),避免透明像素扫不出来
	rgba := image.NewNRGBA(image.Rect(0, 0, w.imgW, w.imgH))
	draw.Draw(rgba, rgba.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Over)

	hdc, _, _ := procCreateCompatibleDC.Call(0)
	if hdc == 0 {
		return false
	}
	w.memDC = hdc
	bi := bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       int32(w.imgW),
		BiHeight:      -int32(w.imgH), // 负值 = top-down
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: 0, // BI_RGB
	}
	var bits uintptr
	bmp, _, _ := procCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == 0 {
		return false
	}
	w.memBmp = bmp
	w.dibBits = bits
	// NRGBA 是 RGBA 字节序,DIB 需要 BGRX;先转到 Go 缓冲再一次性拷入
	n := w.imgW * w.imgH
	buf := make([]byte, n*4)
	for i := 0; i < n; i++ {
		p := rgba.Pix[i*4:]
		buf[i*4+0] = p[2] // B
		buf[i*4+1] = p[1] // G
		buf[i*4+2] = p[0] // R
		buf[i*4+3] = 255
	}
	_, _, _ = procRtlMoveMemory.Call(bits, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	old, _, _ := procSelectObject.Call(hdc, bmp)
	w.oldBmp = old
	w.imgOK = true
	return true
}

func (w *qrWin) cleanup() {
	if w.memDC != 0 && w.oldBmp != 0 {
		_, _, _ = procSelectObject.Call(w.memDC, w.oldBmp)
	}
	if w.memBmp != 0 {
		_, _, _ = procDeleteObject.Call(w.memBmp)
	}
	if w.memDC != 0 {
		_, _, _ = procDeleteDC.Call(w.memDC)
	}
	if w.hFont1 != 0 {
		_, _, _ = procDeleteObject.Call(w.hFont1)
	}
	if w.hFont2 != 0 {
		_, _, _ = procDeleteObject.Call(w.hFont2)
	}
}

func qrWndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case wmEraseBkgnd:
		return 1 // 统一在 WM_PAINT 里填白,避免闪烁
	case wmPaint:
		if v, ok := qrStateMap.Load(hwnd); ok {
			v.(*qrWin).paint(hwnd)
			return 0
		}
	case wmClose:
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

func (w *qrWin) paint(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	var rc [4]int32
	_, _, _ = procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	white, _, _ := procGetStockObject.Call(whiteBrush)
	_, _, _ = procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), white)

	if w.imgOK {
		_, _, _ = procStretchBlt.Call(
			hdc,
			uintptr(qrImgMarginX), uintptr(qrImgTop), uintptr(qrImgSize), uintptr(qrImgSize),
			w.memDC,
			0, 0, uintptr(w.imgW), uintptr(w.imgH),
			srcCopy,
		)
	}

	w.drawText(hdc, rc[2]-rc[0])
	_, _, _ = procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
}

// drawText 画说明文字与链接文本
func (w *qrWin) drawText(hdc uintptr, cw int32) {
	segoe, _ := windows.UTF16PtrFromString("Segoe UI")
	negPx := func(n int) uintptr { return uintptr(uint32(int32(-n))) }
	if w.hFont1 == 0 {
		f, _, _ := procCreateFontW.Call(
			negPx(20), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0,
			uintptr(unsafe.Pointer(segoe)),
		)
		w.hFont1 = f
		f2, _, _ := procCreateFontW.Call(
			negPx(13), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0,
			uintptr(unsafe.Pointer(segoe)),
		)
		w.hFont2 = f2
	}

	caption, _ := windows.UTF16PtrFromString("手机扫码,登录同一 Kimi 账号即可控制本机")
	link, _ := windows.UTF16PtrFromString(w.url)

	old, _, _ := procSelectObject.Call(hdc, w.hFont1)
	_, _, _ = procSetTextColor.Call(hdc, 0x00444444) // COLORREF = 0x00BBGGRR
	r1 := [4]int32{0, qrImgTop + qrImgSize + 14, cw, qrImgTop + qrImgSize + 42}
	_, _, _ = procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(caption)), ^uintptr(0),
		uintptr(unsafe.Pointer(&r1)), dtCenter|dtSingleLine|dtVCenter)

	_, _, _ = procSelectObject.Call(hdc, w.hFont2)
	_, _, _ = procSetTextColor.Call(hdc, 0x00999999)
	r2 := [4]int32{24, qrImgTop + qrImgSize + 50, cw - 24, qrImgTop + qrImgSize + 110}
	_, _, _ = procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(link)), ^uintptr(0),
		uintptr(unsafe.Pointer(&r2)), dtCenter|dtWordBreak)

	_, _, _ = procSelectObject.Call(hdc, old)
}
