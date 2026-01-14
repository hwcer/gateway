package gateway

import (
	"errors"
	"gateway/gwcfg"
	"gateway/rpcx"
	"net"
	"strings"
	"time"

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
	if gwcfg.Options.Address == "" {
		return errors.New("网关地址没有配置")
	}
	session.Heartbeat.Start()
	//session
	if gwcfg.Options.Redis != "" {
		session.Options.Storage, err = session.NewRedis(gwcfg.Options.Redis)
	} else {
		session.Options.Storage = session.NewMemory(gwcfg.Options.Capacity)
	}
	if err != nil {
		return err
	}

	if i := strings.Index(gwcfg.Options.Address, ":"); i < 0 {
		return errors.New("网关地址配置错误,格式: ip:port")
	} else if gwcfg.Options.Address[0:i] == "" {
		gwcfg.Options.Address = "0.0.0.0" + gwcfg.Options.Address
	}
	p := gwcfg.Options.Protocol
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

	return nil
}

func (this *Module) Start() (err error) {
	if err = rpcx.Start(); err != nil {
		return
	}
	if gwcfg.Options.Protocol.CMux() {
		var ln net.Listener
		if ln, err = net.Listen("tcp", gwcfg.Options.Address); err != nil {
			return err
		}
		this.mux = cmux.New(ln)
	}
	p := gwcfg.Options.Protocol
	//SOCKET
	if p.Has(gwcfg.ProtocolTypeTCP) {
		if this.mux != nil {
			so := this.mux.Match(cosnet.Matcher)
			err = TCP.Accept(so)
		} else {
			err = TCP.Listen(gwcfg.Options.Address)
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
			err = HTTP.Listen(gwcfg.Options.Address)
		}
		if err != nil {
			return err
		}
	}

	// websocket
	if p.Has(gwcfg.ProtocolTypeWSS) {
		if p.Has(gwcfg.ProtocolTypeHTTP) {
			err = coswss.Binding(HTTP.Server, gwcfg.Options.Websocket)
		} else {
			err = coswss.Listen(gwcfg.Options.Address, gwcfg.Options.Websocket)
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

	gwcfg.Appid = cosgo.Config.GetString("appid")
	gwcfg.Secret = cosgo.Config.GetString("secret")
	gwcfg.Developer = cosgo.Config.GetString("developer")
	gwcfg.Maintenance = cosgo.Config.GetBool("maintenance")

	if gwcfg.Appid == "" {
		gwcfg.Appid = cosgo.Name()
	}

	return cosgo.Config.UnmarshalKey(gwcfg.ServiceName, &gwcfg.Options)
}
func (this *Module) Close() (err error) {
	if this.mux != nil {
		this.mux.Close()
	}
	return nil
}
