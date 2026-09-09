package gwcfg

import (
	"github.com/hwcer/cosgo/binder"
)

// 微服务名。**定义在这里而不是业务框架层**：网关要按服务名做路由与转发
// （如心跳补服务名前缀），却不能反向依赖 yyds——那会形成 gateway ← yyds 的循环。
// yyds/options 保留同名常量指向这里，业务层原有引用不受影响。
const (
	ServiceTypeGate    = "gate"    //网关
	ServiceTypeGame    = "game"    //游戏服
	ServiceTypeWorld   = "world"   //世界服
	ServiceTypeBattle  = "battle"  //战斗服
	ServiceTypeRooms   = "rooms"   //游戏大厅
	ServiceTypeSocial  = "social"  //社交用户中心
	ServiceTypeLocator = "locator" //角色定位中心
)

// Deprecated: 用 ServiceTypeGate。它与上面那组是同一类东西(微服务名)，
// 只是历史上单独起了个名字、放在了另一个块里，新代码别再用。
const ServiceName = ServiceTypeGate

const (
	MessageSend             = "send"
	MessageWrite            = "write"
	MessageBroadcast        = "broadcast"
	MessageChannelDelete    = "channel/delete"
	MessageChannelBroadcast = "channel/broadcast"
	MessageChannelKick      = "channel/kick"
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
	Redis     string   `json:"redis"`     //使用redis存储session，开启长连接时，请不要使用redis存储session
	Static    *Static  `json:"static"`    //静态服务器
	Prefix    string   `json:"prefix"`    //路由强制前缀
	Address   string   `json:"address"`   //连接地址
	Capacity  int      `json:"capacity"`  //session默认分配大小，
	Protocol  protocol `json:"protocol"`  //位掩码:1-WSS,2-TCP,4-HTTP,可组合(如7=全开)
	Websocket string   `json:"websocket"` //开启websocket时,路由前缀
	KeyFile   string   `json:"KeyFile"`   //HTTPS 证书KEY
	CertFile  string   `json:"CertFile"`  //HTTPS 证书Cert
}

var Gateway = &config{
	Prefix:    "handle",
	Address:   "0.0.0.0:80",
	Capacity:  10240,
	Protocol:  2,
	Websocket: "",
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
