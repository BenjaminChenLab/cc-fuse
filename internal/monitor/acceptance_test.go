package monitor_test

// 驗收測試(黑箱)——cc-fuse T01:額度查詢與解析。
// 規格來源:docs/blueprints/tasks/T01-core-services.md「z.ai 回應解析規格」9 條 +
// 設計文件 §4 + 編碼審計補強(2026-11-15):CREDIT_LIMIT、403、unit=6 number=1、
// 無 reset 兜底優先、業務錯關鍵詞全家族、標頭精確斷言、錯誤路徑欄位完整。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cc-fuse/internal/domain"
	"cc-fuse/internal/monitor"
)

const zhipuQuotaPath = "/api/monitor/usage/quota/limit"

func limitsJSON(s string) domain.QuotaSnapshot {
	return monitor.Parse([]byte(s))
}

func pct(v *float64) float64 {
	if v == nil {
		return -999 // nil 與 0 必須可區分
	}
	return *v
}

// 規則 2+4+9(陷阱 #3036):unit=6 必須壓過重置時間排序——週期末端週桶比 5h 桶更早重置。
func TestParseUnitFieldOverridesResetOrder(t *testing.T) {
	body := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","unit":6,"number":7,"percentage":42.0,"nextResetTime":1000003600000},
		{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":1.0,"nextResetTime":1000018000000}
	],"level":"glm-5.3-plan"}}`
	s := limitsJSON(body)
	if s.Err != nil {
		t.Fatalf("unexpected err: %v", s.Err)
	}
	if pct(s.FiveHourPct) != 1.0 || pct(s.WeeklyPct) != 42.0 {
		t.Fatalf("unit=6 must win over reset order: got 5h=%v weekly=%v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}
	if s.PlanLevel != "glm-5.3-plan" {
		t.Fatalf("plan level = %q, want glm-5.3-plan", s.PlanLevel)
	}
	if s.WeeklyReset == nil || s.FiveHourReset == nil {
		t.Fatalf("resets must be parsed: %v %v", s.FiveHourReset, s.WeeklyReset)
	}
}

// 規則 2+9(審計 M5):unit=6,number=1 也是週桶(number 不參與分類)。
func TestParseWeeklyUnitSixNumberOne(t *testing.T) {
	body := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":30.0,"nextResetTime":1000000000000},
		{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":10.0,"nextResetTime":2000000000000}
	]}}`
	s := limitsJSON(body)
	if pct(s.FiveHourPct) != 10.0 || pct(s.WeeklyPct) != 30.0 {
		t.Fatalf("unit=6 number=1 must be weekly: 5h=%v weekly=%v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}
}

// 規則 1(審計 M3):CREDIT_LIMIT 與 TOKENS_LIMIT 同等對待(大小寫不敏感)。
func TestParseCreditLimitType(t *testing.T) {
	body := `{"success":true,"data":{"limits":[
		{"type":"CREDIT_LIMIT","percentage":11.0,"nextResetTime":1000000000000},
		{"type":"credit_limit","percentage":22.0,"nextResetTime":2000000000000}
	]}}`
	s := limitsJSON(body)
	if pct(s.FiveHourPct) != 11.0 || pct(s.WeeklyPct) != 22.0 {
		t.Fatalf("CREDIT_LIMIT must parse like TOKENS_LIMIT: 5h=%v weekly=%v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}
}

// 規則 5(真實形態):5h 桶 0% 時可能沒有 nextResetTime;週桶帶 reset。
func TestParseMissingFiveHourReset(t *testing.T) {
	body := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","unit":6,"percentage":25.0,"nextResetTime":2000000000000},
		{"type":"TOKENS_LIMIT","unit":3,"percentage":0.0}
	]}}`
	s := limitsJSON(body)
	if pct(s.FiveHourPct) != 0.0 || s.FiveHourReset != nil {
		t.Fatalf("5h bucket at 0%% may lack reset: pct=%v reset=%v", pct(s.FiveHourPct), s.FiveHourReset)
	}
	if pct(s.WeeklyPct) != 25.0 || s.WeeklyReset == nil {
		t.Fatalf("weekly bucket wrong: pct=%v reset=%v", pct(s.WeeklyPct), s.WeeklyReset)
	}
}

// 規則 1:type 只認 TOKENS_LIMIT/CREDIT_LIMIT(大小寫不敏感);TIME_LIMIT 忽略。
func TestParseTypeCaseInsensitiveAndIgnoredTypes(t *testing.T) {
	body := `{"success":true,"data":{"limits":[
		{"type":"tokens_limit","percentage":12.0,"nextResetTime":1000000000000},
		{"type":"Tokens_Limit","percentage":34.0,"nextResetTime":2000000000000},
		{"type":"TIME_LIMIT","percentage":7.0}
	]}}`
	s := limitsJSON(body)
	if pct(s.FiveHourPct) != 12.0 || pct(s.WeeklyPct) != 34.0 {
		t.Fatalf("case-insensitive type match failed: 5h=%v weekly=%v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}
}

// 規則 4:percentage 為字串/null → 0;負數/超過 100 不裁剪照搬。
func TestParsePercentageEdgeValues(t *testing.T) {
	body := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","percentage":"invalid","nextResetTime":1000000000000},
		{"type":"TOKENS_LIMIT","percentage":null,"nextResetTime":2000000000000}
	]}}`
	s := limitsJSON(body)
	if pct(s.FiveHourPct) != 0.0 || pct(s.WeeklyPct) != 0.0 {
		t.Fatalf("invalid/null percentage must read 0: %v %v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}

	body2 := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","percentage":-5.0,"nextResetTime":1000000000000},
		{"type":"TOKENS_LIMIT","percentage":150.0,"nextResetTime":2000000000000}
	]}}`
	s2 := limitsJSON(body2)
	if pct(s2.FiveHourPct) != -5.0 || pct(s2.WeeklyPct) != 150.0 {
		t.Fatalf("extreme percentages must pass through unclamped: %v %v", pct(s2.FiveHourPct), pct(s2.WeeklyPct))
	}
}

// 規則 3(審計 M6):unit 缺失時,無 nextResetTime 者優先歸 5h 桶——不論 reset 排序。
func TestParseNoResetEntryPrioritizedAsFiveHour(t *testing.T) {
	body := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","percentage":53.0,"nextResetTime":2000000000000},
		{"type":"TOKENS_LIMIT","percentage":44.0}
	]}}`
	s := limitsJSON(body)
	if pct(s.FiveHourPct) != 44.0 || s.FiveHourReset != nil {
		t.Fatalf("no-reset entry must take the five_hour slot first: 5h=%v reset=%v", pct(s.FiveHourPct), s.FiveHourReset)
	}
	if pct(s.WeeklyPct) != 53.0 {
		t.Fatalf("remaining entry fills weekly: %v", pct(s.WeeklyPct))
	}
}

