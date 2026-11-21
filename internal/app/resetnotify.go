package app

// The A-account 429-wall episode machine (2026-11-21 reset-notify). When
// account A is Current and its FRESH 5h quota sample crosses the configured
// wall line, ONE wall episode opens, anchored once at the quota API's
// FiveHourReset instant plus the configured buffer; at that instant (or
// earlier, when the wall clears first) the existing wake seam fires the third
// mode "RESET" — a RETRY order for sessions parked on the 429 wall.
//
// Hard rule (structural acceptance test): this path is a NOTIFY ONLY. It
// never writes the quota-mode file — the mode line stays untouched, the
// wake seam alone carries the order, and the message's self-filtering tail
// covers receivers that already moved on. Spec:
// docs/blueprints/2026-11-21-reset-notify.md (decisions 1-6, acceptance
// criteria; buffer default 1 min and early-end-fires are the two
// user-approved amendments).

import (
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
)

// fallbackWindow is the disclosed 5h constant behind the fallback anchor: a
// missing FiveHourReset puts the notify one full window after the observed
// crossing (decision 1; deliberately conservative — it can only be late).
const fallbackWindow = 5 * time.Hour

// resetEpisode is one wall episode's machine state; the zero value = closed.
// Every field is touched only under Loop.mu (the armed schedule's callback
// runs on its own goroutine, racing every Tick).
type resetEpisode struct {
	open         bool
	fired        bool // the episode's single fire attempt happened (success or failure)
	timer        *time.Timer
	fireAt       time.Time  // armed instant, logged for traceability
	resetAt      *time.Time // the anchored reset instant; nil = fallback anchor used
	watchersFile string     // delivery target captured at arm time
}

// resetNotifyTick advances the machine one round. Business rules (spec
// decisions 1-6):
//   - Cancels run FIRST and on EVERY round, fresh or stale: Current leaving A
//     or the master switch going off cancels the armed schedule and closes
//     the episode — both are app-state driven, never sample-driven (a switch
//     round's flip notify already covered the moved sessions).
//   - Master off: no detection at all, episode or not.
//   - Everything sample-driven eats FRESH samples only (roundFresh =
//     !dec.Stale): a stale round opens, closes, cancels, fires or re-anchors
//     NOTHING — the machine freezes so an aged cached sample can never
//     mimic a live wall.
//   - Over the wall (inclusive line): open once + anchor once per episode; a
//     DIFFERENT re-reported FiveHourReset re-anchors the remaining time
//     (still one fire per episode); an already-fired episode stays inert.
//   - Below the wall with an open episode: unfired and outside the min-gap →
//     fire now and close (user-approved early-fire amendment); inside the
//     gap → stay armed, later rounds re-evaluate (the notify must not be
//     lost); already fired → just close (the episode ends at drop-back).
//
// Hook: Tick calls this once per round, after the query/decision phase, so
// Current already reflects any switch this round made.
func (l *Loop) resetNotifyTick(now time.Time, cfg config.Config, snapA *domain.QuotaSnapshot, roundFresh bool) {
	// Phase 1 — cancels: app-state driven, applied before any sample gate so
	// they hold on stale rounds too (decision 3).
	if l.resetNotifyCancelOpened(cfg.ResetNotify) {
		return
	}
	// Phase 2 — static gates: master off, or A not Current (detection and the
	// early-fire path both require A; a flee-to-B earlier in this same round
	// must leave nothing behind).
	l.mu.Lock()
	isA := l.Current == "A"
	l.mu.Unlock()
	if !cfg.ResetNotify || !isA {
		return
	}
	// Phase 3 — freshness and sample guards: only a fresh, usable 5h sample
	// may move the machine (decision 1).
	if !roundFresh || snapA == nil || snapA.Err != nil || snapA.FiveHourPct == nil {
		return
	}
	// Phase 4 — sample-driven transition: over the line (inclusive) vs below.
	if *snapA.FiveHourPct >= cfg.ResetNotifyAfterPct {
		l.resetNotifyInWall(now, cfg, snapA)
		return
	}
	l.resetNotifyWallCleared(cfg.ResetNotifyMinGapMin)
}

