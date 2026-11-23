# 2026-11-23 預算錯過結算修復(budget missed-settlement fix)

需求文件(spec)。單一事實來源;實作與審查以本文件為準。

## 事故(使用者逐字回報,2026-11-23)

> 我現在是關掉當日額度滿強制切A, 才能維持用B, check b:/image 5.png 應該有額度才對
> 到週二 14:57 前:天花板 週19.0% · 尚可 0.0pt(不強制)
> 應該算錯了, day 1 應該是 fri 1457 to mon 1457
> 現在是 day2 不可能上限還是 19
> unit test要加強

現場數據(B 帳號,週一 2026-11-23 傍晚):5h 僅 1%、**7d 週窗口僅用 19%**、重置倒數 3d22h(= 週五 14:57);卡片顯示「到週二 14:57 前:天花板 週19.0% · 尚可 0.0pt」。A 帳號 5h 100%(強制切回等於斷糧),使用者被迫關閉「當日額度滿強制切 A」才能留在 B。

## 根因(已實證,證據 = state.json + 程式碼)

1. **週五 11-20 14:58** Day 1 結算:週% 剛重置 = 0、剩 5 個預算日 → 天花板 `95÷5 = 19.0`,日界 = 週一 14:57(`state.json`:`DayStartWeekly:0`、`CeilingToday:19`、`DayStartAt:11-20T14:58`)。
2. **週一 14:57 過後才開機**(日界通過時程式沒在跑,週一結算錯過)。
3. **開機載入** `LoadBudget`(`internal/app/adapters.go`)對過期日界呼叫 `policy.SnapBoundary` → 快轉**週一 14:57 → 週二 14:57:23.998**(`state.json` 的 `DayBoundary` 奈秒與錨點一致 = snap 手筆),**吞掉應補跑的結算**。
4. 第一輪 Tick:`settlementDue` 看到「週二」在未來 → 不結算;`MarkBudgetDirty` 的開機重算走 `policy.RefreshBudget` 重算路徑,以**死掉的舊日基準**(DayStartAt = 週五)重算 → 天花板 19.0 被重鑄確認。週% 持續上漲,19.2 ≥ 19.0 時 P5 強制切回 A。
5. 預警閂鎖翻動輪(`ensureEmitted`)把含快轉邊界的信封整顆落盤,錯誤狀態化石化管理(`state.json` 同時存在「週二邊界 + Day 1 舊基準」這種結算不可能產出的組合)。

## 定案語意(settled decisions)

- **D-A(SnapBoundary 守則)**:傳入日界已 ≤ now(過期)→ **原樣退回,不快轉**。過期日界是結算機制的扳機,該被「結算」消費,不該被「快轉」消費。日界在未來 → 既有 D6c 行為(校正到錨點鐘點)完全不變。等號情境(boundary == now)屬過期側:不快轉(結算本來就該在該瞬間觸發,審計 M7 inclusive 語意)。
- **D-B(RefreshBudget 重算守則)**:重算路徑(recompute branch,`DayStartWeekly` 非 nil)遇過期日界 → 回傳不可算(`ok=false`),信封原樣退回,交給 Tick 結算。冷啟路徑(呼叫 `settleBudget`)不變 — 那本身就是結算。理由:重算路徑以 `DayStartAt`(已死掉的舊日)推天數,會在 Day 2 重鑄 Day 1 的天花板。
- **D-C(LoadBudget 注入時間)**:`LoadBudget(path, workdaysOnly)` 改為 `LoadBudget(path, now, workdaysOnly)`,呼叫端 `cmd/cc-fuse/main.go` 傳 `time.Now()`。理由:boot 測試需要確定論(既有兩條 boot 測試讀真實牆鐘鏡像 `wantSnappedBoundary(time.Now())`,在新守則下會日期相依地翻紅,必須連同遷移到注入時刻)。
- **D-D(引擎語意 pin)**:`Evaluate` 遇過期日界的第一輪即結算 — 基準重新錨定為**當下**週%、天數重算、日界 = 下一個錨點時刻。此語意引擎**現已正確**,以綠色 pin 測試釘住,禁止實作改動引擎層來「修」這個 bug(修在 snap 與 refresh 兩處)。
- **既有測試遷移**(簽名變更的必然連帶,期望值不變):
  - `TestLoadBudgetKeepsExistingDayStart`、`TestLoadBudgetSeedsDayStartBeforeSnap` 改傳注入 now(2026-11-19 時段),`wantSnappedBoundary(注入now)`、chain tail `RefreshBudget` 的 `Now` 同步注入 — 期望值與原設計完全一致,僅去除牆鐘相依。
  - `cmd/cc-fuse/main.go` 呼叫端補 `time.Now()`。
