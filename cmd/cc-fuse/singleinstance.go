package main

// 單例鎖(設計 §11.6):Windows 具名 mutex 擋下第二份 cc-fuse,
// 防止兩份互改 settings.json。第二實例通知一聲即退出。

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// mutexName is the global-named single-instance mutex (card: Global\cc-fuse-single).
const mutexName = `Global\cc-fuse-single`

// singleInstance holds the process-wide mutex handle so it stays alive for
// the process lifetime (Windows releases it on exit regardless).
type singleInstance struct {
	handle windows.Handle
}

// acquireSingleInstance creates the named mutex; ok=false means another
// cc-fuse already holds it and this process must exit. Any other creation
// failure is reported as an error rather than silently allowing doubles.
func acquireSingleInstance() (*singleInstance, bool, error) {
	h, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(mutexName))
	if err == windows.ERROR_ALREADY_EXISTS {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("create singleton mutex %s: %w", mutexName, err)
	}
	return &singleInstance{handle: h}, true, nil
}

// release drops the mutex handle (defensive; the OS does this on exit too).
func (s *singleInstance) release() {
	if s != nil && s.handle != 0 {
		_ = windows.CloseHandle(s.handle)
		s.handle = 0
	}
}
