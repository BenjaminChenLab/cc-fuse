package main

// win32 薄包裝(R6 主視窗單例):把已存在的設定窗帶到前景。
// x/sys/windows 沒有 ShowWindow/SetForegroundWindow(只有 IsWindow 與
// SW_RESTORE 常數),這兩個直接 lazy-load user32 —— 與 webview2 模組同一套
// 做法,零 cgo。SetForegroundWindow 有前景鎖(前景程序才准搶),但使用者
// 剛點完系統匣圖示 = 本程序收得到輸入,通常過得了鎖;偶爾被拒只是沒搶到
// 焦點,SW_RESTORE 仍會把最小化的視窗還原,屬外觀層瑕疵。

import (
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	procShowWindow    = user32.NewProc("ShowWindow")
	procSetForeground = user32.NewProc("SetForegroundWindow")
)

// swRestore is user32's SW_RESTORE: un-minimize the window if needed.
const swRestore = 9

// hwndAlive reports whether the handle is still a live window.
func hwndAlive(hwnd uintptr) bool {
	return windows.IsWindow(windows.HWND(hwnd))
}

// raiseWindow un-minimizes (if minimized) and foregrounds hwnd. Returns
// false when the handle is no longer a live window (IsWindow check first —
// the OS recycles HWNDs, and a stale raise could foreground an unrelated
// window; re-audit K4).
func raiseWindow(hwnd uintptr) bool {
	if !windows.IsWindow(windows.HWND(hwnd)) {
		return false
	}
	procShowWindow.Call(hwnd, swRestore)
	_, _, _ = procSetForeground.Call(hwnd)
	return true
}
