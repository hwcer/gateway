package gateway

import (
	"bytes"
	"fmt"
	"gateway/gwcfg"
	"gateway/players"

	"github.com/hwcer/cosgo/registry"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosrpc"
	"github.com/hwcer/cosrpc/client"
)

func proxy(h gwcfg.Context) (reply []byte, err error) {
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
	var path string
	if path, err = h.Path(); err == nil {
		return nil, err
	}

	var p *session.Data
	var servicePath, serviceMethod string
	servicePath, serviceMethod, err = Setting.Router(path, req)
	if err != nil {
		return nil, err
	}
	if p, err = gwcfg.Access.Verify(h, req, servicePath, serviceMethod); err != nil {
		return nil, err
	}

	req.Set(gwcfg.ServicePlayerGateway, cosrpc.Address().Encode())
	//使用用户级别微服务筛选器
	if p != nil {
		if serviceAddress := p.GetString(gwcfg.GetServiceSelectorAddress(servicePath)); serviceAddress != "" {
			req.Set(gwcfg.ServicePlayerSelector, serviceAddress)
		}
	}
	var buff *bytes.Buffer
	if buff, err = h.Buffer(); err != nil {
		return nil, err
	}

	body, err := Setting.Request(p, path, req, buff.Bytes())
	if err != nil {
		return nil, err
	}

	reply = make([]byte, 0)
	if gwcfg.Options.Prefix != "" {
		serviceMethod = registry.Join(gwcfg.Options.Prefix, serviceMethod)
	}
	if err = client.CallWithMetadata(req, res, servicePath, serviceMethod, body, &reply); err != nil {
		return nil, err
	}
	res[gwcfg.ServiceMetadataResponseType] = gwcfg.ResponseTypeNone
	reply, err = Setting.Response(p, path, res, reply)
	if err != nil {
		return nil, err
	}
	if len(res) == 1 {
		return reply, nil
	}
	//创建登录信息
	if guid, ok := res[gwcfg.ServicePlayerLogin]; ok {
		if _, err = h.Login(guid, gwcfg.Cookies.Filter(res)); err != nil {
			return nil, err
		}
	}
	//退出登录
	if _, ok := res[gwcfg.ServicePlayerLogout]; ok {
		if err = h.Logout(); err != nil {
			return nil, err
		} else if p != nil {
			players.Delete(p)
		}
		p = nil
	}

	if p != nil {
		CookiesUpdate(res, p)
	}
	return reply, nil
}
