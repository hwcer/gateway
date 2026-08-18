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
	// ⚠️ 必须查**网关自己的 Sockets 实例**，不能用包级 cosnet.Get —— 那个只查 cosnet.Default。
	// 一旦 NewTCPServer 改用 cosnet.New() 另起实例(现在就是)，两个池子不相通、恒查不到，
	// 症状只有一行 Debug「长链接不在线,消息丢弃」，而客户端明明连着，极难往"查错池子"上想。
	sock := TCP.Get(i)
	if sock == nil {
		// socket id 失效通常是"刚好掉线/重连"——旧连接已销毁，人可能带着新 socket 回来了。
		// 退回按 GUID 推送(session 上存的是 socket 对象,顶号/重连后自动跟到新连接)，
		// 真不在线由 send 自己记录。
		logger.Debug("Socket已失效,尝试改用GUID推送,Socket:%s PATH:%s ", id, path)
		return send(c)
	}
	return deliver(c, sock)
}

// send 消息推送
func send(c *cosrpc.Context) any {
	uid := c.GetMetadata(gwcfg.ServiceMetadataUID)
	guid := c.GetMetadata(gwcfg.ServiceMetadataGUID)

	p := players.Get(guid) //guid 为空时必然取不到,不必单独判空
	if p == nil {
		logger.Debug("用户不在线,消息丢弃,UID:%s GUID:%s", uid, guid)
		return nil
	}
	//顶号/换角色后同一 GUID 上挂的可能已经是另一个 uid,不校验就是发错人
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
	CookiesUpdate(mate, p, 0) //path 为空时也要更新:那种请求就是"仅仅设置信息,不需要发送"
	return deliver(c, sock)
}

// deliver 把消息投递到**指定连接**：过一遍业务 Response 钩子(加密/改包/改 flag),再带上请求号发出。
// 只管发,不管"这条连接怎么找到的"——定位逻辑在 write(按 socket id)/send(按 GUID)里。
func deliver(c *cosrpc.Context, sock *cosnet.Socket) any {
	path := c.GetMetadata(gwcfg.ServiceMessagePath)
	if path == "" {
		return nil //仅仅设置信息，不需要发送
	}
	mate := values.Metadata(c.Metadata())
	flag := message.Flag(mate.GetInt32(gwcfg.ServiceResponseFlag))
	body := c.Bytes()
	if h, ok := Setting.Handler.(Response); ok {
		//data 传 nil 即可:socketContext.Session() 取不到 data 会自动回落 sock.Data()
		ctx := newSocketContext(sock, nil, path, 0, body, flag, mate)
		if err := h.Response(ctx); err != nil {
			return err
		}
		flag = ctx.Flag()
		body, _ = ctx.Buffer()
	}
	rid := mate.GetInt32(gwcfg.ServiceMetadataRequestId)
	_ = sock.Send(flag, rid, path, body)
	return nil
}

// broadcast 全服广播
func broadcast(c *cosrpc.Context) any {
	defer func() {
		if e := recover(); e != nil {
			logger.Alert("broadcast panic: %v", e)
		}
	}()
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
	flag.Set(message.FlagNoreply)
	flag.Set(message.FlagBroadcast)

	var err error
	body := c.Bytes()
	if h, ok := Setting.Handler.(Response); ok {
		ctx := newSocketContext(nil, nil, path, 0, body, flag, mate)
		if err = h.Response(ctx); err != nil {
			return err
		}
		flag = ctx.Flag()
		body, _ = ctx.Buffer()
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
