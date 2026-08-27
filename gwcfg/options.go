package gwcfg

import (
	"github.com/hwcer/cosgo/binder"
)

const (
	ServiceName             = "gate"
	MessageSend             = "send"
	MessageWrite            = "write"
	MessageBroadcast        = "broadcast"
	MessageChannelDelete    = "channel/delete"
	MessageChannelBroadcast = "channel/broadcast"
)

type protocol int8

const (
	ProtocolTypeWSS  int8 = 1 << 0
	ProtocolTypeTCP  int8 = 1 << 1
	ProtocolTypeHTTP int8 = 1 << 2
)

func (p protocol) Has(t int8) bool {
	v := int8(p)
	return v|t == v
}

// CMux 是否启动 cmux 模块
func (p protocol) CMux() bool {
	var v int8
	if p.Has(ProtocolTypeTCP) {
		v++
	}
	if p.Has(ProtocolTypeWSS) || p.Has(ProtocolTypeHTTP) {
		v++
	}
	return v > 1
}

type config struct {
	// ForceReplace 是否强制顶号，默认 true。
	//
	// 两种策略下**老连接的处置完全一样**：收到顶号通知，进入 SocketReplacedTime 秒的
	// "只收不发"存活期（在途回包照常送达、新请求被拒），到期断开。唯一的区别是新端：
	//   true  强制顶号：新端**立即接管**会话上线，老连接把在途回包发完就没人理它了
	//   false 协商顶号：新端**本次登录被拒**（收到 ErrReplaced + 剩余秒数），
	//         等老连接下线后再次登录才能上来
	ForceReplace bool     `json:"forceReplace"`
	Redis        string   `json:"redis"`     //使用redis存储session，开启长连接时，请不要使用redis存储session
	Static       *Static  `json:"static"`    //静态服务器
	Prefix       string   `json:"prefix"`    //路由强制前缀
	Address      string   `json:"address"`   //连接地址
	Capacity     int      `json:"capacity"`  //session默认分配大小，
	Protocol     protocol `json:"protocol"`  //位掩码:1-WSS,2-TCP,4-HTTP,可组合(如7=全开)
	Websocket    string   `json:"websocket"` //开启websocket时,路由前缀
	KeyFile      string   `json:"KeyFile"`   //HTTPS 证书KEY
	CertFile     string   `json:"CertFile"`  //HTTPS 证书Cert
}

var Gateway = &config{
	ForceReplace: true,
	Prefix:       "handle",
	Address:      "0.0.0.0:80",
	Capacity:     10240,
	Protocol:     2,
	Websocket:    "",
}

var Options = struct {
	Gate        *config `json:"gate"`
	Appid       string  `json:"appid"`  //程序名称
	Secret      string  `json:"secret"` //平台秘钥
	Binder      string  `json:"binder"`
	Developer   string  `json:"developer"`   //开发者模式秘钥
	Maintenance bool    `json:"maintenance"` //进入维护模式，仅仅开发人员允许进入
}{
	Gate:   Gateway,
	Binder: binder.Json.Name(),
}

type Static struct {
	Root  string `json:"root"`  //静态服务器根目录
	Route string `json:"route"` //静态服务器器前缀
	Index string `json:"index"` //默认页面
}
