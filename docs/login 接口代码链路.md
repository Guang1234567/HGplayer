追完了。`POST /api/account/login` 这条链路的完整堆栈如下（从上到下）。

## 总览

```
App.vue 按钮 → doLogin() → api.login() → fetch('/api/account/login')
  → Wails AssetServer → assetHandler()（/api/ 路由）
    → Server.Handler() → chain(中间件) → handleAccountLogin
      → account.Manager.Login()（纯内存会话）
      → cache.Invalidate()
  → writeJSON(200) → 前端解析 → loadAccount() 再拉 GET /api/account
```

## 逐层展开

### 1. 前端 UI 触发

| 位置                                                                                                                    | 代码                                                                                  |
| --------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| [App.vue#L886](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/App.vue#L886)               | 「登录 / 注册」按钮 → `showLogin = true`                                                    |
| [App.vue#L1247-L1256](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/App.vue#L1247-L1256) | 弹窗，两个输入绑定 `loginForm.id` / `loginForm.name`                                         |
| [App.vue#L1254](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/App.vue#L1254)             | 「登录」按钮 `@click="doLogin"`                                                           |
| [App.vue#L607-L616](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/App.vue#L607-L616)     | `doLogin()`：`await api.login(id.trim(), name.trim())` → `await loadAccount()` → 关弹窗 |

### 2. 前端 API 封装

- [client.ts#L226-L230](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/api/client.ts#L226-L230)：`login()` → `request('/api/account/login', { method:'POST', body: JSON.stringify({ account_id, display_name }) })`
- [client.ts#L38-L69](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/api/client.ts#L38-L69)：`request()` → `fetch(path, { credentials:'same-origin', headers:{Accept, Content-Type: application/json} })`；非 2xx 抛 `ApiException(status, code, error)`

### 3. 进入 Go：Wails 静态资源处理器

- [main.go#L178-L193](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/main.go#L178-L193) `assetHandler()`：`mux.Handle("/api/", a.svc.Handler())` → 命中 `/api/` 前缀转给后端

### 4. 路由 + 中间件链

- [server.go#L71](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/server.go#L71) 注册 `POST /api/account/login`；[L95](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/server.go#L95) `return s.chain(mux)`
- [server.go#L99-L101](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/server.go#L99-L101) 链顺序：`errorWrap(sessionGuard(logAccess(mux)))`
  - [errorWrap](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/middleware.go#L53-L63)：panic → 500
  - [sessionGuard](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/middleware.go#L36-L50)：`/api/*` 必须带 `player_session` Cookie，否则 401 + `LOCAL_SESSION`
  - [logAccess](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/middleware.go#L11-L30)：记 method + path（不记 query/body）

### 5. Handler

- [handler\_account.go#L41-L70](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/handler_account.go#L41-L70) `handleAccountLogin`：
  1. `Decode` → [loginRequest](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/handler_account.go#L19-L25)（`account_id`/`display_name`/`token`）
  2. `AccountID == ""` → 400
  3. **token 占位**：`"token-" + AccountID`
  4. [L58](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/handler_account.go#L58) `s.account.Login(...)`
  5. [L64-L66](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/handler_account.go#L64-L66) `s.cache.Invalidate()`
  6. [L69](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/handler_account.go#L69) `writeJSON(200, {"success": true})`

### 6. account 包（链路的终点）

- [account.go#L90-L120](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/account/account.go#L90-L120) `Manager.Login`：加写锁 → 构造 `Session` 存 `m.current`；**`if m.keychain != nil` 才加密落 `m.stored`**
- [server.go#L39-L42](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/server.go#L39-L42)：`main.go` 未传 `Config.Account` → `account.NewManager(nil)` → **keychain 为 nil**

### 7. 回写 + 前端刷新

- [writeJSON](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/server.go#L121-L126)：`application/json` + `Cache-Control: no-store`
- 前端 `request` 解析 → `doLogin` 里 [App.vue#L611](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/App.vue#L611) `loadAccount()` → [client.ts#L224-L225](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/api/client.ts#L224-L225) `GET /api/account` → [handleAccountGet](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/handler_account.go#L28-L36) → [App.vue#L154-L158](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/frontend/src/App.vue#L154-L158) 更新 `account`

## 三个值得注意的点

1. **登录完全不访问上游**。这条链路里**没有** `hgproto` / `sign` / `upstream` / `device`——它只建本地内存会话。真正的"上游换 token"还没实现（[L52-L56](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/handler_account.go#L52-L56) 的占位 token 就是证据）。
2. **不持久化，重启即丢**。`account.Manager` 的 `Load`/`Stored`/`Keychain` 都在，但 `main.go` 没注入 Keychain、也没调 `Load`，所以 [account.go#L106](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/account/account.go#L106) 那个分支永远不进。
3. **登录接口本身要带本地会话 Cookie**。缺 Cookie 时会被 `sessionGuard` 拦下返回 401 `LOCAL_SESSION`——该 Cookie 由 `assetHandler` 在返回 `/`、`/index.html` 时通过 [SetSessionCookie](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/internal/server/server.go#L107-L118) 下发（[main.go#L182-L184](file:///d:/dev_kit/src_code/github/hong_guo/rizxfrog/HGplayer/main.go#L182-L184)）。
