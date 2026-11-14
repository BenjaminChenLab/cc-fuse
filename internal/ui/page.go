package ui

// indexHTML is the settings window page served at / (design §8: 內嵌本機頁).
// Plain HTML + JS, no external assets. Top half = switcher cards (cc-switch
// style: name/URL/5h/7d quota + reset countdown, active card highlighted,
// one-click switch); bottom half = every configurable number (CLAUDE.md:
// 所有數字一律可設定). The quota numbers come from GET /api/status and
// refresh every 5s; switching posts /api/switch (P1 manual, immediate).
// Candidate names/keys come from the cc-switch database (external data), so
// every dynamic string is built via DOM textContent — never innerHTML.
const indexHTML = `<!doctype html>
<html lang="zh-Hant">
<head>
<meta charset="utf-8">
<title>cc-fuse 設定</title>
<style>
 body{background:#101012;color:#e4e4e7;font-family:"Microsoft JhengHei",sans-serif;font-size:14px;margin:16px;max-width:720px}
 h2{color:#e4e4e7;user-select:none}
 fieldset{border:1px solid #3f3f46;border-radius:8px;margin-bottom:12px}
 legend{color:#a1a1aa;padding:0 6px}
 label{display:inline-block;min-width:150px;color:#d4d4d8}
 input{background:#1c1c1f;color:#e4e4e7;border:1px solid #3f3f46;border-radius:6px;padding:4px 8px}
 input[type=text]{width:340px}
 input[type=number]{width:90px}
 button{padding:6px 16px;border-radius:8px;border:1px solid #3f3f46;background:#26262a;color:#e4e4e7;cursor:pointer}
 button.primary{background:#2563eb;border-color:#2563eb;color:#fff;font-weight:bold}
 button:disabled{opacity:.6;cursor:default}
 .card{display:flex;align-items:center;gap:12px;border:1px solid #3f3f46;border-radius:12px;padding:12px 16px;margin-bottom:10px;background:#18181b}
 .card.active{border-color:#2563eb}
 .av{width:44px;height:44px;border-radius:50%;background:#26262a;display:flex;align-items:center;justify-content:center;font-weight:bold;color:#a1a1aa;flex:none}
 .meta{flex:1;min-width:0}
 .name{font-size:16px;font-weight:bold}
 .url{color:#7dd3fc;font-size:12px;word-break:break-all;margin:2px 0}
 .quota{display:flex;gap:16px;color:#a1a1aa;font-size:13px}
 .quota b{font-weight:bold}
 .slot{font-size:11px;color:#a1a1aa;border:1px solid #3f3f46;border-radius:4px;padding:0 5px;margin-left:8px;vertical-align:middle;font-weight:normal}
 .good{color:#4ade80}.warn{color:#fb923c}.bad{color:#f87171}
 .cd{color:#71717a;font-size:11px}
 .bar{margin:8px 0 16px;color:#a1a1aa}
 #msg{margin-left:8px;color:#4ade80}
 #imp{margin-left:8px;color:#7dd3fc;white-space:pre-wrap}
 #cands div{margin:4px 0}
 #cands span{margin-right:10px}
</style>
</head>
<body>
<h2>切換</h2>
<div id="cards"><div class="quota">載入中…</div></div>
<div class="bar"><span id="hint"></span> <button id="refresh" style="margin-left:12px">重新整理</button></div>
<hr>
<h2>設定</h2>

<fieldset><legend>供應商 A(共用帳號,永遠優先)</legend>
 <div><label>名稱</label><input type="text" id="aName"></div>
 <div><label>API key</label><input type="text" id="aKey"></div>
 <div><label>端點</label><input type="text" id="aURL"></div>
 <div><label>模型 env(JSON)</label><input type="text" id="aModel"></div>
</fieldset>

<fieldset><legend>供應商 B(自購帳號,避難所)</legend>
 <div><label>名稱</label><input type="text" id="bName"></div>
 <div><label>API key</label><input type="text" id="bKey"></div>
 <div><label>端點</label><input type="text" id="bURL"></div>
 <div><label>模型 env(JSON)</label><input type="text" id="bModel"></div>
</fieldset>

<fieldset><legend>閾值(%)</legend>
 <div><label>切換線 5h</label><input type="number" id="sw5" step="0.1"></div>
 <div><label>切換線 週</label><input type="number" id="swW" step="0.1"></div>
 <div><label>恢復線 5h</label><input type="number" id="rc5" step="0.1"></div>
 <div><label>恢復線 週</label><input type="number" id="rcW" step="0.1"></div>
 <div><label>預警線</label><input type="number" id="warn" step="0.1"></div>
 <div><label>資料過期(分)</label><input type="number" id="stale" step="1"></div>
 <div><label>連續解析失敗警報次數</label><input type="number" id="pf" step="1"></div>
</fieldset>

<fieldset><legend>輪詢(秒)</legend>
 <div><label>一般間隔</label><input type="number" id="poll" step="1"></div>
 <div><label>快輪詢間隔</label><input type="number" id="fast" step="1"></div>
 <div><label>快輪詢觸發(%)</label><input type="number" id="fastAbove" step="0.1"></div>
</fieldset>

<fieldset><legend>B 配速預算</legend>
 <div><label>週預算總量(%)</label><input type="number" id="bWeekly" step="0.1"></div>
 <div><label>每日保底(%)</label><input type="number" id="bDaily" step="0.1"></div>
 <div><label>只數工作日</label><input type="checkbox" id="bWork"></div>
</fieldset>

<fieldset><legend>其他</legend>
 <div><label>quota-mode.md 路徑</label><input type="text" id="qm"></div>
 <div><label>proxy(空=直連)</label><input type="text" id="proxy"></div>
 <div><label>開機自動啟動</label><input type="checkbox" id="auto"></div>
</fieldset>

<button id="save">存檔</button><span id="msg"></span>
<hr>
<div><label>cc-switch 資料庫路徑</label><input type="text" id="dbPath" value="~/.cc-switch/cc-switch.db"></div>
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
 document.getElementById(pre+"Model").value=p.modelEnv?JSON.stringify(p.modelEnv):"";
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
function quotaCell(label,pct,reset){
 var d=document.createElement("span");
 var b=document.createElement("b");
 b.textContent=(pct==null?"—":pct+"%");
 b.className=pctCls(pct);
 d.appendChild(document.createTextNode(label+" "));
 d.appendChild(b);
 if(reset){var c=document.createElement("span");c.className="cd";var ms=Date.parse(reset)-Date.now();if(ms>0)c.textContent=" ⏱"+fmtCD(ms);d.appendChild(c)}
 return d;
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
 var q=document.createElement("div");q.className="quota";
 q.appendChild(quotaCell("5h",snap?snap.fiveHourPct:null,snap?snap.fiveHourReset:null));
 q.appendChild(quotaCell("7d",snap?snap.weeklyPct:null,snap?snap.weeklyReset:null));
 meta.appendChild(q);
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
  budget:{weeklyTotalPct:num("bWeekly"),minDailyAllowancePct:num("bDaily"),workdaysOnly:document.getElementById("bWork").checked},
  quotaModeFile:val("qm"),autoStart:document.getElementById("auto").checked,
  proxyUrl:val("proxy")
 };
}
document.getElementById("save").onclick=function(){
 document.getElementById("msg").textContent="存檔中…";
 api("POST","/api/config",collect(),function(st,body){
  document.getElementById("msg").textContent=st===200?"已存檔":("失敗("+st+"):"+body);
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
