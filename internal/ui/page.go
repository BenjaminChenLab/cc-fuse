package ui

// indexHTML is the settings window page served at / (design §8: 內嵌本機頁).
// Dark refined theme (2026-11-17): design tokens, card layout with quota
// progress bars, toggle switches, focus rings — modern-product look while
// every dynamic string is still built via DOM textContent (never innerHTML).
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
<h2>切換</h2>
<div id="cards" style="color:var(--muted)">載入中…</div>
<div class="bar"><span id="hint"></span><span class="grow"></span><button id="apToggle"></button><button id="refresh">重新整理</button></div>
<hr style="border:none;border-top:1px solid var(--border);margin:22px 0">
<h2>設定</h2>

<fieldset><legend>供應商 A(共用帳號,永遠優先)</legend>
 <div><label title="顯示用的帳號名稱:切換卡片與系統匣會用到,可任意命名。">名稱</label><input type="text" id="aName" title="顯示用的帳號名稱:切換卡片與系統匣會用到,可任意命名。"></div>
 <div><label title="z.ai 的 API 金鑰。身分判定以此為準:settings.json 裡的 key 屬於哪個帳號,就是靠比對這裡的 key。">API key</label><input type="text" id="aKey" title="z.ai 的 API 金鑰。身分判定以此為準:settings.json 裡的 key 屬於哪個帳號,就是靠比對這裡的 key。"></div>
 <div><label title="切換時寫入 settings.json 的 ANTHROPIC_BASE_URL。z.ai 兩個帳號的端點通常相同。">端點</label><input type="text" id="aURL" title="切換時寫入 settings.json 的 ANTHROPIC_BASE_URL。z.ai 兩個帳號的端點通常相同。"></div>
 <div><label title="額外寫入 env 的模型對應,JSON 格式(ANTHROPIC_DEFAULT_* 鍵值對)。API key 與端點不可放進這裡,匯入器會自動排除。">模型 env(JSON)</label><textarea id="aModel" rows="8" title="額外寫入 env 的模型對應,JSON 格式(ANTHROPIC_DEFAULT_* 鍵值對)。API key 與端點不可放進這裡,匯入器會自動排除。"></textarea></div>
</fieldset>

<fieldset><legend>供應商 B(自購帳號,避難所)</legend>
 <div><label title="顯示用的帳號名稱:切換卡片與系統匣會用到,可任意命名。">名稱</label><input type="text" id="bName" title="顯示用的帳號名稱:切換卡片與系統匣會用到,可任意命名。"></div>
 <div><label title="z.ai 的 API 金鑰。身分判定以此為準:settings.json 裡的 key 屬於哪個帳號,就是靠比對這裡的 key。">API key</label><input type="text" id="bKey" title="z.ai 的 API 金鑰。身分判定以此為準:settings.json 裡的 key 屬於哪個帳號,就是靠比對這裡的 key。"></div>
 <div><label title="切換時寫入 settings.json 的 ANTHROPIC_BASE_URL。z.ai 兩個帳號的端點通常相同。">端點</label><input type="text" id="bURL" title="切換時寫入 settings.json 的 ANTHROPIC_BASE_URL。z.ai 兩個帳號的端點通常相同。"></div>
 <div><label title="額外寫入 env 的模型對應,JSON 格式(ANTHROPIC_DEFAULT_* 鍵值對)。API key 與端點不可放進這裡,匯入器會自動排除。">模型 env(JSON)</label><textarea id="bModel" rows="8" title="額外寫入 env 的模型對應,JSON 格式(ANTHROPIC_DEFAULT_* 鍵值對)。API key 與端點不可放進這裡,匯入器會自動排除。"></textarea></div>
</fieldset>