// 規則 6(防禦):三條 TOKENS_LIMIT 取前二;兩條同 unit=3 → 第一條歸 5h、第二條填週。
func TestParseDefensiveShapes(t *testing.T) {
	three := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","percentage":1.0,"nextResetTime":1000000000000},
		{"type":"TOKENS_LIMIT","percentage":2.0,"nextResetTime":2000000000000},
		{"type":"TOKENS_LIMIT","percentage":3.0,"nextResetTime":3000000000000}
	]}}`
	s := limitsJSON(three)
	if pct(s.FiveHourPct) != 1.0 || pct(s.WeeklyPct) != 2.0 {
		t.Fatalf("keep first two: %v %v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}

	dup := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":10.0,"nextResetTime":1000000000000},
		{"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":20.0,"nextResetTime":2000000000000}
	]}}`
	s2 := limitsJSON(dup)
	if pct(s2.FiveHourPct) != 10.0 || pct(s2.WeeklyPct) != 20.0 {
		t.Fatalf("duplicate unit=3 must fill both slots: %v %v", pct(s2.FiveHourPct), pct(s2.WeeklyPct))
	}
}

// 規則 3(兜底):unit 缺失 → 按 reset 升序填槽(兩條都有 reset 的形態)。
func TestParseUnitFallbackResetOrder(t *testing.T) {
	noUnit := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","percentage":53.0,"nextResetTime":2000000000000},
		{"type":"TOKENS_LIMIT","percentage":44.0,"nextResetTime":1000000000000}
	]}}`
	s := limitsJSON(noUnit)
	if pct(s.FiveHourPct) != 44.0 || pct(s.WeeklyPct) != 53.0 {
		t.Fatalf("missing unit: reset-order fallback: %v %v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}

	unknownUnit := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","unit":9,"percentage":44.0,"nextResetTime":1000000000000},
		{"type":"TOKENS_LIMIT","unit":9,"percentage":53.0,"nextResetTime":2000000000000}
	]}}`
	s2 := limitsJSON(unknownUnit)
	if pct(s2.FiveHourPct) != 44.0 || pct(s2.WeeklyPct) != 53.0 {
		t.Fatalf("unknown unit=9 must fall back to reset order: %v %v", pct(s2.FiveHourPct), pct(s2.WeeklyPct))
	}
}

