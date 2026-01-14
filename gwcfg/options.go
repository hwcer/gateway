package gwcfg

import (
	"strings"

	"github.com/hwcer/cosgo/binder"
)

const (
	ServiceName = "gate"
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

var Binder binder.Binder = binder.Json

var (
	Appid       string //程序名称
	Secret      string //平台秘钥
	Developer   string //开发者模式秘钥
	Maintenance bool   //进入维护模式，仅仅开发人员允许进入
)

var Options = struct {
	Redis     string   `json:"redis"`     //使用redis存储session，开启长连接时，请不要使用redis存储session
	Static    *Static  `json:"static"`    //静态服务器
	Prefix    string   `json:"prefix"`    //路由强制前缀
	Address   string   `json:"address"`   //连接地址
	Capacity  int      `json:"capacity"`  //session默认分配大小，
	Protocol  protocol `json:"protocol"`  //1-短链接，2-长连接，3-长短链接全开
	Websocket string   `json:"websocket"` //开启websocket时,路由前缀
	KeyFile   string   `json:"KeyFile"`   //HTTPS 证书KEY
	CertFile  string   `json:"CertFile"`  //HTTPS 证书Cert
}{
	Prefix:    "handle",
	Address:   "0.0.0.0:80",
	Capacity:  10240,
	Protocol:  2,
	Websocket: "ws",
}

type Static struct {
	Root  string `json:"root"`  //静态服务器根目录
	Route string `json:"route"` //静态服务器器前缀
	Index string `json:"index"` //默认页面
}

func GetServiceSelectorAddress(k string) string {
	return ServicePlayerSelector + strings.ToLower(k)
}