- **D-E(既有測試遷移 #2,編碼審查標定)**:`internal/policy/budgetsnap_test.go` 的 `TestSnapBoundaryWeekendMerge` 三條斷言全數以 `snapFriMidnight`(週五 00:00)為日界、now 在其後 — 全是**過期**形狀,在 D-A 下會翻紅,且它釘的正是本事故的機制(過期結算被吞掉滾過週末)。屬性「workdaysOnly 時 snap 永不鑄出週末 cut-off」在 D-A 下僅對**未來日界**成立 → 遷移為未來形狀,期望值不變:
  1. 日界 = 週六 11-21 00:00(legacy 鏈落在週六)、now = 週五 11-20 16:00(未來)→ 校正跨過週末 → **Mon 11-23 14:57**(原期望值)。
  2. 日界 = 週日 11-22 00:00、now = 週六 11-21 10:00(未來)→ **Mon 11-23 14:57**(原期望值)。
  3. workdaysOnly=false、日界 = 週日 11-22 00:00、now = 週六 10:00 → **Sat 11-21 14:57**(原期望值)。
  編碼審查已全庫掃過:這是 D-A 唯一的既有測試傷亡;其餘 SnapBoundary/RefreshBudget 既有測試全部使用未來日界,不受影響。

## 正確行為(事故重放的期望數字)

開機載入 {錨點 11-27 14:57:23.998、日界 11-23 14:57(過期)、基準 0、DayStartAt 11-20 14:58、天花板 19},now = 週一 16:37,B 週% 19.2(新鮮),當前帳號 B:

- 補結算發生(`Settled=true`):基準 = 19.2、天數 = 4(二、三、四 + 重置日五的切片)→ **天花板 = 19.2 + (95−19.2)/4 = 38.15**;日界 = 週二 14:57:23.998;結算落盤 state.json(§7)。
- P5 不觸發(週 19.2 < 38.15)→ **留守 B**,不切回死掉的 A。
- 對照錯誤行為(修前):天花板停在 19.0、尚可 0.0、P5 強制切 A。

## 驗收測試路徑(先於實作存在,RED 已確認)

- `internal/policy/missedsettlement_acceptance_test.go`:
  - `TestSnapBoundaryKeepsOverdueBoundary`(S1 核心,RED)
  - `TestSnapBoundaryNeverSwallowsOverdue`(S1 性質測試:6 種過期形狀全數原樣退回,RED)
  - `TestSnapBoundaryFutureBoundaryStillSnaps`(S1 對照 pin:未來日界照舊校正,綠)
  - `TestRefreshBudgetOverdueBoundaryWaitsForSettlement`(S2,RED;**帶新鮮 QuotaB** — 拒絕原因必須可歸因於「日界過期」而非「資料缺失」;並釘全欄位 passthrough:基準/DayStartAt/錨點/預警閂鎖一動即紅)
  - `TestEvaluateSettlesOverdueBoundaryFirstTick`(D-D 引擎 pin,綠)
- `internal/app/missedsettlement_boot_acceptance_test.go`:
  - `TestLoadBudgetKeepsOverdueBoundary`(B1,RED:新簽名編譯錯誤 = breaking-change 合法 RED)
  - `TestLoadBudgetFutureBoundaryStillSnaps`(B1 對照 pin,編譯後綠)
  - `TestBootAfterMissedBoundarySettlesOnFirstTick`(B2 完整事故重放,含落盤驗證,RED)

## 范圍外(不做)

- 不改 UI 卡片、不改 `daysLeftInWindow` 天數語意、不改 P5/P6 切換規則、不動 `ensureEmitted` 的落盤通道本身(D-A/D-B 修好後,該通道攜帶的信封永遠是合法信封,不需另行防守)。
