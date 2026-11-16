package ui

// indexHTML is the settings window page served at / (design §8: 內嵌本機頁).
// Dark refined theme + EN/zh i18n (2026-11-17): every string comes from the
// I18N dictionary applied via data-i18n attributes; the language defaults to
// the browser's (zh → 中文, otherwise English), the header button overrides
// it and the choice persists in localStorage. Every dynamic string is still
// built via DOM textContent (never innerHTML).
// The quota numbers come from GET /api/status and refresh every 5s;
// switching posts /api/switch (P1 manual, immediate). Candidate names/keys
// come from the cc-switch database (external data).
const indexHTML = `<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<title>cc-fuse 設定</title>
<style>
:root{--bg:#0c0d10;--card:#16171c;--card2:#1c1e24;--border:#26272e;--border2:#3a3c46;--text:#e8e8ec;--muted:#9a9aa5;--accent:#3b82f6;--accent2:#2563eb;--ok:#34d399;--warn:#f59e0b;--bad:#f87171}
 body{background:radial-gradient(1100px 500px at 85% -10%,#131722 0%,var(--bg) 55%);color:var(--text);font-family:"Segoe UI","Microsoft JhengHei",sans-serif;font-size:16px;margin:20px auto;max-width:940px}
 h2{font-size:16px;letter-spacing:2px;font-weight:600;color:var(--muted);margin:26px 0 12px;user-select:none}
 fieldset{background:var(--card);border:1px solid var(--border);border-radius:14px;padding:16px 18px;margin-bottom:14px}
 legend{color:var(--accent);font-weight:600;font-size:14px;padding:0 8px}
 label{display:inline-block;min-width:170px;color:var(--text);cursor:help}
 input{background:var(--card2);color:var(--text);border:1px solid var(--border2);border-radius:8px;padding:7px 10px;font-size:15px}
 input:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 3px rgba(59,130,246,.18)}
 input[type=text]{width:340px}
 input[type=number]{width:96px}
 textarea{width:480px;min-height:120px;background:var(--card2);color:var(--text);border:1px solid var(--border2);border-radius:8px;padding:8px 10px;font-family:Consolas,monospace;font-size:13px;resize:vertical;vertical-align:top}
 textarea:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 3px rgba(59,130,246,.18)}
 button{padding:8px 18px;border-radius:10px;border:1px solid var(--border2);background:var(--card2);color:var(--text);cursor:pointer;font-size:15px;transition:.15s}
 button:hover{border-color:var(--accent);color:#fff}
 button.primary{background:var(--accent2);border-color:var(--accent2);color:#fff;font-weight:600}
 button.primary:hover{background:var(--accent)}
 button:disabled{opacity:.5;cursor:default}
 button.eye{padding:7px 12px;margin-left:6px;font-size:14px}
 #lang{position:fixed;top:12px;right:16px;z-index:9;padding:5px 12px;font-size:13px;border-radius:999px}
 .card{display:flex;align-items:center;gap:14px;border:1px solid var(--border);border-radius:14px;padding:14px 18px;margin-bottom:10px;background:var(--card);transition:.15s}
 .card:hover{border-color:var(--border2)}
 .card.active{border-color:var(--accent);box-shadow:0 0 0 3px rgba(59,130,246,.12)}
 .av{width:46px;height:46px;border-radius:14px;background:linear-gradient(135deg,#23252d,#2e3138);display:flex;align-items:center;justify-content:center;font-weight:700;color:var(--muted);flex:none;font-size:17px}
 .meta{flex:1;min-width:0}
 .name{font-size:18px;font-weight:700;display:flex;align-items:center;gap:8px}
 .url{color:#6ea8ff;font-size:13px;word-break:break-all;margin:2px 0 8px}
 .qrow{display:flex;align-items:center;gap:10px;margin:3px 0}
 .qlabel{width:28px;color:var(--muted);font-size:13px;flex:none}
 .qtrack{flex:1;height:8px;background:var(--border);border-radius:999px;overflow:hidden}
 .qfill{height:100%;border-radius:999px;background:var(--ok);transition:width .4s}
 .qfill.warn{background:var(--warn)}.qfill.bad{background:var(--bad)}
 .qval{width:54px;text-align:right;font-weight:600;font-size:15px;flex:none}
 .chip{display:inline-flex;align-items:center;background:var(--card2);border:1px solid var(--border);border-radius:999px;padding:3px 12px;font-size:14px;color:var(--text);margin:8px 8px 0 0}
 .slot{font-size:11px;color:var(--muted);border:1px solid var(--border2);border-radius:999px;padding:1px 8px;font-weight:600;letter-spacing:1px}
 .good{color:var(--ok)}.warn{color:var(--warn)}.bad{color:var(--bad)}
 .cd{color:var(--muted);font-size:13px;margin-left:4px}
 .bar{display:flex;align-items:center;gap:10px;margin:14px 0;color:var(--muted);font-size:14px;flex-wrap:wrap}
 .grow{flex:1}
 .switch{position:relative;display:inline-block;width:42px;height:22px;vertical-align:middle}
 .switch input{appearance:none;-webkit-appearance:none;position:absolute;inset:0;width:100%;height:100%;margin:0;cursor:pointer;opacity:0}
 .switch i{position:absolute;inset:0;background:#2a2b31;border-radius:999px;transition:.2s;pointer-events:none;border:1px solid var(--border2)}
 .switch i:before{content:"";position:absolute;width:16px;height:16px;left:3px;top:2px;background:var(--muted);border-radius:50%;transition:.2s}
 .switch input:checked + i{background:var(--accent2);border-color:var(--accent)}
 .switch input:checked + i:before{transform:translateX(19px);background:#fff}
 #msg{color:var(--ok)} #imp{color:#6ea8ff;white-space:pre-wrap}
 #cands div{margin:6px 0}
 #cands span{margin-right:12px;color:var(--muted)}
</style>
</head>
<body>
<button id="lang"></button>
<h2 data-i18n="h2Switch">切換</h2>
<div id="cards" data-i18n="loading" style="color:var(--muted)">載入中…</div>
<div class="bar"><span id="hint"></span><span class="grow"></span><button id="apToggle"></button><button id="refresh" data-i18n="btnRefresh">重新整理</button></div>
<hr style="border:none;border-top:1px solid var(--border);margin:22px 0">
<h2 data-i18n="h2Settings">設定</h2>

<fieldset><legend data-i18n="legA">供應商 A(共用帳號,永遠優先)</legend>
 <div><label data-i18n="lblName" data-i18n-title="tipName" title="顯示用的帳號名稱:切換卡片與系統匣會用到,可任意命名。">名稱</label><input type="text" id="aName"></div>
 <div><label data-i18n="lblKey" data-i18n-title="tipKey" title="z.ai 的 API 金鑰。身分判定以此為準:settings.json 裡的 key 屬於哪個帳號,就是靠比對這裡的 key。">API key</label><input type="password" id="aKey"><button type="button" id="aKeyEye" class="eye" data-i18n-title="eyeTip" title="按一下顯示/隱藏 API key。">👁</button></div>
 <div><label data-i18n="lblEndpoint" data-i18n-title="tipEndpoint" title="切換時寫入 settings.json 的 ANTHROPIC_BASE_URL。z.ai 兩個帳號的端點通常相同。">端點</label><input type="text" id="aURL"></div>
 <div><label data-i18n="lblModelEnv" data-i18n-title="tipModelEnv" title="額外寫入 env 的模型對應,JSON 格式(ANTHROPIC_DEFAULT_* 鍵值對)。API key 與端點不可放進這裡,匯入器會自動排除。">模型 env(JSON)</label><textarea id="aModel" rows="8"></textarea></div>
</fieldset>

<fieldset><legend data-i18n="legB">供應商 B(自購帳號,避難所)</legend>
 <div><label data-i18n="lblName" data-i18n-title="tipName" title="顯示用的帳號名稱:切換卡片與系統匣會用到,可任意命名。">名稱</label><input type="text" id="bName"></div>
 <div><label data-i18n="lblKey" data-i18n-title="tipKey" title="z.ai 的 API 金鑰。身分判定以此為準:settings.json 裡的 key 屬於哪個帳號,就是靠比對這裡的 key。">API key</label><input type="password" id="bKey"><button type="button" id="bKeyEye" class="eye" data-i18n-title="eyeTip" title="按一下顯示/隱藏 API key。">👁</button></div>
 <div><label data-i18n="lblEndpoint" data-i18n-title="tipEndpoint" title="切換時寫入 settings.json 的 ANTHROPIC_BASE_URL。z.ai 兩個帳號的端點通常相同。">端點</label><input type="text" id="bURL"></div>
 <div><label data-i18n="lblModelEnv" data-i18n-title="tipModelEnv" title="額外寫入 env 的模型對應,JSON 格式(ANTHROPIC_DEFAULT_* 鍵值對)。API key 與端點不可放進這裡,匯入器會自動排除。">模型 env(JSON)</label><textarea id="bModel" rows="8"></textarea></div>
</fieldset>

<fieldset><legend data-i18n="legThresholds">閾值(%)</legend>
 <div><label data-i18n="lblSw5" data-i18n-title="tipSw5" title="在 A 時:5 小時窗口用量 ≥ 此值就切到 B(預設 90=搶在撞 429 牆之前先撤)。">切換線 5h</label><input type="number" id="sw5" step="0.1"></div>
 <div><label data-i18n="lblSwW" data-i18n-title="tipSwW" title="在 A 時:週窗口用量 ≥ 此值同樣切到 B。">切換線 週</label><input type="number" id="swW" step="0.1"></div>
 <div><label data-i18n="lblRc5" data-i18n-title="tipRc5" title="在 B 時:A 的 5 小時用量降到 ≤ 此值才算恢復,自動切回 A。">恢復線 5h</label><input type="number" id="rc5" step="0.1"></div>
 <div><label data-i18n="lblRcW" data-i18n-title="tipRcW" title="在 B 時:A 的週窗口 ≤ 此值(且 5 小時也達標)才切回 A。恢復線遠低於切換線,避免臨界點來回抖動。">恢復線 週</label><input type="number" id="rcW" step="0.1"></div>
 <div><label data-i18n="lblWarn" data-i18n-title="tipWarn" title="目前帳號任一窗口跨過此值時彈一次預警通知(每窗口只報一次,不洗版)。">預警線</label><input type="number" id="warn" step="0.1"></div>
 <div><label data-i18n="lblStale" data-i18n-title="tipStale" title="額度資料超過此分鐘數視同過期:不做任何自動切換、只通知一次。查詢暫時失敗時沿用上次數字,老化超過此限才認定過期。">資料過期(分)</label><input type="number" id="stale" step="1"></div>
 <div><label data-i18n="lblParseFail" data-i18n-title="tipParseFail" title="連續 N 次查詢/解析失敗 → 紅色圖示+通知(z.ai 回應可能改版,該更新程式)。0=停用警報。">連續解析失敗警報次數</label><input type="number" id="pf" step="1"></div>
</fieldset>

<fieldset><legend data-i18n="legPolling">輪詢(秒)</legend>
 <div><label data-i18n="lblPoll" data-i18n-title="tipPoll" title="平時多久查詢一次兩個帳號的額度。">一般間隔</label><input type="number" id="poll" step="1"></div>
 <div><label data-i18n="lblFast" data-i18n-title="tipFast" title="目前帳號額度吃緊時,改用這個較快的輪詢間隔。">快輪詢間隔</label><input type="number" id="fast" step="1"></div>
 <div><label data-i18n="lblFastAbove" data-i18n-title="tipFastAbove" title="目前帳號任一窗口 > 此百分比就進入快輪詢(嚴格大於,剛好等於不算)。">快輪詢觸發(%)</label><input type="number" id="fastAbove" step="0.1"></div>
</fieldset>

<fieldset><legend data-i18n="legBudget">B 配速預算</legend>
 <div><label data-i18n="lblEnforce" data-i18n-title="tipEnforce" title="勾選:B 燒到當日天花板時強制切回 A(寧停等不超支),燒過線的 B 也不當避難所(A 過熱時留 A 等下次結算);不勾:B 的當日天花板只顯示與記帳,完全不影響自動切換。">天花板強制切回 A</label><span class="switch"><input type="checkbox" id="bEnforce" data-i18n-title="tipEnforce" title="勾選:B 燒到當日天花板時強制切回 A(寧停等不超支),燒過線的 B 也不當避難所(A 過熱時留 A 等下次結算);不勾:B 的當日天花板只顯示與記帳,完全不影響自動切換。"><i></i></span></div>
 <div><label data-i18n="lblWeeklyTotal" data-i18n-title="tipWeeklyTotal" title="B 的週額度護欄:即使天天燒到當日天花板,整週用量也只會收斂到這個值(預設 90,真牆 100% 前留餘裕)。">週預算總量(%)</label><input type="number" id="bWeekly" step="0.1"></div>
 <div><label data-i18n="lblDailyFloor" data-i18n-title="tipDailyFloor" title="每天至少分配這個額度百分比(剩餘天數平均算出來更少時用它墊底)。設 0=嚴格平均,不保底。">每日保底(%)</label><input type="number" id="bDaily" step="0.1"></div>
 <div><label data-i18n="lblWorkdays" data-i18n-title="tipWorkdays" title="勾選:剩餘天數只數工作日,週末不佔每日額度;不勾:每天額度=剩餘÷日曆天數。">只數工作日</label><span class="switch"><input type="checkbox" id="bWork" data-i18n-title="tipWorkdays" title="勾選:剩餘天數只數工作日,週末不佔每日額度;不勾:每天額度=剩餘÷日曆天數。"><i></i></span></div>
</fieldset>

<fieldset><legend data-i18n="legOther">其他</legend>
 <div><label data-i18n="lblQmPath" data-i18n-title="tipQmPath" title="切換時翻轉此檔的 mode: 行(RACING/SAVING),給其他 Claude session 讀取的搶速/省錢訊號。檔案不存在只跳過,不影響切換。">quota-mode.md 路徑</label><input type="text" id="qm"></div>
 <div><label data-i18n="lblProxy" data-i18n-title="tipProxy" title="查詢額度 API 用的 HTTP proxy 網址;留空=直連。">proxy(空=直連)</label><input type="text" id="proxy"></div>
 <div><label data-i18n="lblAuto" data-i18n-title="tipAuto" title="存檔時立即寫入/移除登錄檔 Run 機碼:開機自動啟動 cc-fuse。">開機自動啟動</label><span class="switch"><input type="checkbox" id="auto" data-i18n-title="tipAuto" title="存檔時立即寫入/移除登錄檔 Run 機碼:開機自動啟動 cc-fuse。"><i></i></span></div>
</fieldset>

<button id="save" class="primary" data-i18n="btnSave">存檔</button><span id="msg"></span>
<hr style="border:none;border-top:1px solid var(--border);margin:22px 0">
<div style="margin-bottom:8px"><label data-i18n="lblDbPath">cc-switch 資料庫路徑</label><input type="text" id="dbPath" value="~/.cc-switch/cc-switch.db"></div>
<button id="import" data-i18n="btnImport">從 cc-switch 匯入</button><span id="imp"></span>
<div id="cands"></div>

<script>
// ---- i18n(EN/zh)——字典一次載入,套用走 data-i18n / data-i18n-title ----
var I18N={
 zh:{
  title:"cc-fuse 設定",h2Switch:"切換",h2Settings:"設定",loading:"載入中…",btnRefresh:"重新整理",
  legA:"供應商 A(共用帳號,永遠優先)",legB:"供應商 B(自購帳號,避難所)",legThresholds:"閾值(%)",
  legPolling:"輪詢(秒)",legBudget:"B 配速預算",legOther:"其他",
  lblName:"名稱",lblKey:"API key",lblEndpoint:"端點",lblModelEnv:"模型 env(JSON)",
  lblSw5:"切換線 5h",lblSwW:"切換線 週",lblRc5:"恢復線 5h",lblRcW:"恢復線 週",lblWarn:"預警線",
  lblStale:"資料過期(分)",lblParseFail:"連續解析失敗警報次數",
  lblPoll:"一般間隔",lblFast:"快輪詢間隔",lblFastAbove:"快輪詢觸發(%)",
  lblEnforce:"天花板強制切回 A",lblWeeklyTotal:"週預算總量(%)",lblDailyFloor:"每日保底(%)",lblWorkdays:"只數工作日",
  lblQmPath:"quota-mode.md 路徑",lblProxy:"proxy(空=直連)",lblAuto:"開機自動啟動",lblDbPath:"cc-switch 資料庫路徑",
  btnSave:"存檔",btnImport:"從 cc-switch 匯入",
  tipName:"顯示用的帳號名稱:切換卡片與系統匣會用到,可任意命名。",
  tipKey:"z.ai 的 API 金鑰。身分判定以此為準:settings.json 裡的 key 屬於哪個帳號,就是靠比對這裡的 key。",
  tipEndpoint:"切換時寫入 settings.json 的 ANTHROPIC_BASE_URL。z.ai 兩個帳號的端點通常相同。",
  tipModelEnv:"額外寫入 env 的模型對應,JSON 格式(ANTHROPIC_DEFAULT_* 鍵值對)。API key 與端點不可放進這裡,匯入器會自動排除。",
  tipSw5:"在 A 時:5 小時窗口用量 ≥ 此值就切到 B(預設 90=搶在撞 429 牆之前先撤)。",
  tipSwW:"在 A 時:週窗口用量 ≥ 此值同樣切到 B。",
  tipRc5:"在 B 時:A 的 5 小時用量降到 ≤ 此值才算恢復,自動切回 A。",
  tipRcW:"在 B 時:A 的週窗口 ≤ 此值(且 5 小時也達標)才切回 A。恢復線遠低於切換線,避免臨界點來回抖動。",
  tipWarn:"目前帳號任一窗口跨過此值時彈一次預警通知(每窗口只報一次,不洗版)。",
  tipStale:"額度資料超過此分鐘數視同過期:不做任何自動切換、只通知一次。查詢暫時失敗時沿用上次數字,老化超過此限才認定過期。",
  tipParseFail:"連續 N 次查詢/解析失敗 → 紅色圖示+通知(z.ai 回應可能改版,該更新程式)。0=停用警報。",
  tipPoll:"平時多久查詢一次兩個帳號的額度。",
  tipFast:"目前帳號額度吃緊時,改用這個較快的輪詢間隔。",
  tipFastAbove:"目前帳號任一窗口 > 此百分比就進入快輪詢(嚴格大於,剛好等於不算)。",
  tipEnforce:"勾選:B 燒到當日天花板時強制切回 A(寧停等不超支),燒過線的 B 也不當避難所(A 過熱時留 A 等下次結算);不勾:B 的當日天花板只顯示與記帳,完全不影響自動切換。",
  tipWeeklyTotal:"B 的週額度護欄:即使天天燒到當日天花板,整週用量也只會收斂到這個值(預設 90,真牆 100% 前留餘裕)。",
  tipDailyFloor:"每天至少分配這個額度百分比(剩餘天數平均算出來更少時用它墊底)。設 0=嚴格平均,不保底。",
  tipWorkdays:"勾選:剩餘天數只數工作日,週末不佔每日額度;不勾:每天額度=剩餘÷日曆天數。",
  tipQmPath:"切換時翻轉此檔的 mode: 行(RACING/SAVING),給其他 Claude session 讀取的搶速/省錢訊號。檔案不存在只跳過,不影響切換。",
  tipProxy:"查詢額度 API 用的 HTTP proxy 網址;留空=直連。",
  tipAuto:"存檔時立即寫入/移除登錄檔 Run 機碼:開機自動啟動 cc-fuse。",
  active:"使用中",switchBtn:"切換",switchTip:"手動切換(P1 最優先,立即生效)",
  ceilingChip:"今日天花板 週",enforceNote:"到點強制切回 A",noEnforceNote:"不強制",recoveryChip:"恢復預計 ",
  hintAutoOn:"自動切換:開",hintAutoOff:"自動切換:已暫停",hintStale:"額度資料過期",
  hintKeyDead:"目前帳號 key 失效(401/403)",hintParse:"連續解析失敗(紅燈)",
  apPause:"暫停自動",apResume:"恢復自動",apPauseTip:"暫停自動切換(手動切換仍可用)",apResumeTip:"恢復自動切換",
  saving:"存檔中…",saved:"已存檔",saveFailed:"失敗",
  importing:"匯入中…",importFailed:"匯入失敗:",importFoundA:"找到 ",importFoundB:" 個 Claude 供應商——點「設為 A / 設為 B」自動填滿四欄,完成後按存檔。",
  filledPrefix:"已填入 ",filledSuffix:"——確認後按存檔",setAsA:"設為 A",setAsB:"設為 B",
  unnamed:"(未命名)",shortKey:"(短 key)",noKey:"(無 key)",
  eyeTip:"按一下顯示/隱藏 API key。"
 },
 en:{
  title:"cc-fuse Settings",h2Switch:"Switching",h2Settings:"Settings",loading:"Loading…",btnRefresh:"Refresh",
  legA:"Provider A (shared account, always preferred)",legB:"Provider B (personal account, refuge)",legThresholds:"Thresholds (%)",
  legPolling:"Polling (seconds)",legBudget:"B pacing budget",legOther:"Other",
  lblName:"Name",lblKey:"API key",lblEndpoint:"Endpoint",lblModelEnv:"Model env (JSON)",
  lblSw5:"Switch line 5h",lblSwW:"Switch line weekly",lblRc5:"Recovery line 5h",lblRcW:"Recovery line weekly",lblWarn:"Warn line",
  lblStale:"Stale after (min)",lblParseFail:"Parse-fail alert count",
  lblPoll:"Poll interval",lblFast:"Fast interval",lblFastAbove:"Fast trigger (%)",
  lblEnforce:"Ceiling forces back to A",lblWeeklyTotal:"Weekly budget total (%)",lblDailyFloor:"Daily floor (%)",lblWorkdays:"Workdays only",
  lblQmPath:"quota-mode.md path",lblProxy:"Proxy (empty = direct)",lblAuto:"Launch at logon",lblDbPath:"cc-switch database path",
  btnSave:"Save",btnImport:"Import from cc-switch",
  tipName:"Display name for the account — shown on the switcher cards and in the tray. Free-form.",
  tipKey:"The z.ai API key. This is the identity fingerprint: which account settings.json belongs to is decided by matching this key.",
  tipEndpoint:"ANTHROPIC_BASE_URL written into settings.json on switch. Both z.ai accounts normally share the same endpoint.",
  tipModelEnv:"Extra model mapping written into env as JSON (ANTHROPIC_DEFAULT_* key-value pairs). API keys and endpoints are not allowed here — the importer strips them automatically.",
  tipSw5:"On A: when the 5-hour window usage ≥ this value, switch to B (default 90 = retreat before hitting the 429 wall).",
  tipSwW:"On A: when the weekly window usage ≥ this value, switch to B as well.",
  tipRc5:"On B: A counts as recovered when its 5-hour usage drops to ≤ this value; switch back automatically.",
  tipRcW:"On B: switch back when A's weekly window ≤ this value (and the 5-hour condition holds too). Recovery lines sit far below the switch lines to prevent flapping around the threshold.",
  tipWarn:"When either window of the current account crosses this value, raise one warning toast (once per window — no spam).",
  tipStale:"Quota data older than this many minutes counts as stale: no automatic switching, one notification only. A failed query keeps the last numbers; they age out past this limit.",
  tipParseFail:"N consecutive query/parse failures → red icon + toast (z.ai's response may have changed — update the app). 0 disables the alert.",
  tipPoll:"How often to poll both accounts' quotas normally.",
  tipFast:"Faster polling interval used while the current account's quota runs hot.",
  tipFastAbove:"Fast polling kicks in when any window of the current account is above this percentage (strictly greater).",
  tipEnforce:"On: when B burns through today's ceiling, force the switch back to A (stop and wait rather than overspend), and an over-ceiling B loses refuge status (stay on A until the next settlement). Off: B's ceiling is display + accounting only — it never affects automatic switching.",
  tipWeeklyTotal:"B's weekly guardrail: even if B burns to its daily ceiling every day, the whole week converges to this value (default 90 — headroom before the hard 100% wall).",
  tipDailyFloor:"The minimum percentage allocated to each day (when the remaining-days average comes out lower). 0 = strict average, no floor.",
  tipWorkdays:"On: remaining days count workdays only — weekends consume no daily allowance. Off: daily allowance = remaining ÷ calendar days.",
  tipQmPath:"On every switch, flip this file's mode: line (RACING/SAVING) — the race/save signal other Claude sessions read. A missing file is skipped without affecting the switch.",
  tipProxy:"HTTP proxy URL for the quota API; empty = direct connection.",
  tipAuto:"On save, writes/removes the registry Run key that launches cc-fuse at logon.",
  active:"Active",switchBtn:"Switch",switchTip:"Manual switch (P1 top priority, immediate)",
  ceilingChip:"Today's ceiling wk ",enforceNote:"forces switch back to A",noEnforceNote:"info only",recoveryChip:"Recovery est. ",
  hintAutoOn:"Autopilot: on",hintAutoOff:"Autopilot: paused",hintStale:"quota data stale",
  hintKeyDead:"current key dead (401/403)",hintParse:"sustained parse failures",
  apPause:"Pause autopilot",apResume:"Resume autopilot",apPauseTip:"Pause automatic switching (manual switching still works)",apResumeTip:"Resume automatic switching",
  saving:"Saving…",saved:"Saved",saveFailed:"Failed",
  importing:"Importing…",importFailed:"Import failed: ",importFoundA:"Found ",importFoundB:" Claude providers — click Set as A / Set as B to fill both profiles, then press Save.",
  filledPrefix:"Filled ",filledSuffix:" — review and press Save",setAsA:"Set as A",setAsB:"Set as B",
  unnamed:"(unnamed)",shortKey:"(short key)",noKey:"(no key)",
  eyeTip:"Click to show/hide the API key."
 }
};
var lang="en";
try{var saved0=localStorage.getItem("ccfuseLang");lang=saved0||((((navigator.language||"")+"").toLowerCase().indexOf("zh")===0)?"zh":"en")}catch(e){lang=((((navigator.language||"")+"").toLowerCase().indexOf("zh")===0))?"zh":"en"}
function t(k){var d=I18N[lang]||I18N.en;return d[k]!==undefined?d[k]:(I18N.en[k]!==undefined?I18N.en[k]:k)}
function applyLang(){
 var els=document.querySelectorAll("[data-i18n]");
 for(var i=0;i<els.length;i++){els[i].textContent=t(els[i].getAttribute("data-i18n"))}
 var els2=document.querySelectorAll("[data-i18n-title]");
 for(var j=0;j<els2.length;j++){els2[j].title=t(els2[j].getAttribute("data-i18n-title"))}
 document.title=t("title");
 document.getElementById("lang").textContent=(lang==="zh")?"English":"中文";
 document.documentElement.lang=(lang==="zh")?"zh-Hant":"en";
}
function setLang(l){lang=l;try{localStorage.setItem("ccfuseLang",l)}catch(e){}applyLang();fetchStatus()}

// ---- 小工具 ----
function toModel(s){try{var m=JSON.parse(s||"{}");return (m&&typeof m==="object")?m:{}}catch(e){return {}}}
function val(id){return document.getElementById(id).value}
function val2(id,v){document.getElementById(id).value=(v===undefined||v===null)?"":v}
function num(id){return parseFloat(val(id)||"0")}
function fill(p,pre){
 document.getElementById(pre+"Name").value=p.name||"";
 document.getElementById(pre+"Key").value=p.apiKey||"";
 document.getElementById(pre+"URL").value=p.baseUrl||"";
 document.getElementById(pre+"Model").value=(p.modelEnv&&Object.keys(p.modelEnv).length)?JSON.stringify(p.modelEnv,null,2):"";
}
function initials(n){n=(n||"").trim();if(!n)return"?";var w=n.split(/\s+/);var s=w.length>1?(w[0][0]+w[1][0]):n.slice(0,2);return s.toUpperCase()}
function fmtCD(ms){if(ms==null||ms<=0)return"";var m=Math.round(ms/60000);if(m<1)return"<1m";if(m<60)return m+"m";var h=Math.floor(m/60);if(h<48)return h+"h"+(m%60?(""+(m%60)+"m"):"");var d=Math.floor(h/24);return d+"d"+(h%24)+"h"}
function pctCls(v){if(v==null)return"";return v>=90?"bad":(v>=70?"warn":"good")}
function api(method,url,body,cb){
 var x=new XMLHttpRequest();
 x.open(method,url,true);
 x.setRequestHeader("Content-Type","application/json");
 x.onload=function(){cb(x.status,x.responseText)};
 x.onerror=function(){cb(0,"connection failed")};
 x.send(body?JSON.stringify(body):null);
}
// API key 遮罩(R7):預設只顯示點點,按眼睛按鈕才顯示原值。
function bindEye(inputId,btnId){
 var b=document.getElementById(btnId);
 b.onclick=function(){
  var e=document.getElementById(inputId);
  e.type=(e.type==="password")?"text":"password";
 };
}

// ---- 切換卡片(資料全來自 /api/status;外部字串一律 DOM textContent 建) ----
function quotaBar(label,pct,reset){
 var row=document.createElement("div");row.className="qrow";
 var lab=document.createElement("span");lab.className="qlabel";lab.textContent=label;row.appendChild(lab);
 var track=document.createElement("div");track.className="qtrack";
 var fillEl=document.createElement("div");
 var v=(pct==null?0:Math.max(0,Math.min(100,pct)));
 fillEl.style.width=v+"%";
 var cls=pctCls(pct);
 fillEl.className=cls?("qfill "+cls):"qfill";
 track.appendChild(fillEl);row.appendChild(track);
 var valEl=document.createElement("span");valEl.className="qval"+(cls?" "+cls:"");
 valEl.textContent=(pct==null?"—":pct+"%");row.appendChild(valEl);
 if(reset){var c=document.createElement("span");c.className="cd";var ms=Date.parse(reset)-Date.now();if(ms>0)c.textContent="⏱"+fmtCD(ms);row.appendChild(c)}
 return row;
}
function buildCard(slot,p,snap,s){
 var row=document.createElement("div");
 row.className="card"+(s.current===slot?" active":"");
 var av=document.createElement("div");av.className="av";av.textContent=p?initials(p.name):"?";row.appendChild(av);
 var meta=document.createElement("div");meta.className="meta";
 var nm=document.createElement("div");nm.className="name";
 nm.textContent=(p&&p.name)?p.name:t("unnamed");
 var tag=document.createElement("span");tag.className="slot";tag.textContent=slot;
 nm.appendChild(tag);
 meta.appendChild(nm);
 var ur=document.createElement("div");ur.className="url";ur.textContent=(p&&p.baseUrl)?p.baseUrl:"";
 meta.appendChild(ur);
 meta.appendChild(quotaBar("5h",snap?snap.fiveHourPct:null,snap?snap.fiveHourReset:null));
 meta.appendChild(quotaBar("7d",snap?snap.weeklyPct:null,snap?snap.weeklyReset:null));
 // B 卡:今日天花板(R1)——開=到點強制切回 A、關=不強制;未結算過則整行不建。
 if(slot==="B"&&s.ceilingToday!=null){
  var note=s.enforceDailyCeiling?t("enforceNote"):t("noEnforceNote");
  var chip=document.createElement("span");chip.className="chip";
  chip.textContent=t("ceilingChip")+s.ceilingToday.toFixed(1)+"%("+note+")";
  meta.appendChild(chip);
 }
 // A 卡:恢復預計(R3,只看 5h 重置;週窗口也超標時此估計偏樂觀,使用者已知悉)。
 if(slot==="A"&&snap&&snap.fiveHourReset){
  var hh=new Date(snap.fiveHourReset),ms=hh-Date.now();
  var chip2=document.createElement("span");chip2.className="chip";
  chip2.textContent=t("recoveryChip")+hh.toLocaleTimeString("zh-TW",{hour:"2-digit",minute:"2-digit",hour12:false})+(ms>0?"(⏱"+fmtCD(ms)+")":"");
  meta.appendChild(chip2);
 }
 row.appendChild(meta);
 var btn=document.createElement("button");
 if(s.current===slot){btn.className="primary";btn.disabled=true;btn.textContent=t("active")}
 else{btn.textContent=t("switchBtn");btn.title=t("switchTip");btn.onclick=function(){api("POST","/api/switch",{target:slot},function(){fetchStatus()})}}
 row.appendChild(btn);
 return row;
}
function renderStatus(s){
 var ps={};(s.profiles||[]).forEach(function(p){ps[p.id]=p});
 var box=document.getElementById("cards");box.innerHTML="";
 ["A","B"].forEach(function(slot){
  box.appendChild(buildCard(slot,ps[slot]||null,slot==="A"?s.a:s.b,s));
 });
 var h=[];
 h.push(s.autopilot?t("hintAutoOn"):t("hintAutoOff"));
 if(s.stale)h.push(t("hintStale"));
 if(s.keyDead)h.push(t("hintKeyDead"));
 if(s.parseFailing)h.push(t("hintParse"));
 document.getElementById("hint").textContent=h.join(" · ");
 // 自動切換開關(R5):與系統匣共用同一旗標,點完立即重抓。
 var ap=document.getElementById("apToggle");
 ap.textContent=s.autopilot?t("apPause"):t("apResume");
 ap.title=s.autopilot?t("apPauseTip"):t("apResumeTip");
 ap.onclick=function(){api("POST","/api/autopilot",{on:!s.autopilot},function(){fetchStatus()})};
}
function fetchStatus(){
 api("GET","/api/status",null,function(st,body){
  if(st===200){try{renderStatus(JSON.parse(body))}catch(e){}}
 });
}
document.getElementById("lang").onclick=function(){setLang(lang==="zh"?"en":"zh")};
document.getElementById("refresh").onclick=fetchStatus;
bindEye("aKey","aKeyEye");
bindEye("bKey","bKeyEye");
applyLang();
fetchStatus();
setInterval(fetchStatus,5000);

// ---- 設定表單 ----
function readProfile(pre,id){
 return {id:id,name:val(pre+"Name"),apiKey:val(pre+"Key"),baseUrl:val(pre+"URL"),modelEnv:toModel(val(pre+"Model"))};
}
function load(c){
 var a=(c.profiles&&c.profiles[0])||{},b=(c.profiles&&c.profiles[1])||{};
 fill(a,"a");fill(b,"b");
 val2("sw5",c.switchThreshold5h);val2("swW",c.switchThresholdWeekly);
 val2("rc5",c.recoveryThreshold5h);val2("rcW",c.recoveryThresholdWeekly);
 val2("warn",c.warnThreshold);val2("stale",c.staleAfterMin);val2("pf",c.parseFailAlertLimit);
 val2("poll",c.pollIntervalSec);val2("fast",c.fastPollSec);val2("fastAbove",c.fastPollAbovePct);
 val2("bWeekly",c.budget?c.budget.weeklyTotalPct:0);val2("bDaily",c.budget?c.budget.minDailyAllowancePct:0);
 document.getElementById("bEnforce").checked=!!(c.budget&&c.budget.enforceDailyCeiling);
 document.getElementById("bWork").checked=!!(c.budget&&c.budget.workdaysOnly);
 val2("qm",c.quotaModeFile);val2("proxy",c.proxyUrl);
 document.getElementById("auto").checked=!!c.autoStart;
}
function collect(){
 return {
  profiles:[readProfile("a","A"),readProfile("b","B")],
  switchThreshold5h:num("sw5"),switchThresholdWeekly:num("swW"),
  recoveryThreshold5h:num("rc5"),recoveryThresholdWeekly:num("rcW"),
  warnThreshold:num("warn"),staleAfterMin:parseInt(val("stale")||"0",10),
  parseFailAlertLimit:parseInt(val("pf")||"0",10),
  pollIntervalSec:parseInt(val("poll")||"0",10),fastPollSec:parseInt(val("fast")||"0",10),
  fastPollAbovePct:num("fastAbove"),
  budget:{weeklyTotalPct:num("bWeekly"),minDailyAllowancePct:num("bDaily"),workdaysOnly:document.getElementById("bWork").checked,enforceDailyCeiling:document.getElementById("bEnforce").checked},
  quotaModeFile:val("qm"),autoStart:document.getElementById("auto").checked,
  proxyUrl:val("proxy")
 };
}
document.getElementById("save").onclick=function(){
 document.getElementById("msg").textContent=t("saving");
 api("POST","/api/config",collect(),function(st,body){
  document.getElementById("msg").textContent=st===200?t("saved"):(t("saveFailed")+"("+st+"):"+body);
  if(st===200)fetchStatus(); // 存檔當下立即重抓(R4:天花板即時反映,不等 5 秒自刷)
 });
};

// ---- cc-switch 匯入:候選清單 + 「設為 A / 設為 B」一鍵填滿四欄 ----
function maskKey(k){k=k||"";return k.length>8?k.slice(0,4)+"…"+k.slice(-4):(k?t("shortKey"):t("noKey"))}
function renderCands(ps){
 var box=document.getElementById("cands");
 ps.forEach(function(p){
  var row=document.createElement("div");
  var lab=document.createElement("span");
  lab.textContent=(p.name||t("unnamed"))+"  ·  "+maskKey(p.apiKey);
  row.appendChild(lab);
  ["A","B"].forEach(function(slot){
   var b=document.createElement("button");
   b.textContent=t(slot==="A"?"setAsA":"setAsB");
   b.onclick=function(){
    fill(p,slot.toLowerCase());
    document.getElementById("msg").textContent=t("filledPrefix")+slot+":"+(p.name||t("unnamed"))+t("filledSuffix");
   };
   row.appendChild(b);
  });
  box.appendChild(row);
 });
}
document.getElementById("import").onclick=function(){
 document.getElementById("imp").textContent=t("importing");
 document.getElementById("cands").innerHTML="";
 api("POST","/api/import",{dbPath:val("dbPath")},function(st,body){
  if(st!==200){document.getElementById("imp").textContent=t("importFailed")+body;return}
  var ps=(JSON.parse(body).profiles)||[];
  document.getElementById("imp").textContent=t("importFoundA")+ps.length+t("importFoundB");
  renderCands(ps);
 });
};
api("GET","/api/config",null,function(st,body){
 if(st===200){try{load(JSON.parse(body))}catch(e){}}
});
</script>
</body>
</html>`
