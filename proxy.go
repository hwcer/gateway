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

// Forward 把一次请求打到后端服务并取回回包 —— **业务层的快速转发入口**。
//
//	ctx := &gateway.SocketRequest{Context: c}
//	reply, err := gateway.Forward(ctx, "/game/heartbeat")
//
// 它负责与身份无关的那几件事：路由解析、下发网关地址与 socketId、过 Request 钩子、
// 取包体、加路由前缀、发 RPC。
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
	req := c.Metadata()
	var meta values.Metadata
	if len(res) > 0 && res[0] != nil {
		meta = res[0]
	} else {
		meta = values.Metadata{}
	}

	// 路由解析：将请求路径映射到具体的服务和方法
	var servicePath, serviceMethod string
	if servicePath, serviceMethod, err = Setting.Handler.Router(path, req); err != nil {
		return nil, err
	}

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
	if sock := c.Socket(); sock != nil {
		req.Set(gwcfg.ServiceMetadataSocketId, strconv.FormatUint(sock.Id(), 10))
	}

	// 转发前预处理（默认 Default.Request 原样返回）；写入路由 path 供业务钩子判断(如 G2SOAuth)
	c.Path(path)
	if err = Setting.Handler.Request(c); err != nil {
		return nil, err
	}
	var body []byte
	if body, err = c.Buffer(); err != nil {
		return nil, err
	}

	// 性能监控：记录高延时请求
	startTime := time.Now()
	defer func() {
		if elapsed := time.Since(startTime); elapsed > ElapsedMillisecond {
			logger.Alert("发现高延时请求,TIME:%v,PATH:%v,LEN:%d", elapsed, path, len(body))
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

// forward 网关内部的完整转发：**在 Forward 之上补齐登录态**，不重写转发本身。
//
// 只有它需要 Request —— 鉴权要 verify()（HTTP 那份会从 cookie/query/header 取 token
// 现建会话，不是 Session() 能替代的），响应里的登录/登出标记要 login()/logout()。
// 这三样都是业务层碰不到也封装不了的，所以本函数不导出。
func forward(proxy Request, path string) (reply []byte, err error) {
	// 异常捕获和错误处理
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	req := proxy.Metadata()
	res := values.Metadata{}

	// 鉴权要先知道路由档位。Router 是纯字符串处理且对 metadata 的写入幂等，
	// Forward 里再解析一次没有副作用，换来的是两边各取所需、不必互相传参。
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

	if reply, err = Forward(proxy, path, res); err != nil {
		return nil, err
	}

	// 处理登录和退出登录 —— 需要 Request
	if guid, ok := res[gwcfg.ServicePlayerLogin]; ok {
		//秘钥不再回写 res：业务层 Response 钩子可直接用 c.Session() 取当前会话
		if _, err = proxy.login(guid, gwcfg.Cookies.Filter(res)); err != nil {
			return nil, err
		}
		p = proxy.Session()
	}
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
	return response(proxy, path, reply, res)
}

// response 把一份**即将回给客户端**的 []byte 过一遍业务的 Response 钩子（加密/改包/改 flag）。
// 未实现该钩子时原样返回。
//
// ⚠️ 走 Forward 拿到 []byte 再原样返回给客户端的路径，都要过这里。cosnet 的 handler.reply
// 对 []byte 是特判的——直接发，**连 Serialize 都不过**；漏了这一步，那条回包就完全没有
// 后处理机会，而代理路径(forward)是过了的，同一个客户端会收到两套编码的包。
//
// 规则与 deliver/broadcast 一致：**谁把数据发给客户端，谁负责过钩子**。
//
// ⚠️ 必须在登录/登出处理**之后**调用：钩子里业务层会用 c.Session() 取当前会话，
// 而登录正是在转发之后才完成的，提前调就读到登录前的那份。
func response(c context.Context, path string, reply []byte, res values.Metadata) ([]byte, error) {
	h, ok := Setting.Handler.(Response)
	if !ok {
		return reply, nil
	}
	// FlagConfirm 供钩子分流用（确认包 / service.go 的推送 / 广播 FlagBroadcast），
	// 不影响实际回包——代理路径的回包 flag 由 cosnet 的 Handler.reply 固定生成
	resFlag := message.Flag(res.GetInt32(gwcfg.ServiceResponseFlag))
	resFlag.Set(message.FlagConfirm)
	resCtx := newSocketContext(c.Socket(), c.Session(), path, c.Index(), reply, resFlag, res)
	if err := h.Response(resCtx); err != nil {
		return nil, err
	}
	//钩子改过的 flag 回写入站上下文，由 cosnet.Handler.reply 采纳（HTTP 无 flag，写入即空转）
	c.Flag(resCtx.Flag())
	return resCtx.Buffer()
}
