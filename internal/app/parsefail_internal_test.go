package app

// 白箱測試:PD2 連續解析失敗的計數與一次性警報(卡片 F12 項)。
// trackParseFails 是非匯出方法,只有同包測試能直接驅動——
// 驗收測試走不到「踩線那輪通知、之後安靜、恢復後重新上膛」的序列。

import (
	"errors"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
)

// recordingNotifier 記下 title 序列(與 acceptance_test 的假件獨立)。
type recordingNotifier struct{ titles []string }

func (n *recordingNotifier) Notify(title, body string) { n.titles = append(n.titles, title) }

func failSnap(id string) *domain.QuotaSnapshot {
	return &domain.QuotaSnapshot{ProfileID: id, Err: errors.New("boom"), QueriedAt: time.Time{}}
}

func okSnap(id string, pct float64) *domain.QuotaSnapshot {
	return &domain.QuotaSnapshot{ProfileID: id, FiveHourPct: &pct, QueriedAt: time.Now()}
}

// TestParseFailAlertFiresOncePerStreak:第 2 次失敗踩線(限 2)才響一次,
// 之後連續失敗不再洗版;兩邊恢復後警報重新上膛,再踩線又響。
func TestParseFailAlertFiresOncePerStreak(t *testing.T) {
	nt := &recordingNotifier{}
	l := &Loop{Not: nt, Cfg: config.Default()}
	l.Cfg.ParseFailAlertLimit = 2

	var res LoopResult
	l.trackParseFails(failSnap("A"), failSnap("B"), &res)
	if res.Notified {
		t.Fatalf("streak 1 must stay quiet: %+v", res)
	}

	l.trackParseFails(failSnap("A"), failSnap("B"), &res)
	if !res.Notified || len(nt.titles) != 1 {
		t.Fatalf("streak 2 (limit 2) must alert exactly once: notified=%v titles=%v", res.Notified, nt.titles)
	}

	// 之後的失敗輪:警報閂上,不再響。
	res = LoopResult{}
	l.trackParseFails(failSnap("A"), failSnap("B"), &res)
	if res.Notified || len(nt.titles) != 1 {
		t.Fatalf("alert must fire once per streak: notified=%v titles=%v", res.Notified, nt.titles)
	}

	// 恢復:計數歸零、閂鎖重置。
	res = LoopResult{}
	l.trackParseFails(okSnap("A", 10), okSnap("B", 5), &res)
	if res.Notified || l.failA != 0 || l.failB != 0 || l.parseAlerted {
		t.Fatalf("recovery must reset streaks and latch: %+v failA=%d failB=%d", res, l.failA, l.failB)
	}

	// 再來一輪連續失敗踩線:要能再響(重新上膛生效)。
	l.trackParseFails(failSnap("A"), failSnap("B"), &res)
	if res.Notified {
		t.Fatalf("new streak 1 must stay quiet: %+v", res)
	}
	res = LoopResult{}
	l.trackParseFails(failSnap("A"), failSnap("B"), &res)
	if !res.Notified || len(nt.titles) != 2 {
		t.Fatalf("re-armed alert must fire again: notified=%v titles=%v", res.Notified, nt.titles)
	}
}

// TestStatusParseFailing:Status 的紅燈訊號跟著警報線走。
func TestStatusParseFailing(t *testing.T) {
	l := &Loop{Cfg: config.Default()} // ParseFailAlertLimit=5
	l.failA, l.failB = 4, 1
	if l.Status().ParseFailing {
		t.Fatal("streak below limit must not show red")
	}
	l.failB = 5
	if !l.Status().ParseFailing {
		t.Fatal("streak at limit must show red")
	}
	l.Cfg.ParseFailAlertLimit = 0 // 關掉警報線 → 永不紅
	if l.Status().ParseFailing {
		t.Fatal("limit 0 disables the red signal")
	}
}