// resetNotifyCancelOpened cancels an open episode when Current left A or the
// master switch went off (decision 3); reports whether a cancel ran — the
// round is then finished for this machine whatever the sample says.
func (l *Loop) resetNotifyCancelOpened(masterOn bool) bool {
	l.mu.Lock()
	if !l.rnEp.open || (masterOn && l.Current == "A") {
		l.mu.Unlock()
		return false
	}
	fireAt := l.rnEp.fireAt
	l.closeResetEpisodeLocked()
	l.mu.Unlock()
	l.logf("RESET 通知取消:Current 離開 A 或總開關關閉,已取消排程(原定 %s)", fireAt.Format(time.RFC3339))
	return true
}

// resetNotifyInWall runs the over-the-wall branch: open + anchor once per
// episode; a changed reset instant re-anchors the remaining time; an already
// fired episode stays inert until the wall clears (decisions 1-2).
func (l *Loop) resetNotifyInWall(now time.Time, cfg config.Config, snapA *domain.QuotaSnapshot) {
	l.mu.Lock()
	if l.rnEp.open {
		if l.rnEp.fired || sameReset(l.rnEp.resetAt, snapA.FiveHourReset) {
			// Already consumed, or the same anchor as before: never re-fire,
			// never re-arm (decision 2's idempotence half).
			l.mu.Unlock()
			return
		}
		// The API re-reported a DIFFERENT reset instant: re-anchor the
		// remaining time (decision 2; still one fire per episode).
		l.closeResetEpisodeLocked()
	}
	// Anchor once and arm the real schedule. The delay is measured from THIS
	// round's decision instant (the injected now — production passes
	// time.Now(), tests a fixed clock), never from the real wall clock: a
	// fireAt already past the decision instant yields a negative duration and
	// AfterFunc fires immediately (contract point 5's testability).
	fireAt, resetAt := resetFireAt(now, snapA, cfg.ResetNotifyBufferMin)
	wf := cfg.WatchersFile
	l.rnEp = resetEpisode{
		open: true, fireAt: fireAt, resetAt: resetAt, watchersFile: wf,
		timer: time.AfterFunc(fireAt.Sub(now), func() { l.resetNotifyScheduledFire(wf) }),
	}
	l.mu.Unlock()
	l.logf("RESET 通知排程:A 5h %.1f%% ≥ 牆線 %.1f%%,發射時刻 %s", *snapA.FiveHourPct, cfg.ResetNotifyAfterPct, fireAt.Format(time.RFC3339))
}

// resetNotifyWallCleared runs the below-the-wall branch with an open episode
// (user-approved amendment 2): the wall cleared early → fire now (once per
// episode, min-gap permitting) and close; inside the gap → stay armed; the
// scheduled fire already went out → just close (decision 5: the episode ends
// at drop-back, letting a later crossing notify again).
func (l *Loop) resetNotifyWallCleared(gapMin int) {
	l.mu.Lock()
	if !l.rnEp.open {
		l.mu.Unlock()
		return // no episode: nothing to clear
	}
	if l.rnEp.fired {
		// The scheduled fire consumed this episode; the drop-back closes it
		// so the next wall crossing can open a fresh one.
		l.closeResetEpisodeLocked()
		l.mu.Unlock()
		l.logf("RESET 通知:A 用量掉回牆線下,episode 結束(通知已發過,不二發)")
		return
	}
	if !l.resetNotifyGapOKLocked(gapMin) {
		// Gap not satisfied: do NOT fire, do NOT close — the episode stays
		// armed and later rounds re-evaluate (the wall's notify must not be
		// lost to the anti-spam window).
		l.mu.Unlock()
		return
	}
	wf := l.rnEp.watchersFile
	l.rnEp.fired = true // one attempt per episode — even if the spawn fails
	l.rnLastFire = time.Now()
	l.closeResetEpisodeLocked()
	l.mu.Unlock()
	l.logf("RESET 通知提早發:牆線已回落,立即發送 retry 令並結束 episode")
	l.resetNotifyDeliver(wf)
}

