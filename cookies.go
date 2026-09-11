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
	//频道命令统一编码:key = 命令前缀 + ["name","value"],频道身份完全由key表达;
	//value 仅 Kick 使用(被踢玩家UID),Join/Leave 为空
	//
	//频道命令**先收集、后执行**:必须等 Update 把 uid 落地(换角清理/入表/模拟登出
	//都在里面),Join 才能以**新身份**入房。旧顺序(循环里就地执行、Update 收尾)
	//下,换角同包携带的 Join 会以旧uid入房、随即被 rebind 的 SwitchUID 清掉;
	//首次选角同包的 Join 更是因 uid 为空被直接拒绝——两种情形命令都静默丢失
	type channelCmd struct {
		uid   string //仅 Kick:被踢玩家UID
		name  string
		value string
		kind  byte //j:Join l:Leave k:Kick
	}
	var cmds []channelCmd
	for k, v := range cookie {
		if s, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelJoin); ok {
			if name, value, err := context.ChannelNameParse(s); err == nil {
				cmds = append(cmds, channelCmd{name: name, value: value, kind: 'j'})
			} else {
				logger.Debug("channel Join metadata parse error:%v", err)
			}
		} else if s, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelLeave); ok {
			if name, value, err := context.ChannelNameParse(s); err == nil {
				cmds = append(cmds, channelCmd{name: name, value: value, kind: 'l'})
			} else {
				logger.Debug("channel Leave metadata parse error:%v", err)
			}
		} else if s, ok := strings.CutPrefix(k, gwcfg.ServicePlayerChannelKick); ok {
			if name, value, err := context.ChannelNameParse(s); err == nil {
				cmds = append(cmds, channelCmd{uid: v, name: name, value: value, kind: 'k'})
			} else {
				logger.Debug("channel Kick metadata parse error:%v", err)
			}
		} else if _, ok := gwcfg.Cookies[k]; ok || strings.HasPrefix(k, gwcfg.ServicePlayerSelector) {
			vs[k] = v
		}
	}
	if len(vs) > 0 {
		players.Update(p, vs) //uid 变更(首次选角/换角)时同步维护会话表(键=uid)
	}
	for _, c := range cmds {
		switch c.kind {
		case 'j':
			channel.Join(p, c.name, c.value)
		case 'l':
			channel.Leave(p, c.name, c.value)
		case 'k':
			channel.Kick(c.uid, c.name, c.value)
		}
	}
}
