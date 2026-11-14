// Package monitor queries the z.ai account-level quota endpoint and parses
// the response into domain.QuotaSnapshot (design §4, V1–V4).
//
// The endpoint is free, per-account (team usage included) and does not burn
// conversation quota. Parsing rules follow the T01 card's 9 z.ai rules,
// which are a faithful port of cc-switch's coding_plan.rs — including the
// unit=3/6 classification that must override reset-time ordering (issue
// #3036) and the pay-as-you-go "no coding plan" business error family.
package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"cc-fuse/internal/domain"
)

// ErrNoPlan marks the pay-as-you-go business error: the account simply has
// no coding plan (HTTP 200 + success:false). errors.Is-able so callers can
// route to the balance-style handling without string matching.
var ErrNoPlan = errors.New("z.ai: no coding plan (pay-as-you-go account)")

// ErrAuthInvalid marks HTTP 401/403: the API key is dead. Deterministic —
// policy must not retry it, the tray shows red instead.
var ErrAuthInvalid = errors.New("z.ai: authentication failed")

// quotaPath is the monitoring endpoint, always anchored at the host root:
// profile.BaseURL may carry an /api/anthropic suffix for coding, but the
// quota route never inherits it (decision 2026-11-15, prevents double-path).
const quotaPath = "/api/monitor/usage/quota/limit"

// noPlanKeywords is the "message says there is no coding plan" family
// (rule 7): union of the T01 card list and cc-switch's zhipu_body_has_no_plan,
// simplified + traditional Chinese spellings both included.
var noPlanKeywords = []string{
	"不存在", "没有", "沒有", "未开通", "未開通",
	"not exist", "not found", "not have", "doesn't have", "no coding plan", "not subscribed",
}

// errBodyLimit caps how much of an error body we keep for the message.
const errBodyLimit = 4096

// Client queries the z.ai quota endpoint for one profile per call.
type Client struct {
	hostRoot string       // scheme://host only — the quota path is appended verbatim
	http     *http.Client // timeout-carrying client; injectable for tests
}

// New builds a client. baseURL is the profile's coding endpoint; only its
// scheme+host are kept, so a base with an /api/anthropic suffix still hits
// the quota route at the host root. timeout is the per-request HTTP timeout
// (callers pass 15s; tests inject shorter values).
func New(baseURL string, timeout time.Duration) *Client {
	root := strings.TrimRight(baseURL, "/")
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		ru := *u
		ru.Path = ""
		ru.RawPath = ""
		ru.RawQuery = ""
		ru.Fragment = ""
		ru.User = nil
		root = strings.TrimRight(ru.String(), "/")
	}
	return &Client{
		hostRoot: root,
		http:     &http.Client{Timeout: timeout},
	}
}

// Query fetches one quota sample for profile p and returns the snapshot.
//
// The failure taxonomy (card contract): 401/403 wrap ErrAuthInvalid
// (deterministic, dead key); a success:false body whose msg matches the
// no-coding-plan family wraps ErrNoPlan (pay-as-you-go account, rides on
// HTTP 200); everything else — network, timeout, 5xx, unknown business
// error, unparseable body — is a plain error. On any failure the snapshot
// still carries ProfileID, QueriedAt and (when known) HTTPStatus, so the
// policy engine can age the sample out instead of acting on stale numbers.
func (c *Client) Query(ctx context.Context, p domain.Profile) domain.QuotaSnapshot {
	now := time.Now()
	s := domain.QuotaSnapshot{ProfileID: p.ID, QueriedAt: now}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.hostRoot+quotaPath, nil)
	if err != nil {
		s.Err = fmt.Errorf("build z.ai quota request: %w", err)
		return s
	}
	// 智譜慣例(V1):Authorization 直放 key、不加 Bearer;內容型別 JSON;
	// Accept-Language 壓英文,讓業務錯文案落在可預期的語系。
	req.Header.Set("Authorization", p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en-US,en")

	resp, err := c.http.Do(req)
	if err != nil {
		s.Err = fmt.Errorf("z.ai quota query: %w", err)
		return s
	}
	defer resp.Body.Close()
	s.HTTPStatus = resp.StatusCode

	// 金鑰失效分流:401/403 屬確定性失敗,與瞬時/業務錯三者互斥(規則 8)。
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		s.Err = fmt.Errorf("%w (HTTP %d)", ErrAuthInvalid, resp.StatusCode)
		return s
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
		s.Err = fmt.Errorf("z.ai quota HTTP %d: %s", resp.StatusCode, string(body))
		return s
	}

	// 成功回應整讀,不設上限——限流是給錯誤訊息片段用的,拿到完整回應體
	// 才解析是 cc-switch 的陷阱照避(讀體中斷=瞬時,解析失敗=確定性)。
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		s.Err = fmt.Errorf("read z.ai quota body: %w", err)
		return s
	}

	parsed := Parse(data)
	parsed.ProfileID = p.ID
	parsed.QueriedAt = now
	parsed.HTTPStatus = s.HTTPStatus
	return parsed
}

