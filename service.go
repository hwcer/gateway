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

var Service = server.Service(gwcfg.ServiceTypeGate)

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

// Deprecated: 用 send。定位规则已经统一——socketId 与 GUID 二选一，socketId 优先，
// write 只是"只有 socketId"的那个特例，没有必要单独存在。保留只为兼容既有调用方。
func write(c *cosrpc.Context) any {
	return send(c)
}

// send 消息推送。
//
// **定位优先级：socketId > GUID > UID**。socketId 定位连接（见 resolveTarget）；
// 会话定位在本函数：GUID 直查会话表，定位不到或只有 UID 时经全局映射反查 GUID
// （长链接绑定的是 GUID，一个账号可能有多个角色，映射保证落在当前在线的角色上）。
// 按 GUID 定位到会话且带了 UID 时会用 UID 校验归属——顶号/换角后同一 GUID 上挂的
// 可能已经是另一个 uid，不校验就是发错人；只带 socketId 的那种（典型是登录接口
// 本身，那时还没有会话）直接投给那条连接。
func send(c *cosrpc.Context) any {
	uid := c.GetMetadata(gwcfg.ServiceMetadataUID)
	guid := c.GetMetadata(gwcfg.ServiceMetadataGUID)
	path := c.GetMetadata(gwcfg.ServiceMessagePath)
	mate := values.Metadata(c.Metadata())

	var socketId uint64
	if v := c.GetMetadata(gwcfg.ServiceMetadataSocketId); v != "" {
		socketId, _ = strconv.ParseUint(v, 10, 64) //解析失败按 0 处理,退化成纯 GUID 推送
	}

	// **GUID 是用来更新用户信息的，不是用来定位连接的**（定位见 resolveTarget，socketId 优先）。
	// 所以会话取不到并不代表这条消息发不出去：登录途中、会话刚被清理，只要 socketId 指的
	// 那条连接还在，照投——早先这里 p==nil 就直接丢弃，等于让 socketId 优先这条规则失效。
	var p *session.Data
	if guid != "" {
		p = players.Get(guid)
	}
	if p == nil && uid != "" {
		//UID 反查:优先级排在 GUID 之后,定位不到会话再经全局映射换算
		p = players.Get(players.GUID(uid))
	}
	if p != nil {
		//顶号/换角色后同一 GUID 上挂的可能已经是另一个 uid,不校验就是发错人
		if uid != "" {
			if id := p.GetString(gwcfg.ServiceMetadataUID); id != "" && id != uid {
				logger.Debug("用户UID不匹配,UID:%s GUID:%s", uid, guid)
				return nil
			}
		}
		if _, ok := mate[gwcfg.ServicePlayerLogout]; ok {
			players.Delete(p)
			return nil
		}
		//会话数据该更新还是要更新,与这条消息最终投给谁无关(path 为空时更是"只设置信息,不发送")
		CookiesUpdate(mate, p, 0)
	}

	sock := resolveTarget(p, socketId)
	if sock == nil {
		logger.Debug("长链接不在线,消息丢弃,UID:%s GUID:%s Socket:%d PATH:%s ", uid, guid, socketId, path)
		return nil
	}
	return deliver(c, sock)
}

// resolveTarget 定位推送目标连接：**socketId 与会话二选一，socketId 优先**。
// （会话本身由 send 按 GUID/UID 定位，UID 走全局映射反查；这里只管"会话 → 连接"）
//
//	p        按 GUID/UID 找到的会话，可为 nil
//	socketId 发起这次请求的连接 id，0 表示"不是请求驱动的推送"
//
// 规则背后是一条不变量：**请求驱动的推送必须回到发起它的那条连接**。
//
// Forward 每次转发都带上 socketId，业务服推消息时原样带回。认 socketId 而不是回落到
// GUID，代次隔离就是白送的：顶号或重连之后那条连接要么还在（顶号存活期内它 Closing
// 但仍可写，推送与确认包一起回到老连接）、要么已经销毁（投不出去，丢弃）——
// **绝不会改投到新端**。按 GUID 投才会：上一代连接的数据推给刚上来的另一个人，
// 而那次请求的确认包走的是请求自己的 socket，一次响应被劈成两半，两头都拿不全。
// 这正是顶号时"取 ROLE 信息只收到一半"的根因。
//
// ⚠️ 必须查**网关自己的 Sockets 实例**，不能用包级 cosnet.Get —— 那个只查 cosnet.Default。
// 一旦 NewTCPServer 改用 cosnet.New() 另起实例(现在就是)，两个池子不相通、恒查不到，
// 症状只有一行 Debug「长链接不在线,消息丢弃」，而客户端明明连着，极难往"查错池子"上想。
func resolveTarget(p *session.Data, socketId uint64) *cosnet.Socket {
	if socketId != 0 {
		if sock := TCP.Get(socketId); sock != nil && sock.CanWrite() {
			return sock
		}
		return nil //认死这条连接,不回落 GUID
	}
	if p != nil {
		return players.Socket(p) //主动推送(定时器/AOI/跨玩家):投给会话当前的连接
	}
	return nil
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
		ctx := newSenderContext(sock, path, body, flag, mate)
		if err := h.Response(ctx, "", ""); err != nil {
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
		ctx := newSenderContext(nil, path, body, flag, mate)
		if err = h.Response(ctx, "", ""); err != nil {
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