<fieldset><legend>閾值(%)</legend>
 <div><label title="在 A 時:5 小時窗口用量 ≥ 此值就切到 B(預設 90=搶在撞 429 牆之前先撤)。">切換線 5h</label><input type="number" id="sw5" step="0.1" title="在 A 時:5 小時窗口用量 ≥ 此值就切到 B(預設 90=搶在撞 429 牆之前先撤)。"></div>
 <div><label title="在 A 時:週窗口用量 ≥ 此值同樣切到 B。">切換線 週</label><input type="number" id="swW" step="0.1" title="在 A 時:週窗口用量 ≥ 此值同樣切到 B。"></div>
 <div><label title="在 B 時:A 的 5 小時用量降到 ≤ 此值才算恢復,自動切回 A。">恢復線 5h</label><input type="number" id="rc5" step="0.1" title="在 B 時:A 的 5 小時用量降到 ≤ 此值才算恢復,自動切回 A。"></div>
 <div><label title="在 B 時:A 的週窗口 ≤ 此值(且 5 小時也達標)才切回 A。恢復線遠低於切換線,避免臨界點來回抖動。">恢復線 週</label><input type="number" id="rcW" step="0.1" title="在 B 時:A 的週窗口 ≤ 此值(且 5 小時也達標)才切回 A。恢復線遠低於切換線,避免臨界點來回抖動。"></div>
 <div><label title="目前帳號任一窗口跨過此值時彈一次預警通知(每窗口只報一次,不洗版)。">預警線</label><input type="number" id="warn" step="0.1" title="目前帳號任一窗口跨過此值時彈一次預警通知(每窗口只報一次,不洗版)。"></div>
 <div><label title="額度資料超過此分鐘數視同過期:不做任何自動切換、只通知一次。查詢暫時失敗時沿用上次數字,老化超過此限才認定過期。">資料過期(分)</label><input type="number" id="stale" step="1" title="額度資料超過此分鐘數視同過期:不做任何自動切換、只通知一次。查詢暫時失敗時沿用上次數字,老化超過此限才認定過期。"></div>
 <div><label title="連續 N 次查詢/解析失敗 → 紅色圖示+通知(z.ai 回應可能改版,該更新程式)。0=停用警報。">連續解析失敗警報次數</label><input type="number" id="pf" step="1" title="連續 N 次查詢/解析失敗 → 紅色圖示+通知(z.ai 回應可能改版,該更新程式)。0=停用警報。"></div>
</fieldset>

<fieldset><legend>輪詢(秒)</legend>
 <div><label title="平時多久查詢一次兩個帳號的額度。">一般間隔</label><input type="number" id="poll" step="1" title="平時多久查詢一次兩個帳號的額度。"></div>
 <div><label title="目前帳號額度吃緊時,改用這個較快的輪詢間隔。">快輪詢間隔</label><input type="number" id="fast" step="1" title="目前帳號額度吃緊時,改用這個較快的輪詢間隔。"></div>
 <div><label title="目前帳號任一窗口 > 此百分比就進入快輪詢(嚴格大於,剛好等於不算)。">快輪詢觸發(%)</label><input type="number" id="fastAbove" step="0.1" title="目前帳號任一窗口 > 此百分比就進入快輪詢(嚴格大於,剛好等於不算)。"></div>
</fieldset>

