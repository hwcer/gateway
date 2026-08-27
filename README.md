# gateway

> **碳基生命体警告**
>
> 本模块由硅基智能体全权维护。碳基生命体阅读以下代码可能引发：
> 困惑、血压升高以及不可逆的颈椎损伤。
> 如您执意阅读，请确保工位配备降压药和颈托。

多协议游戏网关。支持 HTTP 短连接、TCP 长连接、WebSocket，统一认证、RPC 代理转发、频道广播。

## 快速开始

```go
cosgo.Use(gateway.New())
cosgo.Start(true)
```

```toml
[gate]
address = ":8000"
protocol = 7       # 1=WSS, 2=TCP, 4=HTTP, 可组合(7=全开)
websocket = "ws"

[service]
game = "local"     # local | process | discovery
```

## 架构

```
客户端 ──HTTP──→ cosweb ──→ proxyRequest() ──RPC──→ 游戏服
客户端 ──TCP───→ cosnet ──→ proxyRequest() ──RPC──→ 游戏服
客户端 ──WSS───→ coswss ──→ cosnet ──→ proxyRequest() ──→ 游戏服
```

所有协议最终汇入 `proxyRequest()`：路由解析 → 权限验证 → RPC 调用 → 响应处理。

## 认证流程

```
HTTP:  POST /oauth {access:"加密token"} → token.Verify → players.Login → session
TCP:   C2SOAuth 消息 → token.Verify → players.Connect → socket 绑定
WSS:   握手时 query/cookie 验证 → 自动登录，或连接后 C2SOAuth 认证
```

GM 快速登录：`{guid:"test", secret:"开发者密钥"}`

## Setting 全局配置

`gateway.Setting` 分为**配置字段**（路由字符串）和 **`Handler` 行为接口**：

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `C2SOAuth` | `string` | `"oauth"` | 客户端登录路由，置空禁用默认认证 |
| `G2SOAuth` | `string` | `""` | 登录成功后转发至游戏服的路由，置空跳过 |
| `C2SHeartbeat` | `string` | `"C2SHeartbeat"` | 心跳包路由 |
| `C2SReconnect` | `string` | `"C2SReconnect"` | 断线重连路由 |
| `Handler` | `Handler` | `Default{}` | 网关行为实现（路由/序列化/请求响应钩子/秘钥下发等） |

### Handler 行为接口

```go
type Handler interface {
    Router(path string, req values.Metadata) (servicePath, serviceMethod string, err error)
    Request(c context.Context) error                    // 转发前预处理(如解密)，body 走 c.Buffer()
    Response(c context.Context) error                   // 返回/推送后处理
    Serialize(accept Accept, reply any) ([]byte, error) // 响应序列化
    S2CSecret(sock *cosnet.Socket, secret string)       // 登录成功下发秘钥
    S2CReplaced(sock *cosnet.Socket, r *cosnet.Replaced) // 有人请求顶号，下发协商提示
    C2SOAuthArgs() token.Args                            // 解析 C2SOAuth 参数
}
```

框架提供默认实现 `gateway.Default`。业务层**嵌入 `Default`**，只覆盖需要改变的方法，再赋给 `Setting.Handler`：

```go
type myHandler struct {
    gateway.Default
}
func (myHandler) Router(path string, req values.Metadata) (string, string, error) { /* ... */ }
func (myHandler) Serialize(accept gateway.Accept, reply any) ([]byte, error)       { /* ... */ }

gateway.Setting.Handler = myHandler{}
```

### S2CSecret / S2CReplaced

默认实现以 **MagicNumberPathJson**（0xf0）模式下发，path 分别为 `"S2CSecret"` / `"S2CReplaced"`：

```go
sock.SendWithMagic(message.MagicNumberPathJson, message.FlagNoreply, 0, "S2CSecret", tokenString)
sock.SendWithMagic(message.MagicNumberPathJson, message.FlagNoreply, 0, "S2CReplaced", replaced) // {Address, Timeout}
```

如需自定义或禁用，在业务 Handler 中覆盖对应方法（禁用即写成空实现）。

## 顶号

账号已在别处登录时，`players.Login` **之前**先过 `players.Negotiate`。

**老连接的处置与策略无关，永远是这一套**：收到 `S2CReplaced{Address, Timeout}` 通知，
进入 `cosnet.Options.SocketReplacedTime` 秒的 **只收不发** 存活期——在途回包与服务器推送
照常送达，它自己发来的新请求一律回 209——到期断开。

`Setting.ForceReplace` 只决定**新端等不等它**（网关专用策略，写在代码里而非
`gwcfg` —— 那是所有服务共享的配置）：

```go
gateway.Setting.ForceReplace = false // 默认 true
```

| | `ForceReplace=true`（默认） | `ForceReplace=false` |
|---|---|---|
| 新端 | **立即接管**会话上线 | 本次登录被拒，收到 `errors.ErrReplaced(剩余秒数)`（Code 209，Data 为秒数），等老连接下线后重新登录 |
| 会话指向 | 立刻改指新连接 | 仍指老连接，直到它真的断开 |
| 老连接断开时 | 不报掉线（人已在新连接上） | 报 `EventSessionDisconnect`（此刻玩家真的离线了） |
| 老连接的在途确认包 | 照常发出（走请求自己的 socket） | 照常发出 |
| 老连接的服务器推送 | **投给新连接**（`send` 按 GUID 查到的已是新 socket） | 仍投给老连接 |