// Parse decodes a z.ai quota response body into a snapshot, with no I/O.
//
// Rules (T01 card, ported from cc-switch coding_plan.rs):
//  1. only entries whose type equals TOKENS_LIMIT or CREDIT_LIMIT
//     (case-insensitive) count; TIME_LIMIT and friends are ignored;
//  2. unit=3 → 5h bucket, unit=6 → weekly bucket (number 1 or 7 both
//     weekly) — explicit classification beats any reset ordering;
//  3. missing/unknown unit → fallback: entries without nextResetTime fill
//     the 5h slot first, the rest fill empty slots in reset ascending order;
//  4. percentage is USED percent; string/null → 0; negative/over-100
//     passes through unclamped;
//  5. the 5h bucket at 0% may genuinely lack nextResetTime → nil reset;
//  6. at most two entries (5h + weekly) are kept, extras dropped; a
//     duplicate unit fills the first slot then the remaining slot;
//  7. success:false with a no-coding-plan msg → ErrNoPlan, other
//     success:false → plain business error; success with no data → error.
//
// Parse never sets ProfileID or QueriedAt — that is Query's job.
func Parse(data []byte) domain.QuotaSnapshot {
	var s domain.QuotaSnapshot
	var body struct {
		Success *bool  `json:"success"`
		Msg     string `json:"msg"`
		Data    *struct {
			Level  string            `json:"level"`
			Limits []json.RawMessage `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		s.Err = fmt.Errorf("z.ai quota body is not JSON: %w", err)
		return s
	}

	// 業務錯信封(規則 7):success=false 一律視為查詢失敗;「沒有 coding plan」
	// 家族再細分成 ErrNoPlan(按量帳號),其餘是一般業務錯。
	if body.Success != nil && !*body.Success {
		if bodyHasNoPlan(body.Msg) {
			s.Err = fmt.Errorf("%w: %s", ErrNoPlan, body.Msg)
		} else {
			s.Err = fmt.Errorf("z.ai business error: %s", body.Msg)
		}
		return s
	}
	if body.Data == nil {
		s.Err = errors.New("z.ai quota response missing 'data' field")
		return s
	}

	s.PlanLevel = body.Data.Level
	five, week := classifyLimits(body.Data.Limits)
	s.FiveHourPct, s.FiveHourReset = five.pct, five.reset
	s.WeeklyPct, s.WeeklyReset = week.pct, week.reset
	return s
}

// limitEntry is one classified limits[] entry, kept in raw form until slot
// assignment: resetMs/hasReset drive the fallback ordering, pct/reset the
// snapshot fields.
type limitEntry struct {
	pct      float64
	resetMs  int64
	hasReset bool
}

// bucket is a filled snapshot window; zero value = bucket absent.
type bucket struct {
	pct   *float64
	reset *time.Time
}

// classifyLimits assigns limits[] entries to the 5h and weekly slots.
//
// Order of precedence (rules 2/3/6): explicit unit wins first; leftovers go
// through the fallback heuristic (no-reset entries claim the 5h slot, then
// reset ascending); a third entry is dropped. A duplicate unit=3/6 whose
// first slot is taken lands in the leftover pool, so it fills the remaining
// slot rather than being lost.
func classifyLimits(items []json.RawMessage) (fiveHour, weekly bucket) {
	var leftovers []limitEntry
	for _, raw := range items {
		var item struct {
			Type          string          `json:"type"`
			Unit          json.RawMessage `json:"unit"`
			Percentage    json.RawMessage `json:"percentage"`
			NextResetTime json.RawMessage `json:"nextResetTime"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue // 壞條目整條丟棄,不連坐整份回應
		}
		// 規則 1:type 大小寫不敏感,只認 TOKENS_LIMIT / CREDIT_LIMIT。
		if !(strings.EqualFold(item.Type, "TOKENS_LIMIT") || strings.EqualFold(item.Type, "CREDIT_LIMIT")) {
			continue
		}
		resetMs, hasReset := rawInt64(item.NextResetTime)
		entry := limitEntry{
			pct:      rawFloat64(item.Percentage),
			resetMs:  resetMs,
			hasReset: hasReset,
		}

		// 規則 2:unit=3 → 5h、unit=6 → 週(number 不參與分類);缺/不認得進兜底池。
		switch u, ok := rawInt64(item.Unit); {
		case ok && u == 3 && fiveHour.pct == nil:
			fiveHour = entry.bucket()
		case ok && u == 6 && weekly.pct == nil:
			weekly = entry.bucket()
		default:
			leftovers = append(leftovers, entry)
		}
	}

	// 規則 3 兜底:無 reset 者優先(5h 桶 0% 沒有 reset 是真實形態),
	// 其餘按 reset 升序,依次填仍空缺的槽。
	sortEntries(leftovers)
	for _, e := range leftovers {
		if fiveHour.pct == nil {
			fiveHour = e.bucket()
		} else if weekly.pct == nil {
			weekly = e.bucket()
		}
		// 規則 6:z.ai 目前最多兩條,多出來的直接丟棄。
	}
	return fiveHour, weekly
}

