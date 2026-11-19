package ui

// 驗收測試(內部白箱)——2026-11-19 需求 D3/D3a/D4(2026-11-19 修訂:改用
// cut-off 講法,不用「今日/today」)。規格:docs/blueprints/2026-11-19-budget-day-inclusive-fix.md
// (語意模型「上一個 cut-off → 下一個 cut-off」切片;D3/D3a/A7/A8)。
//
// 頁面是靜態常數(既有 page_markers_test.go 慣例:子串級標記;動態渲染行為——
// toFixed 位數、星期短寫、HH:MM 去秒——交給 JS 引擎,既有套件同樣信賴)。
// 因此 D3 釘「機制在場」:雙語 i18n 鍵(共用鍵名 leftLabel 屬本測試釘死的命名)、
// 文件公式原式、缺資料防護、cut-off 前綴詞與新天花板標籤;D3a 以行為級釘死
// (StatusView 序列化:until 欄位在場/省略);A8 釘 label 固定寬 190px。

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A7/D3:B 卡天花板 chip = cut-off 前綴 + 天花板 + 「尚可」段(pt 一位小數,下限 0)。
func TestBudgetDayChipMarkers(t *testing.T) {
	html := indexHTML
	for _, m := range []string{
		// D3a 前端面:payload 的 cut-off 欄位在場,「Until …」前綴據此渲染;
		// A7「DayBoundary 缺 → 無前綴」= 前綴分支釘成守衛表達式(與同檔另兩個
		// 防護標記同標準,防「無條件拼接出 Until null」)。
		`s.until!=null`,
		// 文件公式原式(下限 0 夾限 + 一位小數;子串級釘死防「寫反」與位數走樣
		// ——既有套件的突變防護慣例):
		`Math.max(0,s.ceilingToday-s.b.weeklyPct).toFixed(1)`,
		// 缺資料防護:ceilingToday 或週% 缺任一 → 不加「尚可」段(維持現狀)。
		`s.b.weeklyPct!=null`,
	} {
		if !strings.Contains(html, m) {
			t.Fatalf("indexHTML 缺 A7/D3 標記 %q", m)
		}
	}
	// chip 整行在 ceilingToday==null 時不建(既有規則不變)。
	if !strings.Contains(html, `slot==="B"&&s.ceilingToday!=null`) {
		t.Fatalf("ceilingToday 缺時整行不建的既有防護不得消失")
	}
	// 雙語 i18n:共用鍵名 leftLabel(本測試釘死的命名決策)兩語言齊備;
	// 詞序差異(EN 數字在前「8.5pt left」、zh 字首在前「尚可 8.5pt」)由各語言鍵值承擔。
	if n := strings.Count(html, "leftLabel:"); n < 2 {
		t.Fatalf("leftLabel 鍵必須雙語齊備(zh+en),實際 %d 次", n)
	}
	if !strings.Contains(html, "尚可") {
		t.Fatalf("indexHTML 缺 zh 詞條「尚可」")
	}
	if !strings.Contains(html, "pt left") {
		t.Fatalf("indexHTML 缺 en 詞條 \"pt left\"")
	}
	// cut-off 前綴:EN "Until " 與 zh「到…前:」包裝字(星期短寫與 HH:MM 由 JS
	// 於執行期自 payload 時刻渲染,靜態原始碼不含)。
	if !strings.Contains(html, "Until ") {
		t.Fatalf("indexHTML 缺 en 前綴詞 \"Until \"")
	}
	if !strings.Contains(html, "前:") {
		t.Fatalf("indexHTML 缺 zh 前綴包裝「前:」")
	}
	// 新天花板標籤(文件格式:zh「天花板 週79.5%」、en "ceiling wk 79.5%"):
	if !strings.Contains(html, `ceilingChip:"天花板 週"`) {
		t.Fatalf("indexHTML 缺 zh 天花板標籤 ceilingChip:\"天花板 週\"")
	}
	if !strings.Contains(html, `ceilingChip:"ceiling wk "`) {
		t.Fatalf("indexHTML 缺 en 天花板標籤 ceilingChip:\"ceiling wk \"")
	}
	// D3 明文禁用「今日/today」:舊 chip 標籤字眼必須退場(含 chip 建構處的註解)。
	if strings.Contains(html, "今日天花板") {
		t.Fatalf("D3:舊「今日天花板」字眼下架(改用 cut-off 講法)")
	}
	if strings.Contains(html, "Today's ceiling") {
		t.Fatalf("D3:舊 \"Today's ceiling\" 字眼下架(改用 cut-off 講法)")
	}
}

// A7/D3a:/api/status 的 B 卡 payload 帶下一個結算瞬間(來源 BudgetState.DayBoundary);
// nil → until 欄位整個省略(行為級釘死:直接序列化 StatusView,優於原始碼子串)。
func TestBudgetDayStatusPayloadUntil(t *testing.T) {
	at := time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local)
	with, err := json.Marshal(StatusView{Until: &at})
	if err != nil || !strings.Contains(string(with), `"until":"`) {
		t.Fatalf("D3a:payload 必須帶 until 欄位(string=%s err=%v)", string(with), err)
	}
	without, err := json.Marshal(StatusView{})
	if err != nil || strings.Contains(string(without), `"until"`) {
		t.Fatalf("D3a:cut-off 未知(nil)時 until 欄位必須整個省略(string=%s err=%v)", string(without), err)
	}
}

// A8:label 固定寬 190px(最長英文標籤 "No auto-flee on weekends" 約 175px 放得下;
// 所有輸入框/開關同一條 x 起點),舊 min-width:170px 退場。
func TestBudgetDayLabelWidth(t *testing.T) {
	html := indexHTML
	if !strings.Contains(html, `label{display:inline-block;width:190px;`) {
		t.Fatalf("A8:label 必須固定寬 190px(width:190px)")
	}
	if strings.Contains(html, `min-width:170px`) {
		t.Fatalf("A8:舊 min-width:170px 必須退場")
	}
}
