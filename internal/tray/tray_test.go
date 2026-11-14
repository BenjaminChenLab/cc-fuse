package tray

// 單元測試(白箱):圖示狀態選擇的嚴重度序、tooltip/狀態行文字。
// 對應卡片手動項 5(匣圖示顏色隨切換變化)——純函式先把判定釘死。

import (
	"strings"
	"testing"
	"time"

	"cc-fuse/internal/app"
	"cc-fuse/internal/domain"
)

func f(v float64) *float64 { return &v }

func sample(pct5 float64) *domain.QuotaSnapshot {
	return &domain.QuotaSnapshot{ProfileID: "x", FiveHourPct: f(pct5), QueriedAt: time.Now()}
}

// TestPickIconSeverity:紅 > 灰 > 帳號色,未知 Current 保守灰。
func TestPickIconSeverity(t *testing.T) {
	cases := []struct {
		name string
		st   app.Status
		want IconState
	}{
		{"on A", app.Status{Current: "A", Autopilot: true}, IconGreenA},
		{"on B", app.Status{Current: "B", Autopilot: true}, IconOrangeB},
		{"paused", app.Status{Current: "A", Autopilot: false}, IconGray},
		{"stale", app.Status{Current: "A", Autopilot: true, Stale: true}, IconGray},
		{"key dead wins over paused", app.Status{Current: "A", Autopilot: false, KeyDead: true}, IconRed},
		{"parse failing red", app.Status{Current: "B", Autopilot: true, ParseFailing: true}, IconRed},
		{"no current", app.Status{Autopilot: true}, IconGray},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PickIcon(tc.st); got != tc.want {
				t.Fatalf("PickIcon = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPickTooltip:雙邊百分比、缺樣本/缺 5h 桶顯示 ?。
func TestPickTooltip(t *testing.T) {
	st := app.Status{A: sample(42.4), B: sample(12)}
	if got, want := PickTooltip(st), "A: 42%(5h) · B: 12%(5h)"; got != want {
		t.Fatalf("tooltip = %q, want %q", got, want)
	}
	if got := PickTooltip(app.Status{}); !strings.Contains(got, "?") {
		t.Fatalf("no-sample tooltip must show ?: %q", got)
	}
}

// TestStatusText:狀態行帶在用帳號與運作狀態。
func TestStatusText(t *testing.T) {
	st := app.Status{Current: "B", Autopilot: true, A: sample(10), B: sample(90)}
	got := StatusText(st)
	for _, want := range []string{"B", "10%", "90%", "自動切換中"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status line %q missing %q", got, want)
		}
	}
	if got := StatusText(app.Status{Autopilot: false}); !strings.Contains(got, "已暫停自動") {
		t.Fatalf("paused status line wrong: %q", got)
	}
}

// TestIconBytes:四種狀態都能產出合法 ICO(目錄頭+PNG 資料),同狀態位元組一致。
func TestIconBytes(t *testing.T) {
	for s := IconGreenA; s <= IconRed; s++ {
		b, err := IconBytes(s)
		if err != nil {
			t.Fatalf("IconBytes(%d): %v", s, err)
		}
		if len(b) <= icoDirSize || b[0] != 0 || b[2] != 1 || b[4] != 1 {
			t.Fatalf("IconBytes(%d) is not a 1-image ICO: len=%d head=%v", s, len(b), b[:6])
		}
		b2, _ := IconBytes(s)
		if string(b) != string(b2) {
			t.Fatalf("IconBytes(%d) not deterministic", s)
		}
	}
}
