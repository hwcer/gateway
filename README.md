# GATE 网关服务

GATE 是一个基于 Go 语言实现的多协议网关服务，支持 HTTP 短连接、TCP 长连接和 WebSocket 连接，主要用于游戏服务器或需要实时通信的应用场景。

## 功能特性

- **多协议支持**：同时支持 HTTP、TCP 和 WebSocket 协议
- **会话管理**：完善的会话管理和断线重连机制
- **消息推送**：支持单点推送、全服广播和频道广播
- **服务发现**：支持本地服务、进程内调用和基于 Redis 的服务发现
- **负载均衡**：使用用户级别的微服务筛选器，确保请求路由到正确的服务实例
- **安全认证**：支持 token 验证和权限控制
- **跨域支持**：内置跨域处理中间件
- **性能监控**：记录高延时请求，便于性能优化

## 技术栈

- **Go 1.24.0**：主要开发语言
- **cosgo**：基础框架
- **cosweb**：HTTP 服务框架
- **cosnet**：TCP 网络框架
- **coswss**：WebSocket 服务框架
- **cosrpc**：RPC 服务框架
- **logger**：日志框架

## 项目结构

```
gateway/
├── channel/          # 频道系统，用于消息广播
├── errors/           # 错误定义
├── example/          # 示例代码
├── gwcfg/            # 网关配置
├── players/          # 会话管理
├── rpcx/             # RPC 服务
├── token/            # 认证系统
├── gate_http.go      # HTTP 服务实现
├── gate_tcp.go       # TCP 服务实现
├── gate_wss.go       # WebSocket 服务实现
├── proxy.go          # 代理转发实现
├── service.go        # 服务实现
├── config.toml       # 配置文件
└── README.md         # 项目说明
```

## 快速开始

### 安装依赖

```bash
go mod tidy
```

### 配置文件

编辑 `config.toml` 文件，配置网关服务：

```toml
# 程序中所有参数都可以在这里配置并覆盖默认配置
# 参数列表可以在命令行模式下使用 -h 参数启动查看
debug=true
appid="gate"              # 服务器 APPID, MASTAR 中创建的游戏 ID
developer="123456"        # 开发者密钥，用于 GM 命令
#pid="pid"                # 生产环境创建 pid 目录，并打开这个
logs.level=0               # 日志等级
#logs.path=""              # 日志路径

[rpcx]
#redis="127.0.0.1:6379?db=1&password=123456"  # Redis 地址，用于服务发现
address="127.0.0.1:8100"  # RPC 服务地址，一般使用默认值，使用内网 IP

#################以下配置按服务器取舍#######################
[gate]
address=":8000"            # 网关服务地址
protocol=7                 # 协议类型：1-websocket，2-长连接，4-短链接，可组合使用
static.root="wwwroot"      # 静态文件根目录
static.route="ui"          # 静态文件路由
static.index="index.html"  # 静态文件索引页

# 网关转发规则，默认使用服务发现
# local: 使用本地服务
# process: 进程内调用
# discovery: 服务器发现，必须配置 rpcx.redis
[service]
game="local"               # 游戏服务
locator="local"            # 定位服务
```

### 运行示例

```bash
cd example
go run main.go
```

### 集成到现有项目

```go
package main

import (
    "fmt"
    "gateway"

    "github.com/hwcer/cosgo"
)

func main() {
    cosgo.SetBanner(banner)
    cosgo.Use(gateway.New())
    cosgo.Start(true)
}

func banner() {
    str := "\n大威天龙，大罗法咒，般若诸佛，般若巴嘛空。\n"
    fmt.Printf(str)
}
```

## API 说明

### HTTP 接口

- **POST /oauth**：认证接口，用于获取会话密钥
- **POST /* **：代理接口，用于转发请求到后端服务

### TCP 命令

- **ping**：心跳命令，返回当前时间戳
- **oauth**：认证命令，用于登录
- **C2SReconnect**：重连命令，用于断线重连

### WebSocket 连接

WebSocket 连接支持两种认证方式：
1. 通过 `Sec-WebSocket-Protocol` 头传递 token
2. 通过 URL query 参数传递 token（`access`、`guid`、`secret`）

## 配置项说明

### 全局配置

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| debug | bool | false | 是否开启调试模式 |
| appid | string | "gate" | 服务器 APPID |
| developer | string | "" | 开发者密钥 |
| logs.level | int | 0 | 日志等级 |
| logs.path | string | "" | 日志路径 |

### RPCX 配置

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| redis | string | "" | Redis 地址，用于服务发现 |
| address | string | "127.0.0.1:8100" | RPC 服务地址 |

### GATE 配置

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| address | string | ":8000" | 网关服务地址 |
| protocol | int | 7 | 协议类型：1-websocket，2-长连接，4-短链接 |
| static.root | string | "" | 静态文件根目录 |
| static.route | string | "" | 静态文件路由 |
| static.index | string | "" | 静态文件索引页 |

### SERVICE 配置

| 配置项 | 类型 | 默认值 | 说明 |
|--------|------|--------|------|
| game | string | "local" | 游戏服务 |
| locator | string | "local" | 定位服务 |

## 开发指南

### 添加新的协议处理

1. 在 `gateway` 目录下创建新的协议处理文件，如 `gate_xxx.go`
2. 实现协议处理结构体和方法
3. 在 `init` 函数中注册协议处理

### 添加新的服务

1. 在 `service.go` 文件中使用 `Register` 函数注册新服务
2. 实现服务方法，方法接收 `*cosrpc.Context` 参数

### 消息推送

- **单点推送**：使用 `send` 服务，通过 `ServiceMetadataUID` 或 `ServiceMetadataGUID` 指定用户
- **全服广播**：使用 `broadcast` 服务
- **频道广播**：使用 `channel/Broadcast` 方法

## 性能优化

1. **使用连接池**：对于数据库和 Redis 连接，使用连接池减少连接建立开销
2. **减少内存分配**：使用对象池和缓冲区池，减少 GC 压力
3. **优化路由**：合理设计路由规则，减少路由查找时间
4. **使用异步处理**：对于耗时操作，使用异步处理，避免阻塞主线程
5. **监控高延时请求**：根据日志中的高延时请求，针对性优化

## 安全建议

1. **使用 HTTPS**：在生产环境中，使用 HTTPS 加密传输
2. **加强认证**：使用强密码和 token 验证，定期更换密钥
3. **限制请求频率**：添加请求频率限制，防止 DoS 攻击
4. **过滤输入**：对用户输入进行严格过滤，防止注入攻击
5. **定期更新依赖**：定期更新依赖库，修复安全漏洞

## 许可证

MIT License

## 联系方式

如有问题或建议，欢迎联系我们。

