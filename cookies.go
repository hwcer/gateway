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

// CookiesUpdate 把业务回包/推送 metadata 中的白名单键落到会话,并执行频道命令。
//
// expectUID 为推送路径的身份基线(service.send 按其查表):非空时走
// players.UpdateExpect 做锁内基线校验,会话 uid 已翻变则丢弃 uid 键——推送只投递、
// 不变更身份;请求/响应路径传空串,选角/换角的 uid 落地必须照常走 rebind 维护会话表。
func CookiesUpdate(cookie values.Metadata, p *session.Data, i int32, expectUID string) {
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
	//推送路径的身份基线先行:会话 uid 已翻变(换角/被顶)时,频道命令一并丢弃——
	//Join/Kick 会以会话当前(新)身份变更频道归属、Kick 作用于现表,与
	//"推送只投递、不变更身份"的口径冲突(UpdateExpect 内的锁内校验仍保留作防线)
	baselineOK := expectUID == "" || players.UIDIs(p, expectUID)
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
		if expectUID == "" {
			players.Update(p, vs) //请求路径:uid 变更(首次选角/换角)时同步维护会话表(键=uid)
		} else {
			players.UpdateExpect(p, vs, expectUID) //推送路径:身份基线校验,只投递不变更身份
		}
	}
	if !baselineOK {
		cmds = nil //基线不符:频道命令整体丢弃,cookies(_rid 等)仍照常应用
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
