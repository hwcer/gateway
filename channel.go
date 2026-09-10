package gateway

import (
	"github.com/hwcer/cosnet/message"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/context"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosrpc"
	"github.com/hwcer/logger"
)

func init() {
	Register(&channelHandle{}, "channel", "%m")
	channel.SendMessage = func(p *session.Data, path string, data []byte) {
		//房间成员表存的是入房那刻的会话实例:Redis 后端重连会从存储还原出新实例,
		//旧实例上的 socket 已死。按 uid 从会话表定位**当前**持有者再取 socket,
		//查不到(未选角/刚下线)才退回实例自带的 socket。
		sock := players.Socket(p)
		if uid := p.GetString(gwcfg.ServiceMetadataUID); uid != "" {
			if cur := players.Get(uid); cur != nil {
				sock = players.Socket(cur)
			}
		}
		if sock != nil {
			flag := message.FlagBroadcast
			_ = sock.Send(flag, 0, path, data)
		}
	}
}

// 内部接口，游戏服务器广播
type channelHandle struct {
}

func (this channelHandle) Broadcast(c *cosrpc.Context) any {
	path := c.GetMetadata(gwcfg.ServiceMessagePath)
	s := c.GetMetadata(gwcfg.ServiceMessageChannel)
	if s == "" {
		logger.Debug("频道名不能为空")
		return nil
	}
	name, value, err := context.ChannelNameParse(s)
	if err != nil {
		return err
	}

	room := channel.Get(name, value)
	if room == nil {
		logger.Debug("房间不存在,room:%s  path:%s", name, path)
		return nil
	}
	room.Broadcast(path, c.Bytes())
	logger.Debug("频道广播,room:%s  path:%s", s, path)

	return nil
}

// Delete 删除一个频道,如果path不为空，先使用path广播再删除
func (this channelHandle) Delete(c *cosrpc.Context) any {
	s := c.GetMetadata(gwcfg.ServiceMessageChannel)
	if s == "" {
		logger.Debug("频道名不能为空")
		return nil
	}
	name, value, err := context.ChannelNameParse(s)
	if err != nil {
		return err
	}
	room := channel.Get(name, value)
	if room == nil {
		logger.Debug("房间不存在,room:%s", s)
		return nil
	}

	if path := c.GetMetadata(gwcfg.ServiceMessagePath); path != "" {
		room.Broadcast(path, c.Bytes())
		logger.Debug("频道广播 name:%s  path:%s", s, path)
	}
	logger.Debug("删除频道 %s", name)
	channel.Delete(name, value)
	return nil
}

// Kick 将指定玩家踢出频道,S2S 直连命令(如世界服定时器里踢人,没有请求者回包可搭):
// 被踢玩家由 metadata uid 指定,频道由 ServiceMessageChannel 指定
func (this channelHandle) Kick(c *cosrpc.Context) any {
	uid := c.GetMetadata(gwcfg.ServiceMetadataUID)
	if uid == "" {
		logger.Debug("频道踢人失败,uid不能为空")
		return nil
	}
	s := c.GetMetadata(gwcfg.ServiceMessageChannel)
	if s == "" {
		logger.Debug("频道名不能为空")
		return nil
	}
	name, value, err := context.ChannelNameParse(s)
	if err != nil {
		return err
	}
	channel.Kick(uid, name, value)
	return nil
}