// 規則 6+3(舊套餐):只有一條 TOKENS_LIMIT → 週桶為 nil(不可虛造 0)。
func TestParseOldPlanSingleTier(t *testing.T) {
	body := `{"success":true,"data":{"limits":[
		{"type":"TOKENS_LIMIT","percentage":2.0,"nextResetTime":1774967594803}
	]}}`
	s := limitsJSON(body)
	if pct(s.FiveHourPct) != 2.0 {
		t.Fatalf("single tier goes to five_hour: %v", pct(s.FiveHourPct))
	}
	if s.WeeklyPct != nil {
		t.Fatalf("old plan has no weekly bucket, must be nil: %v", pct(s.WeeklyPct))
	}
}

// 規則 1(反面):全無 TOKENS_LIMIT → 兩桶皆 nil,不得 panic。
func TestParseNoTokenLimits(t *testing.T) {
	s := limitsJSON(`{"success":true,"data":{"limits":[{"type":"TIME_LIMIT","percentage":5.0}]}}`)
	if s.FiveHourPct != nil || s.WeeklyPct != nil {
		t.Fatalf("no token limits → both nil: %v %v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}
}

// 規則 7(審計 M7):按量帳號業務錯的關鍵詞家族全表——中英八種說法都識別為 ErrNoPlan。
func TestParseNoPlanKeywordFamily(t *testing.T) {
	msgs := []string{
		"当前用户不存在coding plan",               // 不存在
		"当前用户没有coding plan",                // 沒有
		"当前用户未開通coding plan",               // 未開通
		"user does not have a coding plan", // not have
		"coding plan not exist",            // not exist
		"coding plan not found",            // not found
		"no coding plan for this account",  // no coding plan
		"coding plan not subscribed",       // not subscribed
	}
	for _, msg := range msgs {
		s := limitsJSON(fmt.Sprintf(`{"code":500,"msg":%q,"success":false}`, msg))
		if !errors.Is(s.Err, monitor.ErrNoPlan) {
			t.Fatalf("msg %q must classify as ErrNoPlan, got: %v", msg, s.Err)
		}
	}

	// 反例:含 "coding plan" 字樣但語意不是「沒有」→ 一般業務錯。
	neg := limitsJSON(`{"code":500,"msg":"coding plan quota query failed","success":false}`)
	if neg.Err == nil || errors.Is(neg.Err, monitor.ErrNoPlan) {
		t.Fatalf("non-missing coding-plan error must not be ErrNoPlan: %v", neg.Err)
	}
}

// 規則 7 補充:成功形態但缺 data → 明確錯誤,非 panic。
func TestParseMissingData(t *testing.T) {
	s := limitsJSON(`{"success":true}`)
	if s.Err == nil {
		t.Fatal("missing data field must error")
	}
}

// --- Query:HTTP 路徑 + 請求形狀(httptest 假 server) ---

func testProfile() domain.Profile {
	return domain.Profile{
		ID: "A", Name: "team", APIKey: "test-key-123",
		BaseURL: "https://api.z.ai/api/anthropic",
	}
}

// 契約:GET、host 根路徑(base 帶 /api/anthropic 須剝除)、Authorization 直放 key、
// Content-Type=application/json、Accept-Language=en-US,en(卡片標頭規格,審計 M11)。
func TestQueryRequestShape(t *testing.T) {
	var gotPath, gotAuth, gotMethod, gotAccept, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept-Language")
		gotContentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"success":true,"data":{"limits":[
			{"type":"TOKENS_LIMIT","unit":3,"percentage":1.0,"nextResetTime":1000000000000},
			{"type":"TOKENS_LIMIT","unit":6,"percentage":2.0,"nextResetTime":2000000000000}
		]}}`))
	}))
	defer srv.Close()

	c := monitor.New(srv.URL, 5*time.Second)
	p := testProfile()
	s := c.Query(context.Background(), p)

	if s.Err != nil {
		t.Fatalf("query failed: %v", s.Err)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("method = %s, want GET", gotMethod)
	}
	if gotPath != zhipuQuotaPath {
		t.Fatalf("path = %s, want %s", gotPath, zhipuQuotaPath)
	}
	if gotAuth != p.APIKey {
		t.Fatalf("Authorization = %q, want bare key %q (no Bearer prefix)", gotAuth, p.APIKey)
	}
	if gotAccept != "en-US,en" {
		t.Fatalf("Accept-Language = %q, want %q", gotAccept, "en-US,en")
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	if s.ProfileID != "A" {
		t.Fatalf("snapshot must carry profile id: %q", s.ProfileID)
	}
	if pct(s.FiveHourPct) != 1.0 || pct(s.WeeklyPct) != 2.0 {
		t.Fatalf("snapshot percentages wrong: %v %v", pct(s.FiveHourPct), pct(s.WeeklyPct))
	}
	if s.QueriedAt.IsZero() {
		t.Fatal("QueriedAt must be stamped")
	}
	if s.HTTPStatus != http.StatusOK {
		t.Fatalf("HTTPStatus = %d, want 200", s.HTTPStatus)
	}
}

// BaseURL 帶 /api/anthropic 尾巴時,查詢仍打回 host 根的監控路徑(活體形態;決策 2026-11-15)。
func TestQueryBaseURLWithAnthropicSuffix(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"success":true,"data":{"limits":[]}}`))
	}))
	defer srv.Close()

	c := monitor.New(strings.TrimSuffix(srv.URL, "/")+"/api/anthropic", 5*time.Second)
	s := c.Query(context.Background(), testProfile())
	if s.Err != nil {
		t.Fatalf("query failed: %v", s.Err)
	}
	if gotPath != zhipuQuotaPath {
		t.Fatalf("path = %s, want %s (base suffix must not leak into quota path)", gotPath, zhipuQuotaPath)
	}
}

