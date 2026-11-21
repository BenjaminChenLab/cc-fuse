# 需求文件:設定窗介面語言持久化(uilang)

- 日期:2026-11-19
- 狀態:settled(使用者 mid-turn 提出:「中文 英文 choice should be persisted as well」)
- 單元名:uilang-persist

## 背景(成因,已驗證)

設定窗的伺服器每次開窗綁一個**臨時埠**(127.0.0.1:隨機埠,`cmd/cc-fuse/settings.go`);
網頁 localStorage 以 origin(含埠號)為儲存鑰 → 埠一換,上次存的語言選擇永遠讀不到,
每次開窗都回到瀏覽器預設語言。語言選擇原本只存 localStorage(`page.go` 的
`ccfuseLang` 鍵),因此實際上從未跨視窗存活。

## 定案

- **D-L1 語言選擇改存 `config.json`**:新欄位 `uilang`(json `uilang`),
  值 `""`(預設,跟隨瀏覽器語言)/ `"en"` / `"zh"`;舊設定檔缺欄 → 零值 = 跟隨瀏覽器(相容)。
- **D-L2 新端點 `POST /api/uilang`**:body `{"lang":"en|zh|"}`;合法值 → 經 `onSave`
  立即落盤 config.json(不用按主存檔)+ 更新 live cell;非法值/壞 JSON → 400 且不動
  現狀(與 postConfig 同慣例);非 POST → 405。
- **D-L3 頁面行為**:`#lang` 按鈕切換 → 仍寫 localStorage(同窗即時渲染)+ 套用 +
  **POST /api/uilang 落盤**;頁面載入的 `GET /api/config` 回來後,若 `uilang` 為
  `"en"`/`"zh"` 且與目前顯示不同 → 糾正語言(server 值為準)。
- **D-L4 主存檔不得洗掉語言**:`collect()` 必須帶 `uilang`(目前顯示語言)——
  `POST /api/config` 反序列化進零值 struct,漏帶即每次存檔清空語言
  (同 `enforceDailyCeiling` 的歷史教訓,白箱標記測試釘死)。

## 驗收語意

- **A-L1**:`POST /api/uilang {"lang":"zh"}` → 200、live cell `UILang=="zh"`、
  `onSave` 收到 `"zh"`;`{"lang":"en"}` 同理;`{"lang":"xx"}` → 400 且 cell/onSave 不動;
  壞 JSON → 400 且不動。
- **A-L2**:`GET /api/config` 帶 `uilang` 現值(頁面據以糾正)。
- **A-L3**:頁面原始碼含標記:`api/uilang`(按鈕落盤)、`c.uilang`(載入糾正)、
  `uilang:lang`(collect 攜帶)— 靜態頁子串級斷言(既有慣例;chip 組合順序同級
  限制已於先前審查揭露)。

## 已接受的殘留

- 載入糾正發生在 `/api/config` 回應抵達後:若瀏覽器預設語言 ≠ 持久化語言,
  開窗瞬間會有一次語言切換的閃爍(可接受,不預載 server 值 — 靜態頁在設定讀回前
  無從得知)。
- localStorage 仍寫(同窗即時渲染用);跨窗它本來就失效,不影響正確性。
