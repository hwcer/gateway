package players

import (
	"strings"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/gateway/gwcfg"
)

const (
	SessionPlayerSocketName = "player.sock"
)

func Socket(p *session.Data) *cosnet.Socket {
	i := p.Get(SessionPlayerSocketName)
	if i == nil {
		return nil
	}
	r, _ := i.(*cosnet.Socket)
	return r
}

// stripPort 去掉 host:port 里的端口，只留 IP。
func stripPort(addr string) string {
	if i := strings.Index(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

// Replace 把会话绑定到新连接；老连接（若还在）立即进入关闭流程。
//
// 走到这里时"该不该换人"已经判完了：token 登录阶段没有占用判断（登录不踢人，
// 见 players.Create），secret 重连则是同一个客户端自己回来、直接接管。所以对老连接
// 是**立即关**而不是发起协商——会话已经改指向新连接，老的那条再留着也收不到任何推送
// （send 按会话查到的已经是新 socket），纯僵尸，没有留一个协商期的意义。
//
// ⚠️ 这里只能用 Close 这种"纯状态切换"的操作。同步断开会 Emit 到 Disconnect，
// 那里还要再拿一次会话锁 —— 而这整段跑在 p.Mutex 里，sync.Mutex 不可重入，必死锁。
func Replace(p *session.Data, sock *cosnet.Socket) {
	os := Socket(p)
	p.Mutex(func(setter session.Setter) {
		var reconnect bool
		if os != nil && !os.Is(sock) {
			reconnect = true
			os.Close(0)
		}
		if sock != nil {
			setter.Set(SessionPlayerSocketName, sock)
			sock.Authentication(p, reconnect)
		}
	})
}

// Connect 新建**正式会话**并绑定到这条连接(WSS token 失效后的回退路径)。
// TCP 认证不走这里——那只绑 GUID(见 Auth),正式会话由选角回包落地时的
// Login 升级建立。
//
// 建会话阶段没有任何"占用"判断——同账号多角色并行是合法状态，
// 角色级顶号在选角回包落地时发生（见 rebind）。
func Connect(sock *cosnet.Socket, guid string, value values.Values) (data *session.Data, err error) {
	if _, data, err = Create(guid, value); err == nil {
		Replace(data, sock)
	}
	return
}

// Reconnect 断线重连（secret 路径）：**不走顶号协商**，直接接管。
//
// 持有 secret 就是同一个客户端实例自己回来了，不是别人来抢。而且闪断时老 socket 往往
// 还没被心跳判死（要等 SocketConnectTime），若这条路也排协商队，每次正常重连都得
// 等满协商期才能回到游戏——重连体验直接崩掉。token 登录协商、secret 重连直通，
// 两条路径本来就是分开的，天然可分。
func Reconnect(sock *cosnet.Socket, secret string) (data *session.Data, err error) {
	//已正式登录才视为"在线直接返回";认证态伪会话不算——继续走 secret 验证,
	//验过即整段接管(伪会话被 Replace 覆盖)
	if data = sock.Data(); data != nil && Logged(data) {
		return //已在线,直接返回现有会话,避免调用方对 nil 解引用
	}
	s := session.New()
	if err = s.Verify(secret); err != nil {
		return
	}
	_, err = s.Refresh() //刷线TOKEN
	data = s.Data
	Replace(data, sock)
	//会话已选角的要重新入表:存储还原的对象与表里的可能是两个实例(Redis 后端每次
	//Verify 都新建对象),内存后端下则是同一实例、表项本就在——rebind 幂等。
	//若断线期间角色已被接管,本会话的 uid 已被 supersede 清空(含存储),这里自然跳过:
	//重连落地为未选角状态,夺回角色须走重新选角的占用判定,而非秘钥说了算。
	if uid := data.GetString(gwcfg.ServiceMetadataUID); uid != "" {
		rebind(data, "", uid)
	}
	return
}

// Disconnect 连接断开。只有"会话此刻确实指向这条连接"才算玩家掉线。
//
// ⚠️ 掉线事件必须收在这个判断里面。老实现是无条件 Emit 的，靠 Socket.Replaced 把
// sock.data 置 nil（上面 data==nil 就 return）来挡住僵尸连接；现在协商流程要求保留 data，
// 那道闸门没了——重连接管后老连接断开时会误报一次掉线，而玩家正好好地连在新 socket 上。
//
// 反过来，顶号协商到期断开的那条**必须**报掉线：会话自始至终指向它，新端还没进来，
// 此刻玩家是真的离线了。两种情形的唯一区别就是这个 os.Is(sock)。
func Disconnect(sock *cosnet.Socket) (err error) {
	data := sock.Data()
	if data == nil {
		return
	}
	os := Socket(data)
	var offline bool
	data.Mutex(func(setter session.Setter) {
		if os != nil && os.Is(sock) {
			setter.Delete(SessionPlayerSocketName)
			offline = true
		}
	})
	if !offline {
		return //会话已改指向别的连接,这条断开与玩家在线状态无关
	}
	// 立即触发掉线事件（早于 Release 销毁事件）
	session.Emit(session.EventSessionDisconnect, data)
	return
}
