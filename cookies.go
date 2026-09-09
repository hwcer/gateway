package gateway

import (
	"strings"

	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/context"
	"github.com/hwcer/gateway/gwcfg"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/logger"
)

func CookiesUpdate(cookie values.Metadata, p *session.Data, i int32) {
	vs := values.Values{}
	if i > 0 {
		vs[gwcfg.ServiceMetadataRequestId] = i
	}
	for k, v := range cookie {
		if strings.HasPrefix(k, gwcfg.ServicePlayerChannelJoin) {
			k = strings.TrimPrefix(k, gwcfg.ServicePlayerChannelJoin)
			channel.Join(p, k, v)
		} else if strings.HasPrefix(k, gwcfg.ServicePlayerChannelLeave) {
			k = strings.TrimPrefix(k, gwcfg.ServicePlayerChannelLeave)
			channel.Leave(p, k, v)
		} else if strings.HasPrefix(k, gwcfg.ServicePlayerChannelKick) {
			//踢的是别人:发起人(如会长)回包携带,value 编码为[频道值,被踢玩家UID]
			k = strings.TrimPrefix(k, gwcfg.ServicePlayerChannelKick)
			if value, uid, err := context.ChannelNameParse(v); err == nil {
				channel.Kick(uid, k, value)
			} else {
				logger.Debug("channel Kick metadata parse error:%v", err)
			}
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
