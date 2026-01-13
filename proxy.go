package gateway

import (
	"bytes"
	"fmt"
	"server/gwcfg"

	"github.com/hwcer/cosgo/registry"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosrpc"
	"github.com/hwcer/cosrpc/client"
	"github.com/hwcer/yyds/modules/gateway/players"
	"github.com/hwcer/yyds/options"
)

func proxy(h Context, path string) (reply []byte, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		if err != nil && Setting.Errorf != nil {
			reply = Setting.Errorf(err)
			err = nil
		}
	}()
	req := h.Metadata()
	res := make(values.Metadata)
	var p *session.Data
	var servicePath, serviceMethod string
	servicePath, serviceMethod, err = Setting.Router(path, req)
	if err != nil {
		return nil, err
	}

	l, s := options.OAuth.Get(servicePath, serviceMethod)
	isMaster := options.OAuth.IsMaster(s)
	if f, ok := gwcfg.Access.dict[l]; !ok {
		return nil, fmt.Errorf("unknown authorization type: %d", l)
	} else if p, err = f(h, req, isMaster); err != nil {
		return nil, err
	}
	req.Set(options.ServicePlayerGateway, cosrpc.Address().Encode())
	//使用用户级别微服务筛选器
	if p != nil {
		if serviceAddress := p.GetString(options.GetServiceSelectorAddress(servicePath)); serviceAddress != "" {
			req.Set(options.SelectorAddress, serviceAddress)
		}
	}
	var buff *bytes.Buffer
	if buff, err = h.Buffer(); err != nil {
		return nil, err
	}
	//验证BODY有效性
	if Setting.Validate != nil {
		if err = Setting.Validate(p, l, s, req, buff.Bytes()); err != nil {
			return nil, err
		}
	}
	reply = make([]byte, 0)

	if Setting.Request != nil {
		Setting.Request(p, s, req)
	}

	if options.Gate.Prefix != "" {
		serviceMethod = registry.Join(options.Gate.Prefix, serviceMethod)
	}

	if err = client.CallWithMetadata(req, res, servicePath, serviceMethod, buff.Bytes(), &reply); err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return reply, nil
	}
	//创建登录信息
	if guid, ok := res[options.ServicePlayerOAuth]; ok {
		var token string
		if token, err = h.Login(guid, CookiesFilter(res)); err != nil {
			return nil, err
		}
		p = players.Get(guid)
		if Setting.Access != nil {
			reply = Setting.Access(p, token, reply)
		}
	}
	//退出登录
	if _, ok := res[options.ServicePlayerLogout]; ok {
		if err = h.Logout(); err == nil && p != nil {
			players.Delete(p)
		}
		p = nil
	}

	if p != nil {
		CookiesUpdate(res, p)
	}
	if err != nil {
		return nil, err
	} else {
		return reply, nil
	}
}
