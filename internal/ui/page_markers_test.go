package ui

// 白箱測試:內嵌設定頁必須帶齊 2026-11-16 計畫的新行為標記。
// 規格:docs/blueprints/2026-11-16-cc-fuse-budget-ui.md §2.2/§2.3/§2.6。
// 動機(複審 A4):POST /api/config 反序列化進零值 Config(不疊預設),
// collect() 漏帶 enforceDailyCeiling 會讓每次存檔都靜默關掉強制切 —
// 所以 load() 與 collect() 都必須出現該欄位,逐字在頁面原始碼裡斷言。

import (
	"strings"
	"testing"
)

func TestPageBudgetMarkers(t *testing.T) {
	html := indexHTML
	for _, m := range []string{
		"bEnforce",       // 勾選框 id(B 配速預算區)
		"天花板 週",          // B 卡天花板行(2026-11-19 D3:cut-off 講法,「今日天花板」退場)
		"到點強制切回 A",       // 開啟時的標註
		"不強制",            // 關閉時的標註
		"恢復預計",           // A 卡恢復時間行(只看 5h)
		"api/autopilot",  // 自動切換按鈕的呼叫
		"fetchStatus();", // 存檔成功後立即重抓
		// 2026-11-17 方向性避難閘:六日不自動切 B 的勾選框,load/collect 同理防漏。
		"noFleeWknd",         // 勾選框 id(閾值區)
		"自動切 B:已暫停(切回 A 照常)", // 暫停語意=方向性的提示文字(2026-11-17 決定 3)
		// 語意釘死(Phase-3 審查突變5:子串存在抓不了「寫反」與「可刪」)——
		// collect 的極性(不得寫成 !checked)與存檔成功分支的立即重抓,
		// 頁面是靜態常數,子串級斷言可承受。
		`enforceDailyCeiling:document.getElementById("bEnforce").checked`,
		`noFleeOnWeekend:document.getElementById("noFleeWknd").checked`,
		`if(st===200)fetchStatus();`,
		// 2026-11-19 介面語言持久化:按鈕切換 POST /api/uilang 落盤,
		// 載入時以 /api/config 的 uilang 糾正顯示語言(臨時埠讓 localStorage 失憶)。
		"api/uilang",  // the toggle's flush-to-config call
		"c.uilang",    // load-time correction from the server value
		"uilang:lang", // collect must carry uilang (the main save must not wipe it; zero-value deserialization lesson)
		"applyServerLang(c.uilang)", // full-expression pin: the correction polarity (a dropped guard reproduces the original bug)
	} {
		if !strings.Contains(html, m) {
			t.Fatalf("indexHTML 缺標記 %q", m)
		}
	}
	for _, k := range []string{"enforceDailyCeiling", "noFleeOnWeekend"} {
		if n := strings.Count(html, k); n < 2 {
			t.Fatalf("%s 必須同時出現在 load 與 collect,實際 %d 次", k, n)
		}
	}
}