// 401 → 金鑰失效:Err 屬 ErrAuthInvalid,HTTPStatus=401;錯誤路徑快照仍帶 ProfileID/QueriedAt(審計 M12)。
func TestQueryAuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	c := monitor.New(srv.URL, 5*time.Second)
	s := c.Query(context.Background(), testProfile())
	if !errors.Is(s.Err, monitor.ErrAuthInvalid) {
		t.Fatalf("401 must map to ErrAuthInvalid: %v", s.Err)
	}
	if s.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("HTTPStatus = %d, want 401", s.HTTPStatus)
	}
	if s.ProfileID != "A" || s.QueriedAt.IsZero() {
		t.Fatalf("error snapshot must still carry ProfileID/QueriedAt: %+v", s)
	}
}

// 403(審計 M4)→ 同屬金鑰失效:ErrAuthInvalid、HTTPStatus=403。
func TestQueryForbiddenIsAuthInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer srv.Close()

	c := monitor.New(srv.URL, 5*time.Second)
	s := c.Query(context.Background(), testProfile())
	if !errors.Is(s.Err, monitor.ErrAuthInvalid) {
		t.Fatalf("403 must map to ErrAuthInvalid: %v", s.Err)
	}
	if s.HTTPStatus != http.StatusForbidden {
		t.Fatalf("HTTPStatus = %d, want 403", s.HTTPStatus)
	}
}

// 逾時 → 瞬時失敗:Err 非空、非鑰失效類、非業務錯。
func TestQueryTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := monitor.New(srv.URL, 200*time.Millisecond)
	s := c.Query(context.Background(), testProfile())
	if s.Err == nil {
		t.Fatal("timeout must error")
	}
	if errors.Is(s.Err, monitor.ErrAuthInvalid) || errors.Is(s.Err, monitor.ErrNoPlan) {
		t.Fatalf("timeout must not be classified as auth/no-plan: %v", s.Err)
	}
}

// 200+業務錯(按量帳號)→ ErrNoPlan、HTTPStatus=200。
func TestQueryNoPlanBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":500,"msg":"当前用户不存在coding plan","success":false}`))
	}))
	defer srv.Close()

	c := monitor.New(srv.URL, 5*time.Second)
	s := c.Query(context.Background(), testProfile())
	if !errors.Is(s.Err, monitor.ErrNoPlan) {
		t.Fatalf("no-plan business error must surface as ErrNoPlan: %v", s.Err)
	}
	if s.HTTPStatus != http.StatusOK {
		t.Fatalf("HTTPStatus = %d, want 200 (business error rides HTTP 200)", s.HTTPStatus)
	}
}

// 5xx → 瞬時失敗:Err 非空、HTTPStatus 帶回。
func TestQueryServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := monitor.New(srv.URL, 5*time.Second)
	s := c.Query(context.Background(), testProfile())
	if s.Err == nil {
		t.Fatal("5xx must error")
	}
	if s.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("HTTPStatus = %d, want 500", s.HTTPStatus)
	}
}

// 壞 JSON(200 但非 JSON)→ 確定性錯誤,非 panic。
func TestQueryBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>not json</html>")
	}))
	defer srv.Close()

	c := monitor.New(srv.URL, 5*time.Second)
	s := c.Query(context.Background(), testProfile())
	if s.Err == nil {
		t.Fatal("bad JSON must error")
	}
}
