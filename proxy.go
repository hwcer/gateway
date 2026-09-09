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

// inbound 在 context.Context（统一请求上下文）之上附加**登录态内部操作**。
//
// 不导出：只有 forward 与 access 用得到它，业务层一律只接触 context.Context——
// 所有对外的钩子（Request / Response / C2SHeartbeat / C2SReconnect）和 Forward
// 都收 context.Context。
type inbound interface {
	context.Context
	login(guid string, value values.Values) (string, error) //通过业务服激活登录信息（gateway 内部）
	logout() error                                          //退出登录（gateway 内部）
	verify() (*session.Data, error)                         //验证登录信息（gateway 内部）
}

// recoverPanic 把 panic 收成 error 交回调用方，转发的两个入口各用一次。
//
// 要防的是**业务层代码**：Router / Request / Response / Serialize 都是 Handler 实现的，
// 一次空指针就会顺着调用栈把整条连接的处理打断——转发失败最多是这个请求报错，
// 不该演变成掉线。
//
// ⚠️ recover 只在被 defer **直接调用**的函数里有效，所以必须写成 defer recoverPanic(&err)，
// 不能包一层匿名函数再调它。
func recoverPanic(err *error) {
	if e := recover(); e != nil {
		*err = fmt.Errorf("%v", e)
	}
}

// Forward 把一次请求打到后端服务并取回回包 —— **业务层的快速转发入口**。
//
//	ctx := &gateway.SocketRequest{Context: c}
//	reply, err := gateway.Forward(ctx, "/game/heartbeat")
//
// 它负责与身份无关的那几件事：路由解析、下发网关地址与 socketId、过 Request 钩子、
// 取包体、加路由前缀、发 RPC，最后**过 Response 钩子**。
//
// 回包的后处理堵在这一层，内外调用都不必各自记得补——凡是经过转发的包，一定走完整的
// Request → RPC → Response 周期。
//
// ⚠️ **不处理登录态**（鉴权 / 登录 / 登出）。那三样要 Request 里的 verify/login/logout，
// 是网关自己的活，业务层既封装不了也不该碰 —— 需要它们的是网关内部的 forward，
// 它在这个函数之上补齐，而不是另写一遍转发。
//
// ⚠️ 自己拼 metadata 去 client.CallWithMetadata 是很容易出错的路子：网关地址、socketId、
// 由消息 magic 推导的 Accept/Content-Type，漏一个的症状都很隐蔽（回包编码不对、
// 被判顶号、推送投错连接）。走这里就都对了。
//
// res 用来接住响应元数据（登录/登出标记、cookies 等），只有 forward 需要，业务层不必传。
func Forward(c context.Context, path string, res ...values.Metadata) (reply []byte, err error) {
	defer recoverPanic(&err)
	meta := values.Metadata{} //响应元数据:调用方给了就写进它的,没给就自己拿一个用完丢
	if len(res) > 0 && res[0] != nil {
		meta = res[0]
	}
	var servicePath, serviceMethod string
	if servicePath, serviceMethod, err = Setting.Handler.Router(path, c.Metadata()); err != nil {
		return nil, err
	}
	//**转发目标**写进上下文,供 Request/Response 钩子分流与日志取用。
	//代理路径下 path 本就取自 c.Path(),写回去是无操作;但 G2SOAuth / G2SHeartbeat 这类
	//"客户端路径 ≠ 转发目标"的场合必须写——不写钩子拿到的是客户端包名(如 oauth),
	//而业务层判的是转发目标(如 /game/oauth)。
	c.Path(path)
	if reply, err = proxyRequest(c, servicePath, serviceMethod, meta); err != nil {
		return nil, err
	}
	return response(c, servicePath, serviceMethod, reply, meta)
}

// forward 网关内部的完整转发：**在 Forward 之上补齐登录态**，不重写转发本身。
//
// 只有它需要 Request —— 鉴权要 verify()（HTTP 那份会从 cookie/query/header 取 token
// 现建会话，不是 Session() 能替代的），响应里的登录/登出标记要 login()/logout()。
// 这三样都是业务层碰不到也封装不了的，所以本函数不导出。
func forward(proxy inbound, path string) (reply []byte, err error) {
	defer recoverPanic(&err)
	req := proxy.Metadata()
	res := values.Metadata{}

	// 鉴权要先知道路由档位，解析结果一路传下去，不在链路上重复调 Router
	var servicePath, serviceMethod string
	if servicePath, serviceMethod, err = Setting.Handler.Router(path, req); err != nil {
		return nil, err
	}
	var p *session.Data
	if p, err = Access.Verify(proxy, req, servicePath, serviceMethod); err != nil {
		return nil, err
	}

	// 用户级别微服务筛选器：会话里锁定过该服务的地址就按地址定向（多区服下漏掉会投错服）
	if p != nil {
		if serviceAddress := p.GetString(gwcfg.GetServiceSelectorAddress(servicePath)); serviceAddress != "" {
			req.Set(selector.MetaDataAddress, serviceAddress)
		}
	}

	proxy.Path(path) //同 Forward:让钩子看到转发目标而不是客户端包名
	if reply, err = proxyRequest(proxy, servicePath, serviceMethod, res); err != nil {
		return nil, err
	}

	// 处理登出 —— 需要 inbound
	if _, ok := res[gwcfg.ServicePlayerLogout]; ok {
		if err = proxy.logout(); err != nil {
			return nil, err
		} else if p != nil {
			players.Delete(p)
		}
		p = nil
	}

	// 更新用户会话的 cookies 信息
	if p != nil {
		CookiesUpdate(res, p, proxy.Index())
	}
	//Response 排在登录/登出之后:钩子里 c.Session() 要读到这次新建的会话
	return response(proxy, servicePath, serviceMethod, reply, res)
}

