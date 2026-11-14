package main

// 開機自啟(設計 §8):HKCU\Software\Microsoft\Windows\CurrentVersion\Run
// 寫/移 "cc-fuse" 值,由 config.AutoStart 驅動(存檔時同步套用)。

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath is the per-user autostart registry key.
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// valueName is this app's value under the Run key.
const valueName = "cc-fuse"

// applyAutoStart writes or removes the HKCU Run value so Windows launches
// cc-fuse at logon. exePath empty = use the running executable's path.
// Called on every settings save (design §8: 由 autoStart 設定驅動)。
func applyAutoStart(enabled bool, exePath string) error {
	if exePath == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable for autostart: %w", err)
		}
		exePath = exe
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer key.Close()

	if !enabled {
		// 關閉自啟:值不存在也算成功(冪等)。
		if err := key.DeleteValue(valueName); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("remove Run value: %w", err)
		}
		return nil
	}
	if err := key.SetStringValue(valueName, `"`+exePath+`"`); err != nil {
		return fmt.Errorf("write Run value: %w", err)
	}
	return nil
}
