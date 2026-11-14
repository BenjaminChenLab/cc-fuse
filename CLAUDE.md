# cc-fuse

Claude Code 帳號自動切換器(保險絲:額度快過載時搶先熔斷切換,保護工作不被 429 燒死)— Windows 系統匣常駐小工具(Go)。

## 這是什麼

監看兩個 z.ai GLM Coding Plan 帳號的額度,自動切換 `~/.claude/settings.json` 裡的供應商:

- **A(共用帳號,永遠優先)**:5 小時% 或週% ≥ 切換閾值 → 切到 B;兩窗口都回到安全區 → 切回 A。
- **B(自購帳號,避難所+配速預算)**:每日額度 = (週預算總量 − 週%已用) ÷ 剩餘工作日數;天花板到了 → B 不再是避難所,正在 B 上也立刻切回 A,寧可停等也不超支。
- 同步把 `S:\claude-cache\quota-mode.md` 的 `mode:` 行翻成 RACING/SAVING(卡片內容不動)。
- 目的:**完全取代 cc-switch 在本機的用途**(切 Claude Code 供應商 + 看額度),退休 cc-switch,沒有同步問題。

## 關鍵外部事實(已驗證)

- **額度 API(免費、帳號級、含隊友用量)**:`GET https://api.z.ai/api/monitor/usage/quota/limit`,header `Authorization: <apiKey>`(智譜**不加** Bearer 前綴)。回傳 `data.limits[]`:`unit=3` 是 5 小時窗口、`unit=6` 是週窗口;每條有 `percentage`(已用%)與 `nextResetTime`(ms)。按量付費帳號會回「不存在 coding plan」的業務錯誤。
- **切換機制**:只改 `settings.json` `env` 區塊的供應商欄位(`ANTHROPIC_API_KEY`、`ANTHROPIC_BASE_URL`、`ANTHROPIC_DEFAULT_*` 模型對應),其他欄位(permissions、statusLine、plugins…)一律不碰;原子寫入(暫存檔+改名);Claude Code 支援熱切換,進行中 session 下一個請求生效。
- **供應商身分判定**:A、B 端點相同(都是 z.ai),身分以 `ANTHROPIC_API_KEY` 比對為準。
- **cc-switch 參考程式碼**:`github.com/farion1231/cc-switch`(Tauri/Rust);額度解析在 `src-tauri/src/services/coding_plan.rs`。僅作邏輯參考,本機不再依賴它執行。

## 開發規矩

- **所有數字一律可設定、不寫死**:切換閾值、恢復閾值、預警線、輪詢間隔、B 週預算總量、每日保底額度、假日要不要算(勾選)、預算日結算時刻。
- 預算規則細節:預算週以 B 週窗口重置時刻自動錨定(從 `nextResetTime` 偵測,例:週五 15:00 重置 → 預算日每天 15:00 結算);剩餘天數只數工作日(預設排除週六日)。
- 單例鎖(防止兩份互改 settings.json);設定存 `%APPDATA%`;第一次改動前備份原檔。
- 測試:額度解析用真實回傳樣本(cc-switch 測試碼有 fixture);政策引擎表格驅動逐規則驗證;settings.json 合併要測「不相關欄位零損傷」。
- 設計文件:`docs/blueprints/2026-11-15-cc-fuse-design.md`(單一事實來源)。