// resetNotifyScheduledFire is the armed schedule's callback (its own
// goroutine at fireAt). One attempt per episode; inside the min-gap it
// suppresses and marks the episode consumed (kept open so in-wall
// re-detection stays inert); the fire goes through the wake seam and a
// failure is log-only (decision 5).
func (l *Loop) resetNotifyScheduledFire(wf string) {
	l.mu.Lock()
	if !l.rnEp.open || l.rnEp.fired {
		// Closed (cancelled / early-fired) or already consumed: the
		// single-fire guarantee holds against the early-fire race.
		l.mu.Unlock()
		return
	}
	l.rnEp.timer = nil // the schedule itself has run
	gapMin := l.Cfg.ResetNotifyMinGapMin
	if !l.resetNotifyGapOKLocked(gapMin) {
		l.rnEp.fired = true // consumed: no retry storm; the episode stays open while the wall holds
		l.mu.Unlock()
		l.logf("RESET 通知:距上次通知不足最小間隔(%d 分),抑制本次並視為已發", gapMin)
		return
	}
	l.rnEp.fired = true
	l.rnLastFire = time.Now()
	l.mu.Unlock()
	l.resetNotifyDeliver(wf)
}

// resetNotifyDeliver spawns the RESET retry order through the existing wake
// seam (the third wake mode — a RETRY order carrying NO mode change; the
// quota-mode file is never written on this path). A failure is log-only: the
// attempt is already consumed, and this machine must never affect polling or
// switching (decision 5, acceptance: fire failure counts as the single fire).
func (l *Loop) resetNotifyDeliver(watchersFile string) {
	if l.Wake == nil {
		l.logf("RESET 通知:wake seam 未接線,略過(本次 episode 已消耗)")
		return
	}
	if err := l.Wake.Wake(watchersFile, "RESET"); err != nil {
		l.logf("RESET 通知發送失敗(僅記錄,不重試):%v", err)
		return
	}
	l.logf("RESET 通知已發送(retry 令 → %s)", watchersFile)
}

// resetNotifyGapOKLocked reports whether the min-gap between RESET notifies
// is satisfied, measured with REAL elapsed time since the last fire stamp —
// deliberately not the tick's injected clock, which can sit arbitrarily far
// from the wall clock (decision 6). Caller holds mu.
func (l *Loop) resetNotifyGapOKLocked(gapMin int) bool {
	if l.rnLastFire.IsZero() || gapMin <= 0 {
		return true
	}
	return time.Since(l.rnLastFire) >= time.Duration(gapMin)*time.Minute
}

// closeResetEpisodeLocked stops the armed schedule (if any) and closes the
// episode. Caller holds mu.
func (l *Loop) closeResetEpisodeLocked() {
	if l.rnEp.timer != nil {
		l.rnEp.timer.Stop()
	}
	l.rnEp = resetEpisode{}
}

// resetFireAt computes the episode's fire instant: FiveHourReset + buffer;
// a missing FiveHourReset anchors on the first-crossing proxy — the sample's
// capture time, else the tick's decision instant — plus the disclosed 5h
// constant and the buffer (decision 1). The second return mirrors which
// anchor was used (nil = fallback), so a later real instant counts as a
// re-anchor trigger.
func resetFireAt(now time.Time, snapA *domain.QuotaSnapshot, bufferMin int) (time.Time, *time.Time) {
	buffer := time.Duration(bufferMin) * time.Minute
	if r := snapA.FiveHourReset; r != nil {
		stored := *r
		return r.Add(buffer), &stored
	}
	anchor := snapA.QueriedAt
	if anchor.IsZero() {
		anchor = now
	}
	return anchor.Add(fallbackWindow + buffer), nil
}

// sameReset reports whether the stored anchor matches the API's re-reported
// instant; two fallback episodes (both nil) count as the same anchor.
func sameReset(stored, reported *time.Time) bool {
	if stored == nil || reported == nil {
		return stored == nil && reported == nil
	}
	return stored.Equal(*reported)
}