// response 把一份**即将回给客户端**的 []byte 过一遍业务的 Response 钩子（加密/改包/改 flag）。
// 未实现该钩子时原样返回。
//
// 调用方是 Forward（业务层的转发）、forward（代理路径）与 deliver/broadcast（推送与广播）
// ——规则是**谁把数据发给客户端，谁负责过钩子**。
//
// ⚠️ 代理路径上必须排在登录/登出**之后**：钩子里业务层会用 c.Session() 取当前会话，
// 而登录正是转发回来才完成的，提前调就读到登录前的那份。
//
// servicePath/serviceMethod 是已解析的后端路由，直接给钩子用；推送与广播没有后端路由，
// 那两处传空。
func response(c context.Context, servicePath, serviceMethod string, reply []byte, res values.Metadata) ([]byte, error) {
	h, ok := Setting.Handler.(Response)
	if !ok {
		return reply, nil
	}
	// FlagConfirm 供钩子分流用（确认包 / service.go 的推送 / 广播 FlagBroadcast），
	// 不影响实际回包——代理路径的回包 flag 由 cosnet 的 Handler.reply 固定生成
	resFlag := message.Flag(res.GetInt32(gwcfg.ServiceResponseFlag))
	resFlag.Set(message.FlagConfirm)
	resCtx := newSocketContext(c, reply, resFlag, res)
	if err := h.Response(resCtx, servicePath, serviceMethod); err != nil {
		return nil, err
	}
	//钩子改过的 flag 回写入站上下文，由 cosnet.Handler.reply 采纳（HTTP 无 flag，写入即空转）
	c.Flag(resCtx.Flag())
	return resCtx.Buffer()
}

// proxyRequest 纯转发：下发 metadata → Request 钩子 → RPC，取回原始回包。
//
// **既不碰登录态，也不过 Response**——前者是 forward 的活，后者由两个调用方各自在
// 正确的位置补：Forward 紧跟转发就过，forward 要等登录/登出处理完再过（钩子里会取会话）。
//
// 路由由调用方解析好传进来：forward 得先拿档位做鉴权、Forward 作为入口自己解析，
// 各解析一次就够——Router 是业务层实现的，不该为省事在链路上反复调。
//
// 转发目标同样由调用方写进上下文（c.Path(path)），这里与后续的 response 一律 c.Path() 取，
// 不再逐层当参数传。
func proxyRequest(c context.Context, servicePath, serviceMethod string, meta values.Metadata) (reply []byte, err error) {
	req := c.Metadata()
	req.Set(gwcfg.ServiceMetadataGateway, cosrpc.Address().Encode())

	// **每一次长连接转发都带上发起它的那条连接 id**，与鉴权档位无关。
	//
	// 业务服推消息时把它原样带回来（yyds 的 NewSender 已经这么做），网关据此判断
	// "这条推送还属不属于当初那条连接"——顶号或重连之后会话已经指向新 socket，
	// 按 GUID 投就会把上一代连接的数据推给刚上来的新端，而那次请求的确认包却走
	// 请求自己的 socket。一次响应被劈成两半，客户端两头都拿不全。
	//
	// ⚠️ 曾经只有 None/OAuth 两档塞它，Player/Select 档不塞。业务服感觉不到，
	// 因为 c.Send 会从玩家对象上现取 uid 兜住；坏就坏在兜住之后没人发现路由变了。
	if sock := c.Socket(); sock != nil {
		req.Set(gwcfg.ServiceMetadataSocketId, strconv.FormatUint(sock.Id(), 10))
	}

	// 转发前预处理：业务 Handler 实现了 Request 才有（解密、改包体之类）
	if h, ok := Setting.Handler.(Request); ok {
		if err = h.Request(c, servicePath, serviceMethod); err != nil {
			return nil, err
		}
	}
	var body []byte
	if body, err = c.Buffer(); err != nil {
		return nil, err
	}

	// 性能监控：记录高延时请求
	startTime := time.Now()
	defer func() {
		if elapsed := time.Since(startTime); elapsed > ElapsedMillisecond {
			logger.Alert("发现高延时请求,TIME:%v,PATH:%v,LEN:%d", elapsed, c.Path(), len(body))
		}
	}()

	// 如果配置了服务前缀，则添加前缀
	if gwcfg.Options.Gate.Prefix != "" {
		serviceMethod = registry.Join(gwcfg.Options.Gate.Prefix, serviceMethod)
	}
	reply = make([]byte, 0)
	if err = client.CallWithMetadata(req, meta, servicePath, serviceMethod, body, &reply); err != nil {
		return nil, err
	}
	return reply, nil
}