// sortEntries orders leftovers no-reset-first, then reset ascending — the
// reset ascending within is what makes the weekly-bucket-resets-earlier
// trap (issue #3036) safe, because explicit unit sorting already bypassed
// this path for classified entries.
func sortEntries(entries []limitEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && lessEntry(entries[j], entries[j-1]); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

// lessEntry reports whether a sorts before b in fallback order.
func lessEntry(a, b limitEntry) bool {
	if a.hasReset != b.hasReset {
		return !a.hasReset // 無 reset 者排最前,優先認領 5h 槽
	}
	return a.resetMs < b.resetMs
}

// bucket converts a classified entry into snapshot window fields.
func (e limitEntry) bucket() bucket {
	pct := e.pct
	reset := (*time.Time)(nil)
	if e.hasReset {
		t := time.UnixMilli(e.resetMs)
		reset = &t
	}
	return bucket{pct: &pct, reset: reset}
}

// rawFloat64 reads a JSON number from raw; string/null/absent/anything
// else reads 0 (rule 4, mirroring cc-switch's as_f64().unwrap_or(0.0)).
func rawFloat64(raw json.RawMessage) float64 {
	if !rawStartsNumeric(raw) {
		return 0
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0
	}
	return v
}

// rawInt64 reads a JSON integer from raw; string/null/absent → not ok.
func rawInt64(raw json.RawMessage) (int64, bool) {
	if !rawStartsNumeric(raw) {
		return 0, false
	}
	var v int64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false
	}
	return v, true
}

// rawStartsNumeric reports whether raw begins like a JSON number (a digit
// or minus sign). JSON null/string literals start with other bytes.
func rawStartsNumeric(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	c := raw[0]
	return c == '-' || (c >= '0' && c <= '9')
}

// bodyHasNoPlan decides whether a success:false msg means "this account has
// no coding plan" (pay-as-you-go). The message must mention coding plan at
// all (whitespace/punctuation-insensitive) AND contain one of the missing
// keywords — otherwise genuine quota failures would masquerade as balance.
func bodyHasNoPlan(msg string) bool {
	lower := strings.ToLower(msg)
	var compact strings.Builder
	for _, r := range lower {
		if unicode.IsSpace(r) || r == '_' || r == '-' {
			continue
		}
		compact.WriteRune(r)
	}
	if !strings.Contains(compact.String(), "codingplan") {
		return false
	}
	for _, kw := range noPlanKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
