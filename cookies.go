package gateway

import (
	"server/gwcfg"
	"strings"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/yyds/modules/gateway/channel"
)

var cookiesAllowableName = map[string]struct{}{}

func SetCookieName(k string) {
	cookiesAllowableName[k] = struct{}{}
}

func init() {
	SetCookieName(gwcfg.ServiceMetadataUID)
	SetCookieName(gwcfg.ServiceMetadataServerId)
	SetCookieName(gwcfg.ServiceMetadataDeveloper)
}

func CookiesFilter(cookie values.Metadata) values.Values {
	r := values.Values{}
	for k, v := range cookie {
		if _, ok := cookiesAllowableName[k]; ok {
			r[k] = v
		}
	}
	return r
}

func CookiesUpdate(cookie values.Metadata, p *session.Data) {
	vs := values.Values{}
	for k, v := range cookie {
		if strings.HasPrefix(k, gwcfg.ServicePlayerRoomJoin) {
			k = strings.TrimPrefix(k, gwcfg.ServicePlayerRoomJoin)
			channel.Join(p, k, v)
		} else if strings.HasPrefix(k, gwcfg.ServicePlayerRoomLeave) {
			k = strings.TrimPrefix(k, gwcfg.ServicePlayerRoomLeave)
			channel.Leave(p, k, v)
		} else if strings.HasPrefix(k, gwcfg.ServicePlayerSelector) {
			vs[k] = v
		} else if _, ok := cookiesAllowableName[k]; ok {
			vs[k] = v
		}
	}
	if len(vs) > 0 {
		p.Update(vs)
	}
}
