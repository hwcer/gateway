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

// 顶号/秘钥下发的默认包名（Default 实现用 MagicNumberPathJson 以 JSON 直接下发）
const (
	pathS2CSecret   = "S2CSecret"
	pathS2CReplaced = "S2CReplaced"
)

// Handler 网关行为接口。业务层可嵌入 Default 只覆盖需要改变的方法。
type Handler interface {
	Router(path string, req values.Metadata) (servicePath, serviceMethod string, err error) //路由处理规则
	Request(c context.Context) error                                                        //转发前预处理(如解密)，默认原样返回
	Response(c context.Context) error                                                       //返回/推送后处理，默认原样返回
	Serialize(accept Accept, reply any) ([]byte, error)                                     //响应序列化
	S2CSecret(sock *cosnet.Socket, secret string)                                           //登录成功给客户端下发秘钥
	S2CReplaced(sock *cosnet.Socket, ip string)                                             //被顶号时给客户端下发提示
	C2SOAuthArgs() token.Args                                                               //解析 C2SOAuth 参数
}

var Setting = struct {
	C2SOAuth     string  //网关登录包名,置空时不启用默认验证方式
	G2SOAuth     string  //游戏服登录验证,网关登录成功后继续用GUID去游戏服验证,留空不验证
	C2SHeartbeat string  //客户端心跳包名
	C2SReconnect string  //客户端断线重连包名
	Handler      Handler //网关行为实现,默认 Default{};业务层嵌入 Default 覆盖需要改变的方法
}{
	C2SOAuth:     "oauth",
	C2SHeartbeat: "C2SHeartbeat",
	C2SReconnect: "C2SReconnect",
	Handler:      Default{},
}

// Default 网关行为默认实现。业务层嵌入它，只覆盖需要改变的方法。
type Default struct{}

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

// Response 默认不处理，原样返回
func (Default) Response(c context.Context) error {
	return nil
}

// Serialize 默认序列化
func (Default) Serialize(accept Accept, reply any) ([]byte, error) {
	b := accept.Accept()
	v := values.Parse(reply)
	return b.Marshal(v)
}

// S2CSecret 默认用 MagicNumberPathJson 以 JSON 下发秘钥
func (Default) S2CSecret(sock *cosnet.Socket, secret string) {
	_ = sock.SendWithMagic(message.MagicNumberPathJson, message.FlagNoreply, 0, pathS2CSecret, secret)
}

// S2CReplaced 默认用 MagicNumberPathJson 以 JSON 下发顶号提示
func (Default) S2CReplaced(sock *cosnet.Socket, ip string) {
	_ = sock.SendWithMagic(message.MagicNumberPathJson, message.FlagNoreply, 0, pathS2CReplaced, ip)
}

// C2SOAuthArgs 默认 C2SOAuth 参数解析
func (Default) C2SOAuthArgs() token.Args {
	return token.NewArgs()
}