<fieldset><legend>B 配速預算</legend>
 <div><label title="勾選:B 燒到當日天花板時強制切回 A(寧停等不超支),燒過線的 B 也不當避難所(A 過熱時留 A 等下次結算);不勾:B 的當日天花板只顯示與記帳,完全不影響自動切換。">天花板強制切回 A</label><span class="switch"><input type="checkbox" id="bEnforce" title="勾選:B 燒到當日天花板時強制切回 A(寧停等不超支),燒過線的 B 也不當避難所(A 過熱時留 A 等下次結算);不勾:B 的當日天花板只顯示與記帳,完全不影響自動切換。"><i></i></span></div>
 <div><label title="B 的週額度護欄:即使天天燒到當日天花板,整週用量也只會收斂到這個值(預設 90,真牆 100% 前留餘裕)。">週預算總量(%)</label><input type="number" id="bWeekly" step="0.1" title="B 的週額度護欄:即使天天燒到當日天花板,整週用量也只會收斂到這個值(預設 90,真牆 100% 前留餘裕)。"></div>
 <div><label title="每天至少分配這個額度百分比(剩餘天數平均算出來更少時用它墊底)。設 0=嚴格平均,不保底。">每日保底(%)</label><input type="number" id="bDaily" step="0.1" title="每天至少分配這個額度百分比(剩餘天數平均算出來更少時用它墊底)。設 0=嚴格平均,不保底。"></div>
 <div><label title="勾選:剩餘天數只數工作日,週末不佔每日額度;不勾:每天額度=剩餘÷日曆天數。">只數工作日</label><span class="switch"><input type="checkbox" id="bWork" title="勾選:剩餘天數只數工作日,週末不佔每日額度;不勾:每天額度=剩餘÷日曆天數。"><i></i></span></div>
</fieldset>

<fieldset><legend>其他</legend>
 <div><label title="切換時翻轉此檔的 mode: 行(RACING/SAVING),給其他 Claude session 讀取的搶速/省錢訊號。檔案不存在只跳過,不影響切換。">quota-mode.md 路徑</label><input type="text" id="qm" title="切換時翻轉此檔的 mode: 行(RACING/SAVING),給其他 Claude session 讀取的搶速/省錢訊號。檔案不存在只跳過,不影響切換。"></div>
 <div><label title="查詢額度 API 用的 HTTP proxy 網址;留空=直連。">proxy(空=直連)</label><input type="text" id="proxy" title="查詢額度 API 用的 HTTP proxy 網址;留空=直連。"></div>
 <div><label title="存檔時立即寫入/移除登錄檔 Run 機碼:開機自動啟動 cc-fuse。">開機自動啟動</label><span class="switch"><input type="checkbox" id="auto" title="存檔時立即寫入/移除登錄檔 Run 機碼:開機自動啟動 cc-fuse。"><i></i></span></div>
</fieldset>

<button id="save" class="primary">存檔</button><span id="msg"></span>
<hr style="border:none;border-top:1px solid var(--border);margin:22px 0">
<div style="margin-bottom:8px"><label>cc-switch 資料庫路徑</label><input type="text" id="dbPath" value="~/.cc-switch/cc-switch.db"></div>
<button id="import">從 cc-switch 匯入</button><span id="imp"></span>
<div id="cands"></div>

