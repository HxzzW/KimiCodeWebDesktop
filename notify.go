package main

import (
	"github.com/go-toast/toast"
)

// toastNotify 发送 Windows 通知;失败静默(通知不是关键路径)
func toastNotify(message string) {
	n := toast.Notification{
		AppID:   appTitle,
		Title:   appTitle,
		Message: message,
	}
	_ = n.Push()
}
