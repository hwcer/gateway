package gateway

import (
	"strings"

	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/message"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/context"
	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/players"
	"github.com/hwcer/gateway/token"

	"github.com/hwcer/cosgo"
	"github.com/hwcer/cosgo/binder"
	"github.com/hwcer/cosgo/registry"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
)

func init() {
	cosgo.On(cosgo.EventTypStarted, func() error {
		session.On(session.EventSessionRelease, release)
		return nil
	})
}

// 监控登录信息
func release(i any) {
	data, _ := i.(*session.Data)
	if data == nil {
		return
	}
	_ = players.Delete(data)
	channel.Release(data)
}

type Accept interface {
	Accept() binder.Binder
}

// C2SHeartbeat 可选接口：业务 Handler 实现则覆盖网关默认心跳处理。
//
// 参数是协议无关的 context.Context（不是 *cosnet.Context）——**短连接也有心跳保活**，
// 长短连接共用同一个钩子。要转发到后端服务时直接把它交给 gateway.Forward 即可。
type C2SHeartbeat interface {
	C2SHeartbeat(c context.Context) any
}

// C2SReconnect 可选接口：业务 Handler 实现则覆盖网关默认重连处理。
//
// 参数同样是协议无关的 context.Context：网关默认实现要绑定 socket、短连接走不通，
// 需要 HTTP 也支持重连时由业务层自己定义语义（比如只验 secret、只回 handledIndex）。
type C2SReconnect interface {
	C2SReconnect(c context.Context) any
}

// Response 可选接口：业务 Handler 实现则在回包/推送前做后处理（如加密、改包、改 flag）。
// 未实现时网关原样返回，且不再构造响应上下文（socketContext/resFlag）。
//
// ⚠️⚠️ **Response 与 Serialize 是互斥的两条路，不是一前一后**。cosnet 的 handler.reply
// 按 handler 返回值类型分流，一份数据只会经过其中一条：
//
//	返回 []byte           → 直接发，只过 Response（代理回包、心跳转发、推送、广播）
//	返回其它任意类型       → 先过 Serialize 再发，**不过 Response**（心跳降级的时间戳、
//	                        断线重连的 handledIndex，以及业务 handler 的常规返回值）
//
// 所以**加密这类后处理必须在两处都做**，只写一处就会有一类包是裸的，而且很难发现：
// 客户端大部分包解得开，偏偏心跳或重连解不开。
type Response interface {
	Response(c context.Context) error
}

// 顶号/秘钥下发的默认包名（Default 实现用 MagicNumberPathJson 以 JSON 直接下发）
const (
	pathS2CSecret   = "S2CSecret"
	pathS2CReplaced = "S2CReplaced"
)

// Handler 网关行为接口。业务层可嵌入 Default 只覆盖需要改变的方法。
type Handler interface {
	Token() token.Token                                                                     //登录（C2SOAuth）Token
	Router(path string, req values.Metadata) (servicePath, serviceMethod string, err error) //路由处理规则
	Request(c context.Context) error                                                        //转发前预处理(如解密)，默认原样返回；可直接交给 Forward
	Serialize(accept Accept, reply any) ([]byte, error)                                     //响应序列化

	// 以下两个是**长连接特有**，参数是 *cosnet.Socket 而不是 context.Context：
	// 它们由 socket 事件驱动（EventTypeAuthentication / EventTypeReplaced）而非请求驱动，
	// 手里根本没有请求上下文；短连接也没有"下发秘钥"和"被顶号"这两件事。
	S2CSecret(sock *cosnet.Socket, secret string)        //登录成功给客户端下发秘钥
	S2CReplaced(sock *cosnet.Socket, r *cosnet.Replaced) //有人请求顶号时给客户端下发协商提示
}

var Setting = struct {
	C2SOAuth     string  //网关登录包名,置空时不启用默认验证方式
	G2SOAuth     string  //游戏服登录验证,网关登录成功后继续用GUID去游戏服验证,留空不验证
	C2SHeartbeat string  //客户端心跳包名
	G2SHeartbeat string  //游戏服心跳包名
	C2SReconnect string  //客户端断线重连包名
	Handler      Handler //网关行为实现,默认 Default{};业务层嵌入 Default 覆盖需要改变的方法

	// ForceReplace 是否强制顶号，默认 true。
	//
	// 两种策略下**老连接的处置完全一样**：收到顶号通知，进入
	// cosnet.Options.SocketReplacedTime 秒的"只收不发"存活期（在途回包照常送达、
	// 它自己发来的新请求被回 209），到期断开。唯一的区别是新端：
	//   true  强制顶号：新端**立即接管**会话上线，老连接把在途回包发完就没人理它了
	//   false 协商顶号：新端**本次登录被拒**（收到 errors.ErrReplaced + 剩余秒数），
	//         等老连接下线后再次登录才能上来
	//
	// 实质差别在服务器推送：强制模式下 send 按 GUID 查到的已是新 socket，老连接那次
	// 请求的推送投给新端、确认包却走老端，一次响应被劈成两半；协商模式没有这个问题。
	ForceReplace bool
}{
	C2SOAuth:     "oauth",
	C2SHeartbeat: "C2SHeartbeat",
	C2SReconnect: "C2SReconnect",
	Handler:      Default{},
	ForceReplace: true,
}

// Default 网关行为默认实现。业务层嵌入它，只覆盖需要改变的方法。
type Default struct{}

// Token 默认 C2SOAuth 参数解析
func (Default) Token() token.Token {
	return &token.Default{}
}

// Router 默认路由：/servicePath/serviceMethod
func (Default) Router(path string, req values.Metadata) (servicePath, serviceMethod string, err error) {
	path = strings.TrimPrefix(path, "/")
	i := strings.Index(path, "/")
	if i < 0 {
		err = errors.ErrNotFount
		return
	}
	servicePath = registry.Formatter(path[0:i])
	serviceMethod = registry.Formatter(path[i:])
	return
}

// Request 默认不处理，原样转发
func (Default) Request(c context.Context) error {
	return nil
}

// Serialize 默认序列化
func (Default) Serialize(accept Accept, reply any) ([]byte, error) {
	b := accept.Accept()
	v := values.Parse(reply)
	return b.Marshal(v)
}

// S2CSecret 默认用 MagicNumberPathJson 以 JSON 下发秘钥（长连接特有）
func (Default) S2CSecret(sock *cosnet.Socket, secret string) {
	_ = sock.SendWithMagic(message.MagicNumberPathJson, message.FlagNoreply, 0, pathS2CSecret, secret)
}

// S2CReplaced 默认用 MagicNumberPathJson 以 JSON 下发顶号协商提示（长连接特有）
//
// 下发的是整个 *cosnet.Replaced（Address + Timeout），不再只是一个 IP 字符串：
// 语义已经从"你被顶掉了"变成"有人想顶号，你还剩 Timeout 秒"，客户端得拿这个秒数
// 决定是弹让出确认框还是提示倒计时。
func (Default) S2CReplaced(sock *cosnet.Socket, r *cosnet.Replaced) {
	_ = sock.SendWithMagic(message.MagicNumberPathJson, message.FlagNoreply, 0, pathS2CReplaced, r)
}
