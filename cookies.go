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
		//频道命令统一编码:key = 命令前缀 + ["name","value"],频道身份完全由key表达;
		//value 仅 Kick 使用,携带被踢玩家UID,Join/Leave 为空
		if s, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelJoin); ok {
			if name, value, err := context.ChannelNameParse(s); err == nil {
				channel.Join(p, name, value)
			} else {
				logger.Debug("channel Join metadata parse error:%v", err)
			}
		} else if s, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelLeave); ok {
			if name, value, err := context.ChannelNameParse(s); err == nil {
				channel.Leave(p, name, value)
			} else {
				logger.Debug("channel Leave metadata parse error:%v", err)
			}
		} else if s, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelKick); ok {
			if name, value, err := context.ChannelNameParse(s); err == nil {
				channel.Kick(v, name, value)
			} else {
				logger.Debug("channel Kick metadata parse error:%v", err)
			}
		} else if _, ok := gwcfg.Cookies[k]; ok || strings.HasPrefix(k, gwcfg.ServicePlayerSelector) {
			vs[k] = v
		}
	}
	if len(vs) > 0 {
		players.Update(p, vs) //uid 变更(首次选角/换角)时同步维护会话表
	}
}
