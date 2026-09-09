package gateway

import (
	"strings"

	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/context"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"

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
		//频道命令按前缀识别,rest 为去掉前缀后的频道名
		if rest, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelJoin); ok {
			channel.Join(p, rest, v)
		} else if rest, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelLeave); ok {
			channel.Leave(p, rest, v)
		} else if rest, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelKick); ok {
			//踢的是别人:发起人(如会长)回包携带,value 编码为[频道值,被踢玩家UID]
			if value, uid, err := context.ChannelNameParse(v); err == nil {
				channel.Kick(uid, rest, value)
			} else {
				logger.Debug("channel Kick metadata parse error:%v", err)
			}
		} else if _, ok := gwcfg.Cookies[k]; ok || strings.HasPrefix(k, gwcfg.ServicePlayerSelector) {
			vs[k] = v
		}
	}
	if len(vs) > 0 {
		players.Update(p, vs) //uid 变更时同步维护 UID->GUID 映射
	}
}
