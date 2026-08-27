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
	sock, ok := resolveTarget(nil, i)
	if !ok {
		// ⚠️ **不退回 GUID**。socket id 失效意味着那条连接已经销毁，而按 GUID 找到的
		// 是顶号/重连之后的**另一条**连接——把上一代连接的回包投给它，等于把数据发错人。
		// 在飞包的正确处置是丢弃：客户端重连时按 handledIndex 对账（未处理的丢弃、
		// 已处理的重登录拉权威数据），本来就不指望这一份能送达。
		logger.Debug("Socket已失效,消息丢弃,Socket:%s PATH:%s ", id, path)
		return nil
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

	//会话数据该更新还是要更新,与这条消息最终投给谁无关(path 为空时更是"只设置信息,不发送")
	CookiesUpdate(mate, p, 0)

	var socketId uint64
	if v := c.GetMetadata(gwcfg.ServiceMetadataSocketId); v != "" {
		socketId, _ = strconv.ParseUint(v, 10, 64) //解析失败按 0 处理,退化成纯 GUID 推送
	}
	sock, ok := resolveTarget(players.Socket(p), socketId)
	if !ok {
		logger.Debug("长链接不在线,消息丢弃,UID:%s GUID:%s Socket:%d PATH:%s ", uid, guid, socketId, path)
		return nil
	}
	return deliver(c, sock)
}

// resolveTarget 决定一条推送投给哪条连接；ok=false 表示应当丢弃。
//
//	current  会话此刻指向的连接（按 GUID 找到的），可为 nil
//	socketId 发起这次请求的连接 id，0 表示"不是请求驱动的推送"
//
// 规则只有一条：**请求驱动的推送必须回到发起它的那条连接**。
//
// proxyRequest 每次转发都带上 socketId，业务服推消息时原样带回，于是这里能认出
// "会话已经换到别的连接上了"——顶号或重连之后按 GUID 投，就会把上一代连接的数据
// 推给刚上来的新端；而那次请求的确认包走的是请求自己的 socket，一次响应被劈成两半，
// 两头都拿不全。这正是顶号时"取 ROLE 信息只收到一半"的根因。
//
// 原连接还活着（典型是顶号存活期内，那时它 Closing 但仍可写）就投给它：推送与确认包
// 一起回到老连接，老客户端仍能拿到完整响应。已经销毁则丢弃——**绝不改投新连接**。
func resolveTarget(current *cosnet.Socket, socketId uint64) (*cosnet.Socket, bool) {
	if socketId == 0 {
		return current, current != nil //主动推送(定时器/AOI/跨玩家):投给当前连接
	}
	if current != nil && current.Id() == socketId {
		return current, true //没换代,最常见的路径
	}
	if os := TCP.Get(socketId); os != nil && os.CanWrite() {
		return os, true //换代了,但原连接还活着
	}
	return nil, false //原连接已销毁
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
