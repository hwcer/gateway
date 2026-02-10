package gateway

import (
	"errors"
	"net"
	"strings"
	"time"

	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosrpc/redis"
	"github.com/hwcer/gateway/gwcfg"

	"github.com/hwcer/cosgo"
	"github.com/hwcer/cosgo/scc"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/coswss"
	"github.com/soheilhy/cmux"
)

var mod = &Module{}

var TCP = NewTCPServer()
var HTTP = NewHttpServer()

func New() *Module {
	return mod
}

type Module struct {
	mux cmux.CMux
}

func (this *Module) Id() string {
	return gwcfg.ServiceName
}

func (this *Module) Init() (err error) {
	if err = this.Reload(); err != nil {
		return
	}
	if gwcfg.Options.Gate.Address == "" {
		return errors.New("网关地址没有配置")
	}
	session.Heartbeat.Start()
	//session
	if gwcfg.Options.Gate.Redis != "" {
		session.Options.Storage, err = session.NewRedis(gwcfg.Options.Gate.Redis)
	} else {
		session.Options.Storage = session.NewMemory(gwcfg.Options.Gate.Capacity)
	}
	if err != nil {
		return err
	}

	if i := strings.Index(gwcfg.Options.Gate.Address, ":"); i < 0 {
		return errors.New("网关地址配置错误,格式: ip:port")
	} else if gwcfg.Options.Gate.Address[0:i] == "" {
		gwcfg.Options.Gate.Address = "0.0.0.0" + gwcfg.Options.Gate.Address
	}
	p := gwcfg.Options.Gate.Protocol
	if p.Has(gwcfg.ProtocolTypeTCP) || p.Has(gwcfg.ProtocolTypeWSS) {
		if err = TCP.init(); err != nil {
			return err
		}
	}
	if p.Has(gwcfg.ProtocolTypeHTTP) {
		if err = HTTP.init(); err != nil {
			return err
		}
	}
	//将 G2SOAuth 设置为 必须登录
	if Setting.G2SOAuth != "" {
		var ServicePath, ServiceMethod string
		if ServicePath, ServiceMethod, err = Setting.Router(Setting.G2SOAuth, values.Metadata{}); err != nil {
			return err
		}
		gwcfg.Authorize.Set(ServicePath, ServiceMethod, gwcfg.OAuthTypeOAuth)
	}

	return nil
}

func (this *Module) Start() (err error) {
	if err = redis.Start(); err != nil {
		return
	}
	if gwcfg.Options.Gate.Protocol.CMux() {
		var ln net.Listener
		if ln, err = net.Listen("tcp", gwcfg.Options.Gate.Address); err != nil {
			return err
		}
		this.mux = cmux.New(ln)
	}
	p := gwcfg.Options.Gate.Protocol
	//SOCKET
	if p.Has(gwcfg.ProtocolTypeTCP) {
		if this.mux != nil {
			so := this.mux.Match(cosnet.Matcher)
			err = TCP.Accept(so)
		} else {
			err = TCP.Listen(gwcfg.Options.Gate.Address)
		}
		if err != nil {
			return err
		}
	}
	//http
	if p.Has(gwcfg.ProtocolTypeHTTP) {
		if this.mux != nil {
			so := this.mux.Match(cmux.HTTP1Fast())
			err = HTTP.Accept(so)
		} else {
			err = HTTP.Listen(gwcfg.Options.Gate.Address)
		}
		if err != nil {
			return err
		}
	}

	// websocket
	if p.Has(gwcfg.ProtocolTypeWSS) {
		if p.Has(gwcfg.ProtocolTypeHTTP) {
			err = HTTP.wss() //在COSWEB上启动WS
		} else {
			// 使用coswss.New创建WebSocket服务器
			err = coswss.New(gwcfg.Options.Gate.Address, gwcfg.Options.Gate.Websocket)
		}
		if err != nil {
			return err
		}
	}

	if this.mux != nil {
		err = scc.Timeout(time.Second, func() error { return this.mux.Serve() })
		if errors.Is(err, scc.ErrorTimeout) {
			err = nil
		}
	}

	return err
}

func (this *Module) Reload() error {

	if err := cosgo.Config.Unmarshal(&gwcfg.Options); err != nil {
		return err
	}
	if gwcfg.Options.Appid == "" {
		gwcfg.Options.Appid = cosgo.Name()
	}

	return nil
}
func (this *Module) Close() (err error) {
	if this.mux != nil {
		this.mux.Close()
	}
	return nil
}
