package gateway

import (
	"strings"

	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
)

func CookiesUpdate(cookie values.Metadata, p *session.Data) {
	vs := values.Values{}
	for k, v := range cookie {
		if strings.HasPrefix(k, gwcfg.ServicePlayerChannelJoin) {
			k = strings.TrimPrefix(k, gwcfg.ServicePlayerChannelJoin)
			channel.Join(p, k, v)
		} else if strings.HasPrefix(k, gwcfg.ServicePlayerChannelLeave) {
			k = strings.TrimPrefix(k, gwcfg.ServicePlayerChannelLeave)
			channel.Leave(p, k, v)
		} else if strings.HasPrefix(k, gwcfg.ServicePlayerSelector) {
			vs[k] = v
		} else if _, ok := gwcfg.Cookies[k]; ok {
			vs[k] = v
		}
	}
	if len(vs) > 0 {
		p.Update(vs)
	}
}
