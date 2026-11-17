// Package tray renders the cc-fuse system-tray presence (design §8): a
// four-color icon, a dual-percentage tooltip and the right-click menu
// [狀態 | 切到 A | 切到 B | 暫停/恢復自動切 B | 設定 | 結束].
//
// PickIcon/PickTooltip/StatusText are the pure rendering decisions
// (unit-tested); Run is the systray wiring and stays thin — every menu
// action only mutates the Loop's runtime knobs or opens the settings window.
package tray

import (
	"fmt"

	"cc-fuse/internal/app"
	"cc-fuse/internal/domain"
)

// IconState is one of the tray icon's four colors (design §8).
type IconState int

// The icon states: solid green/orange while auto-switching on the shared/
// personal account, a hollow ring in the account's color while paused (shape
// carries the paused meaning, color keeps "who am I on" — 2026-11-17
// Benjamin's pick), gray when the data ran stale or the state is unknown,
// red when the current key is dead or quota parsing has failed past the
// alert limit.
const (
	IconGreenA  IconState = iota // 綠實心=自動切換中,使用 A
	IconOrangeB                  // 橘實心=自動切換中,使用 B
	IconGray                     // 灰實心=資料過期或無法判定
	IconRed                      // 紅=金鑰失效或連續解析失敗
	IconHollowA                  // 綠空心環=暫停自動切 B,使用 A
	IconHollowB                  // 橘空心環=暫停自動切 B,使用 B
)

// PickIcon resolves the render state by severity: red beats the paused
// hollow ring beats stale gray beats the solid account colors — a dead key
// or parse-drift alert must stay visible in every other state; while paused
// the ring keeps the account color (stale data matters less when nothing
// switches automatically); a paused app with an unknown current account
// stays conservatively gray.
func PickIcon(st app.Status) IconState {
	switch {
	case st.KeyDead || st.ParseFailing:
		return IconRed
	case !st.Autopilot:
		if st.Current == "B" {
			return IconHollowB
		}
		if st.Current == "A" {
			return IconHollowA
		}
		return IconGray // 暫停但無法判定目前在誰身上:保守顯示灰
	case st.Stale:
		return IconGray
	case st.Current == "A":
		return IconGreenA
	case st.Current == "B":
		return IconOrangeB
	default:
		return IconGray // 尚無法判定目前在誰身上:保守顯示灰
	}
}

// PickTooltip builds the tray tooltip, e.g. `A: 42%(5h) · B: 12%(5h)` —
// each side shows its last good 5h percent, or ? until a sample exists.
func PickTooltip(st app.Status) string {
	return fmt.Sprintf("A: %s(5h) · B: %s(5h)", pct(st.A), pct(st.B))
}

// StatusText builds the tray menu's status line: who is active, both sides'
// percentages, and whether autopilot is running/paused/ailing.
func StatusText(st app.Status) string {
	who := st.Current
	if who == "" {
		who = "?"
	}
	state := "自動切換中"
	switch {
	case st.ParseFailing || st.KeyDead:
		state = "異常"
	case !st.Autopilot:
		state = "已暫停自動切B"
	case st.Stale:
		state = "資料過期"
	}
	return fmt.Sprintf("目前 %s · A: %s · B: %s · %s", who, pct(st.A), pct(st.B), state)
}

// pct renders one side's 5h usage percent for the tooltip; no sample or an
// absent 5h bucket reads "?" rather than a misleading 0%.
func pct(s *domain.QuotaSnapshot) string {
	if s == nil || s.FiveHourPct == nil {
		return "?"
	}
	return fmt.Sprintf("%.0f%%", *s.FiveHourPct)
}
