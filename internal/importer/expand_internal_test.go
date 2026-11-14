package importer

// 白箱單元測試:~ 展開(Windows 原生不展開,設定頁預設值靠它)。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir in this environment: %v", err)
	}
	cases := []struct {
		in, want string
	}{
		{"~/.cc-switch/cc-switch.db", filepath.Join(home, ".cc-switch", "cc-switch.db")},
		{`~\.cc-switch\cc-switch.db`, filepath.Join(home, ".cc-switch", "cc-switch.db")},
		{"~", home},
		{"  ~/x.db  ", filepath.Join(home, "x.db")}, // 前後空白剪掉
		{"C:/data/db.sqlite", "C:/data/db.sqlite"},  // 絕對路徑原樣
		{"relative/db.sqlite", "relative/db.sqlite"},
		{"", ""},
		{"just~a~name.db", "just~a~name.db"}, // 中間的 ~ 不是家目錄記號
	}
	for _, tc := range cases {
		if got := expandTilde(tc.in); got != tc.want {
			t.Fatalf("expandTilde(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
