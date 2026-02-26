package gateway

import (
	"strconv"
	"strings"

	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet/message"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosrpc"
	"github.com/hwcer/cosrpc/server"
	"github.com/hwcer/logger"
)

var Service = server.Service(gwcfg.ServiceName)

func init() {
	Register(send)
	Register(write)
	Register(broadcast)
}

// Register 注册协议，用于服务器推送消息
func Register(i any, prefix ...string) {
	if err := Service.Register(i, prefix...); err != nil {
		logger.Fatal("%v", err)
	}
}

// 仅仅 在登录接口本身 需要提前对SOCKET发送信息时使用
func write(c *cosrpc.Context) any {
	id := c.GetMetadata(gwcfg.ServiceMetadataSocketId)
	if id == "" {
		return c.Error("socket id not found")
	}
	path := c.GetMetadata(gwcfg.ServiceMessagePath)
	i, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		logger.Debug("Socket id error,消息丢弃,Socket:%s PATH:%s ", id, path)
		return nil
	}
	sock := cosnet.Get(i)
	if sock == nil {
		logger.Debug("长链接不在线,消息丢弃,Socket:%s PATH:%s ", id, path)
		return nil
	}
	if len(path) == 0 {
		return nil //仅仅设置信息，不需要发送
	}
	mate := values.Metadata(c.Metadata())
	var flag = message.Flag(mate.GetInt32(gwcfg.ServiceResponseFlag))
	body := c.Bytes()
	if Setting.Response != nil {
		ctx := NewContextWithSocket(&path, &flag, mate, sock)
		body, err = Setting.Response(ctx, body)
	}

	if err != nil {
		return err
	}
	rid := mate.GetInt32(gwcfg.ServiceMetadataRequestId)
	_ = sock.Send(flag, rid, path, body)
	return nil
}

// send 消息推送
func send(c *cosrpc.Context) any {
	uid := c.GetMetadata(gwcfg.ServiceMetadataUID)
	guid := c.GetMetadata(gwcfg.ServiceMetadataGUID)

	p := players.Get(guid)
	if p == nil {
		logger.Debug("用户不在线,消息丢弃,UID:%s GUID:%s", uid, guid)
		return nil
	}
	if uid != "" {
		if id := p.GetString(gwcfg.ServiceMetadataUID); id != "" && id != uid {
			logger.Debug("用户UID不匹配,UID:%s GUID:%s", uid, guid)
			return nil
		}
	}

	mate := values.Metadata(c.Metadata())
	if _, ok := mate[gwcfg.ServicePlayerLogout]; ok {
		players.Delete(p)
		return nil
	}
	path := c.GetMetadata(gwcfg.ServiceMessagePath)

	sock := players.Socket(p)
	if sock == nil {
		logger.Debug("长链接不在线,消息丢弃,UID:%s GUID:%s PATH:%s ", uid, guid, path)
		return nil
	}
	CookiesUpdate(mate, p)
	if len(path) == 0 {
		return nil //仅仅设置信息，不需要发送
	}

	var err error
	flag := message.Flag(mate.GetInt32(gwcfg.ServiceResponseFlag))
	body := c.Bytes()
	if Setting.Response != nil {
		ctx := NewContextWithSocket(&path, &flag, mate, sock)
		body, err = Setting.Response(ctx, body)
	}
	if err != nil {
		return err
	}
	rid := mate.GetInt32(gwcfg.ServiceMetadataRequestId)
	//logger.Debug("推送消息  GUID:%s RID:%d PATH:%s", guid, rid, path)
	_ = sock.Send(flag, rid, path, body)
	return nil
}

// broadcast 全服广播
func broadcast(c *cosrpc.Context) any {
	path := c.GetMetadata(gwcfg.ServiceMessagePath)
	//logger.Debug("广播消息:%v", path)

	ignore := c.GetMetadata(gwcfg.ServiceMessageIgnore)
	ignoreMap := make(map[string]struct{})
	if ignore != "" {
		arr := strings.Split(ignore, ",")
		for _, v := range arr {
			ignoreMap[v] = struct{}{}
		}
	}
	mate := values.Metadata(c.Metadata())
	flag := message.Flag(mate.GetInt32(gwcfg.ServiceResponseFlag))
	flag.Set(message.FlagBroadcast)

	var err error
	body := c.Bytes()
	if Setting.Response != nil {
		ctx := NewContextWithSocket(&path, &flag, mate, nil)
		body, err = Setting.Response(ctx, body)
	}
	if err != nil {
		return err
	}

	players.Range(func(p *session.Data) bool {
		uid := p.GetString(gwcfg.ServiceMetadataUID)
		if _, ok := ignoreMap[uid]; ok {
			return true
		}
		//CookiesUpdate(mate, p)
		//Emitter.emit(EventTypeBroadcast, p, path, nil)
		if sock := players.Socket(p); sock != nil {
			_ = sock.Send(flag, 0, path, body, false)
		}
		return true
	})
	return nil
}