<script>
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
 x.onerror=function(){cb(0,"連線失敗")};
 x.send(body?JSON.stringify(body):null);
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
 nm.textContent=(p&&p.name)?p.name:"(未設定)";
 var tag=document.createElement("span");tag.className="slot";tag.textContent=slot;
 nm.appendChild(tag);
 meta.appendChild(nm);
 var ur=document.createElement("div");ur.className="url";ur.textContent=(p&&p.baseUrl)?p.baseUrl:"";
 meta.appendChild(ur);
 meta.appendChild(quotaBar("5h",snap?snap.fiveHourPct:null,snap?snap.fiveHourReset:null));
 meta.appendChild(quotaBar("7d",snap?snap.weeklyPct:null,snap?snap.weeklyReset:null));
 // B 卡:今日天花板(R1)——開=到點強制切回 A、關=不強制;未結算過則整行不建。
 if(slot==="B"&&s.ceilingToday!=null){
  var note=s.enforceDailyCeiling?"到點強制切回 A":"不強制";
  var chip=document.createElement("span");chip.className="chip";
  chip.textContent="今日天花板 週"+s.ceilingToday.toFixed(1)+"%("+note+")";
  meta.appendChild(chip);
 }
 // A 卡:恢復預計(R3,只看 5h 重置;週窗口也超標時此估計偏樂觀,使用者已知悉)。
 if(slot==="A"&&snap&&snap.fiveHourReset){
  var hh=new Date(snap.fiveHourReset),ms=hh-Date.now();
  var chip2=document.createElement("span");chip2.className="chip";
  chip2.textContent="恢復預計 "+hh.toLocaleTimeString("zh-TW",{hour:"2-digit",minute:"2-digit",hour12:false})+(ms>0?"(⏱"+fmtCD(ms)+")":"");
  meta.appendChild(chip2);
 }
 row.appendChild(meta);
 var btn=document.createElement("button");
 if(s.current===slot){btn.className="primary";btn.disabled=true;btn.textContent="使用中"}
 else{btn.textContent="切換";btn.title="手動切換(P1 最優先,立即生效)";btn.onclick=function(){api("POST","/api/switch",{target:slot},function(){fetchStatus()})}}
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
 h.push(s.autopilot?"自動切換:開":"自動切換:已暫停");
 if(s.stale)h.push("額度資料過期");
 if(s.keyDead)h.push("目前帳號 key 失效(401/403)");
 if(s.parseFailing)h.push("連續解析失敗(紅燈)");
 document.getElementById("hint").textContent=h.join(" · ");
 // 自動切換開關(R5):與系統匣共用同一旗標,點完立即重抓。
 var ap=document.getElementById("apToggle");
 ap.textContent=s.autopilot?"暫停自動":"恢復自動";
 ap.title=s.autopilot?"暫停自動切換(手動切換仍可用)":"恢復自動切換";
 ap.onclick=function(){api("POST","/api/autopilot",{on:!s.autopilot},function(){fetchStatus()})};
}
function fetchStatus(){
 api("GET","/api/status",null,function(st,body){
  if(st===200){try{renderStatus(JSON.parse(body))}catch(e){}}
 });
}
document.getElementById("refresh").onclick=fetchStatus;
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
 document.getElementById("msg").textContent="存檔中…";
 api("POST","/api/config",collect(),function(st,body){
  document.getElementById("msg").textContent=st===200?"已存檔":("失敗("+st+"):"+body);
  if(st===200)fetchStatus(); // 存檔當下立即重抓(R4:天花板即時反映,不等 5 秒自刷)
 });
};

// ---- cc-switch 匯入:候選清單 + 「設為 A / 設為 B」一鍵填滿四欄 ----
function maskKey(k){k=k||"";return k.length>8?k.slice(0,4)+"…"+k.slice(-4):(k?"(短 key)":"(無 key)")}
function renderCands(ps){
 var box=document.getElementById("cands");
 ps.forEach(function(p){
  var row=document.createElement("div");
  var lab=document.createElement("span");
  lab.textContent=(p.name||"(未命名)")+"  ·  "+maskKey(p.apiKey);
  row.appendChild(lab);
  ["A","B"].forEach(function(slot){
   var b=document.createElement("button");
   b.textContent="設為 "+slot;
   b.onclick=function(){
    fill(p,slot.toLowerCase());
    document.getElementById("msg").textContent="已填入 "+slot+":"+(p.name||"(未命名)")+"——確認後按存檔";
   };
   row.appendChild(b);
  });
  box.appendChild(row);
 });
}
document.getElementById("import").onclick=function(){
 document.getElementById("imp").textContent="匯入中…";
 document.getElementById("cands").innerHTML="";
 api("POST","/api/import",{dbPath:val("dbPath")},function(st,body){
  if(st!==200){document.getElementById("imp").textContent="匯入失敗:"+body;return}
  var ps=(JSON.parse(body).profiles)||[];
  document.getElementById("imp").textContent="找到 "+ps.length+" 個 Claude 供應商——點「設為 A / 設為 B」自動填滿四欄,完成後按存檔。";
  renderCands(ps);
 });
};
api("GET","/api/config",null,function(st,body){
 if(st===200){try{load(JSON.parse(body))}catch(e){}}
});
</script>
</body>
</html>`
