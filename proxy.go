package gateway

import (
	"fmt"
	"strconv"
	"time"

	"github.com/hwcer/cosnet/message"
	"github.com/hwcer/cosrpc/selector"
	"github.com/hwcer/gateway/context"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"

	"github.com/hwcer/cosgo/registry"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosrpc"
	"github.com/hwcer/cosrpc/client"
	"github.com/hwcer/logger"
)

// ElapsedMillisecond 高延时请求阈值
// 当请求处理时间超过此值时，会记录告警日志
var ElapsedMillisecond = 500 * time.Millisecond

// Request 内部转发时使用：在 context.Context（统一请求上下文）之上附加登录态内部操作，
// 仅 Forward/access 使用；业务层只接触 context.Context。
type Request interface {
	context.Context
	login(guid string, value values.Values) (string, error) //通过业务服激活登录信息（gateway 内部）
	logout() error                                          //退出登录（gateway 内部）
	verify() (*session.Data, error)                         //验证登录信息（gateway 内部）
}

// Forward 把一次请求转发到后端服务并取回回包，HTTP / TCP / WebSocket 共用这一条路径。
//
// 它完整走一遍：路由解析 → 权限校验 → 下发 metadata（网关地址 / socketId / 用户级服务定向）
// → Request 钩子 → RPC 调用 → 登录/登出处理 → cookies 更新 → Response 钩子。
//
// **业务层可以直接调它把消息投递到后端**，不必手搓 metadata：
//
//	ctx := &gateway.SocketRequest{Context: c}
//	reply, err := gateway.Forward(ctx, "/game/heartbeat")
//
// ⚠️ 自己拼 metadata 去 client.CallWithMetadata 是很容易出错的路子：权限档、网关地址、
// 由消息 magic 推导的 Accept/Content-Type、用户级服务定向地址，漏一个的症状都很隐蔽
// （回包编码不对、被判顶号、多区服投错服）。走这里就都对了。
//
// 参数:
//   - proxy: 请求上下文，用框架提供的 SocketRequest / HttpRequest 构造
//   - path:  转发目标路径，交给 Setting.Handler.Router 解析成 servicePath/serviceMethod
//
// 返回值:
//   - reply: 后端返回的数据（已过 Response 钩子）
//   - err:   路由/鉴权/调用过程中的错误
func Forward(proxy Request, path string) (reply []byte, err error) {
	// 异常捕获和错误处理
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	// 获取请求元数据和创建响应元数据
	req := proxy.Metadata()
	res := make(values.Metadata)

	// 路由解析和权限验证
	var p *session.Data
	var servicePath, serviceMethod string

	// 路由解析：将请求路径映射到具体的服务和方法
	servicePath, serviceMethod, err = Setting.Handler.Router(path, req)
	if err != nil {
		return nil, err
	}

	// 权限验证：验证用户是否有权限访问该服务和方法
	if p, err = Access.Verify(proxy, req, servicePath, serviceMethod); err != nil {
		return nil, err
	}

	// 设置网关地址和用户级别微服务筛选器
	req.Set(gwcfg.ServiceMetadataGateway, cosrpc.Address().Encode())
	// **每一次长连接转发都带上发起它的那条连接 id**，与鉴权档位无关。
	//
	// 业务服推消息时把它原样带回来（yyds 的 NewSender 已经这么做），网关据此判断
	// "这条推送还属不属于当初那条连接"——顶号或重连之后会话已经指向新 socket，
	// 按 GUID 投就会把上一代连接的数据推给刚上来的新端，而那次请求的确认包却走
	// 请求自己的 socket。一次响应被劈成两半，客户端两头都拿不全。
	//
	// ⚠️ 曾经只有 None/OAuth 两档塞它，Player/Select 档不塞。业务服感觉不到，
	// 因为 c.Send 会从玩家对象上现取 GUID 兜住；坏就坏在兜住之后没人发现路由变了。
	if sock := proxy.Socket(); sock != nil {
		req.Set(gwcfg.ServiceMetadataSocketId, strconv.FormatUint(sock.Id(), 10))
	}
	// 使用用户级别微服务筛选器：如果用户会话中存在该服务的地址，则使用该地址
	if p != nil {
		if serviceAddress := p.GetString(gwcfg.GetServiceSelectorAddress(servicePath)); serviceAddress != "" {
			req.Set(selector.MetaDataAddress, serviceAddress)
		}
	}

	// 处理请求：转发前预处理（默认 Default.Request 原样返回）；写入路由 path 供业务钩子判断(如 G2SOAuth)
	proxy.Path(path)
	if err = Setting.Handler.Request(proxy); err != nil {
		return nil, err
	}
	var body []byte
	if body, err = proxy.Buffer(); err != nil {
		return nil, err
	}
	// 性能监控：记录高延时请求
	startTime := time.Now()
	defer func() {
		if elapsed := time.Since(startTime); elapsed > ElapsedMillisecond {
			logger.Alert("发现高延时请求,TIME:%v,PATH:%v,LEN:%d", elapsed, path, len(body))
		}
	}()

	// 调用服务
	reply = make([]byte, 0)
	// 如果配置了服务前缀，则添加前缀
	if gwcfg.Options.Gate.Prefix != "" {
		serviceMethod = registry.Join(gwcfg.Options.Gate.Prefix, serviceMethod)
	}
	// 调用远程服务
	if err = client.CallWithMetadata(req, res, servicePath, serviceMethod, body, &reply); err != nil {
		return nil, err
	}

	// 处理登录和退出登录
	// 创建登录信息：如果响应中包含登录标志，则执行登录操作
	if guid, ok := res[gwcfg.ServicePlayerLogin]; ok {
		//秘钥不再回写 res：业务层 Response 钩子可直接用 c.Session() 取当前会话
		if _, err = proxy.login(guid, gwcfg.Cookies.Filter(res)); err != nil {
			return nil, err
		}
		p = proxy.Session()
	}
	// 退出登录：如果响应中包含退出登录标志，则执行退出登录操作
	if _, ok := res[gwcfg.ServicePlayerLogout]; ok {
		if err = proxy.logout(); err != nil {
			return nil, err
		} else if p != nil {
			players.Delete(p)
		}
		p = nil
	}

	index := proxy.Index()
	// 更新用户会话的 cookies 信息
	if p != nil {
		CookiesUpdate(res, p, index)
	}
	// 响应后处理：仅业务 Handler 实现 Response 时才构造响应上下文；meta 为响应元数据 res
	h, ok := Setting.Handler.(Response)
	if !ok {
		return reply, nil
	}
	// FlagConfirm 供钩子分流用（确认包 / service.go 的推送 / 广播 FlagBroadcast），
	// 不影响实际回包——代理路径的回包 flag 由 cosnet 的 Handler.reply 固定生成
	resFlag := message.Flag(res.GetInt32(gwcfg.ServiceResponseFlag))
	resFlag.Set(message.FlagConfirm)
	resCtx := newSocketContext(proxy.Socket(), proxy.Session(), path, index, reply, resFlag, res)
	if err = h.Response(resCtx); err != nil {
		return nil, err
	}
	//钩子改过的 flag 回写入站上下文，由 cosnet.Handler.reply 采纳（HTTP 无 flag，写入即空转）
	proxy.Flag(resCtx.Flag())
	return resCtx.Buffer()
}
