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

// Deprecated: 用 send。定位规则已经统一——socketId 与会话二选一，socketId 优先，
// write 只是"只有 socketId"的那个特例，没有必要单独存在。保留只为兼容既有调用方。
func write(c *cosrpc.Context) any {
	return send(c)
}

// send 消息推送。
//
// **定位优先级：socketId > UID**。socketId 定位连接（见 resolveTarget）；
// 会话按 UID 直查会话表——表键就是 uid，定位不到即角色不在线。
// 只带 socketId 的那种（典型是登录/选角接口本身，那时还没有 uid）直接投给那条连接。
// GUID 只是随行的业务身份信息，不参与定位。
func send(c *cosrpc.Context) any {
	uid := c.GetMetadata(gwcfg.ServiceMetadataUID)
	path := c.GetMetadata(gwcfg.ServiceMessagePath)
	mate := values.Metadata(c.Metadata())

	var socketId uint64
	if v := c.GetMetadata(gwcfg.ServiceMetadataSocketId); v != "" {
		socketId, _ = strconv.ParseUint(v, 10, 64) //解析失败按 0 处理,退化成纯会话推送
	}

	// 会话取不到并不代表这条消息发不出去：登录途中、会话刚被清理，只要 socketId 指的
	// 那条连接还在，照投——早先这里 p==nil 就直接丢弃，等于让 socketId 优先这条规则失效。
	var p *session.Data
	if uid != "" {
		p = players.Get(uid)
	}
	if p != nil {
		if _, ok := mate[gwcfg.ServicePlayerLogout]; ok {
			players.Delete(p)
			return nil
		}
		//会话数据该更新还是要更新,与这条消息最终投给谁无关(path 为空时更是"只设置信息,不发送")
		CookiesUpdate(mate, p, 0)
	}

	sock := resolveTarget(p, socketId)
	if sock == nil {
		logger.Debug("长链接不在线,消息丢弃,UID:%s Socket:%d PATH:%s ", uid, socketId, path)
		return nil
	}
	return deliver(c, sock)
}

// resolveTarget 定位推送目标连接：**socketId 与会话二选一，socketId 优先**。
// （会话本身由 send 按 UID 定位；这里只管"会话 → 连接"）
//
//	p        按 UID 找到的会话，可为 nil
//	socketId 发起这次请求的连接 id，0 表示"不是请求驱动的推送"
//
// 规则背后是一条不变量：**请求驱动的推送必须回到发起它的那条连接**。
//
// Forward 每次转发都带上 socketId，业务服推消息时原样带回。认 socketId 而不是回落到
// 会话，代次隔离就是白送的：顶号或重连之后那条连接要么还在（存活期内它 Closing
// 但仍可写，推送与确认包一起回到老连接）、要么已经销毁（投不出去，丢弃）——
// **绝不会改投到新端**。按会话投才会：上一代连接的数据推给刚上来的另一个人，
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
// 只管发,不管"这条连接怎么找到的"——定位逻辑在 send(按 socketId/uid)里。
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

	// 只发**已选角**的会话:广播都是玩法/运营语义,未选角的连接(还停在登录/选角界面)
	// 收到也没有意义——会话表(键=uid)就是"在线角色"集合,遍历它正合适。
	players.Range(func(p *session.Data) bool {
		if _, ok := ignoreMap[p.GetString(gwcfg.ServiceMetadataUID)]; ok {
			return true
		}
		if sock := players.Socket(p); sock != nil {
			_ = sock.Send(flag, 0, path, body, false)
		}
		return true
	})
	return nil
}