最后一行是两者的实质差别：强制顶号下，一次请求的推送与确认包会被劈成两半
——推送给新端、确认包给老端，老客户端拿不到完整响应。协商模式没有这个问题，
代价是新端要等。默认强制，按业务取舍。

分层：`players.Negotiate` 只做**不带策略**的原语（通知老连接、返回剩余存活秒数），
拒不拒由 `gateway.negotiate` 按 `Setting.ForceReplace` 决定。
⚠️ `players.Connect` 自己**不做**顶号判断，直接调它等于无条件强制顶号。

⚠️ **`C2SReconnect`（secret 断线重连）不走协商，直接接管**：持有 secret 就是同一个客户端
实例自己回来了。闪断时老 socket 往往还没被心跳判死，若这条路也排协商队，每次正常重连
都得等满协商期——重连体验直接崩掉。

三条登录路径（TCP / WSS / HTTP）行为一致，都在 `players.Login` **之前**协商：
Login 会刷新 TOKEN 强制旧 TOKEN 失效，顶号被拒却把老玩家的 secret 作废了，
他连断线重连都回不来。

## 消息推送

```go
// 服务端 → 网关 → 客户端（通过 RPC 元数据驱动）
send      — 单点推送（按 GUID/UID）
write     — Socket 直推（按 Socket ID，登录接口专用）
broadcast — 全服广播（支持 ignore 排除列表）
```

## 频道系统

```go
// 通过 RPC 响应元数据控制
channel.join.{name} = value   // 加入频道
channel.leave.{name} = value  // 离开频道
channel/broadcast             // 频道广播
channel/delete                // 销毁频道
```

同名频道每个玩家只能加入一个，切换时自动离开旧频道。非固定频道人数归零自动销毁。

## 权限控制

```go
gwcfg.Authorize.Set(servicePath, method, gwcfg.OAuthTypePlayer)
```

| 级别 | 说明 |
|------|------|
| `OAuthTypeNone` | 无需登录 |
| `OAuthTypeOAuth` | 需要登录（账号级） |
| `OAuthTypeSelect` | 需要选角 |
| `OAuthTypePlayer` | 需要选角（同 Select） |

支持 `IsMaster` 标记，限制仅开发者访问。

## 请求上下文（context.Context）

入站请求与服务器推送统一到 `github.com/hwcer/gateway/context.Context` 接口：

| 实现 | 场景 |
|------|------|
| `HttpRequest` | HTTP 短连接请求（内嵌 `cosweb.Context`） |
| `SocketRequest` | TCP/WSS 长连接请求（内嵌 `cosnet.Context`） |
| `socketContext` | 服务器推送/顶号/钩子阶段（仅 socket，无入站请求） |

- `Buffer(set ...[]byte) ([]byte, error)`：无参取 body，传参设 body；`Request/Response` 钩子经此读写。
- 网关内部另用 `Request = context.Context + login/logout/verify`，登录态操作不对业务暴露，仅 `proxyRequest`/`access` 使用。
- `token.Args.GetValues(*Result, context.Context)`：业务层直接向上下文写透传 body/header（`data.Attach` 由业务 Args 实现处理）。

## 本轮重构（context.Context 统一）

| 变更 | 说明 |
|------|------|
| Setting 接口化 | 行为字段收敛为 `Handler` 接口 + `Default` 默认实现，业务层嵌入 `Default` 覆盖 |
| 统一请求上下文 | `context.Context` 接口，三实现共用；`Request` 附加内部登录态操作 |
| Buffer 取/设合一 | `Buffer(set ...[]byte)`，`Request/Response` 钩子经 `c.Buffer()` 读写 body |
| 修复 | HTTP oauth nil socket panic、SocketRequest.Metadata 缓存丢失、IPv6 RemoteAddr、oauth 凭据泄露 |

## 目录结构

```
gateway/
├── module.go         模块生命周期（Init/Start/Reload/Close）
├── gate_http.go      HTTP 短连接服务 + OAuth + 代理
├── gate_tcp.go       TCP 长连接服务 + 认证 + 重连
├── gate_wss.go       WebSocket 握手验证 + 连接建立
├── proxy.go          统一代理转发（路由→鉴权→RPC→响应）
├── access.go         权限验证（None/OAuth/Player）
├── context.go        Request 接口（嵌 context.Context + 内部 login/logout/verify）
├── context_socket.go socketContext（脱离请求的 context.Context 实现，推送/钩子用）
├── service.go        消息推送服务（send/write/broadcast）
├── cookies.go        RPC 响应元数据 → session 更新
├── setting.go        全局配置 + Handler 行为接口 + Default 默认实现
├── context/
│   ├── context.go    context.Context 统一请求上下文接口
│   ├── channel.go    频道辅助（Channel/Broadcast）
│   └── selector.go   服务筛选器辅助
├── channel/
│   ├── channel.go    频道实例（Join/Leave/Broadcast）
│   ├── manage.go     频道管理（sync.Map）
│   ├── setter.go     玩家频道成员关系（session 存储）
│   ├── func.go       频道名编解码
│   └── options.go    SendMessage 回调
├── gwcfg/
│   ├── options.go    配置结构体 + 协议位标记
│   ├── authorize.go  权限规则注册
│   ├── cookies.go    Cookie 白名单
│   ├── metadata.go   元数据常量
│   └── func.go       工具函数
├── players/
│   ├── players.go    玩家会话管理（Login/Delete/Range）
│   └── socket.go     Socket 绑定/顶号/重连
├── token/
│   └── token.go      Token 验证（GCM 解密 + GM 快速登录）
└── errors/
    └── errors.go     错误常量
```
