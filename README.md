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

## 本轮修复

| 修复 | 说明 |
|------|------|
| WSVerify 启用 | 维护模式拦截 + session 自动登录，不再无验证放行 |
| CORS Headers | `strings.Join` 单字符串 → `Headers...` 正确传入多个头 |
| 正则预编译 | 账号验证 `regexp.MatchString` → `regexp.MustCompile` |
| 高延时日志 | 不再打印 body 内容，只打印长度，防止泄露敏感数据 |
| Sockets.Start 双调 | 移除重复的 `EventTypStarted` 注册 |
| 死代码清理 | `HttpContent.uri`、`HttpServer.redis`、注释代码块 |
| cosweb API 适配 | `allow.Handle` → `allow.Middleware`，Static 中间件重构 |
| 依赖升级 | cosgo v1.8.0, cosnet v1.4.2, cosrpc v1.4.1, cosweb v1.4.1, coswss v0.4.0 |

## 目录结构

```
gateway/
├── module.go         模块生命周期（Init/Start/Reload/Close）
├── gate_http.go      HTTP 短连接服务 + OAuth + 代理
├── gate_tcp.go       TCP 长连接服务 + 认证 + 重连
├── gate_wss.go       WebSocket 握手验证 + 连接建立
├── proxy.go          统一代理转发（路由→鉴权→RPC→响应）
├── access.go         权限验证（None/OAuth/Player）
├── context.go        Proxy 接口 + Context 构造
├── service.go        消息推送服务（send/write/broadcast）
├── cookies.go        RPC 响应元数据 → session 更新
├── setting.go        全局配置（路由/序列化/认证回调）
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
