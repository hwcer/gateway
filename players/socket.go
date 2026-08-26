package players

import (
	"strings"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/gateway/errors"
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

// Negotiate 顶号协商：老连接还活着时**不允许**新端直接上线。
//
//	返回 nil          没有活着的老连接，调用方继续登录即可
//	返回 ErrReplaced  已给老连接发出顶号通知并开始倒计时，本次登录被拒；
//	                  倒计时结束（或老连接自己退出）后再次登录即可直接上线
//
// 老连接在协商期内 **只收不发**：在途回包和服务器推送照常送达，它自己发来的新请求
// 被回 209（见 cosnet.Socket.CanRead/CanWrite）。会话自始至终指向老连接，
// 直到它真的断开才由 Disconnect 清空——新端不会中途接管，也就不会收到属于老连接的回包。
//
// ⚠️ 必须在 Login **之前**调用。Login 的 loaded 分支会 p.Update(value) 用新登录者的数据
// 覆盖老会话，还会 ss.Refresh() 强制旧 TOKEN 失效——顶号被拒却把老玩家的 secret 作废了，
// 他连断线重连都回不来，等于"不许顶号"反而把人踢得更彻底。
//
// ⚠️ 在会话锁**外面**调用 os.Replaced：它会 Emit 事件同步走到业务层的下发逻辑，
// 塞进 p.Mutex 里迟早撞上重入死锁。
func Negotiate(guid, ip string, sock *cosnet.Socket) error {
	p := Get(guid)
	if p == nil {
		return nil //从没登录过
	}
	os := Socket(p)
	if os == nil || !os.CanWrite() {
		return nil //老连接不存在或已经死了(CanWrite 覆盖 Connected|Closing,即"还活着")
	}
	if os.Is(sock) {
		return nil //就是这条连接自己在重复登录
	}
	if i := strings.Index(ip, ":"); i > 0 {
		ip = ip[:i]
	}
	os.Replaced(ip) //已在协商期内则内部返回 false：不重复通知，也不重置倒计时
	return errors.ErrReplaced(os.Countdown())
}

// Replace 把会话绑定到新连接；老连接（若还在）立即进入关闭流程。
//
// 走到这里时"该不该换人"已经判完了：token 登录由 Negotiate 挡在前面（老连接活着根本到不了这），
// secret 重连则是同一个客户端自己回来、直接接管。所以对老连接是**立即关**而不是发起协商——
// 会话已经改指向新连接，老的那条再留着也收不到任何推送（send 按 GUID 查到的已经是新 socket），
// 纯僵尸，没有留一个协商期的意义。
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

// Connect 长连接登录（token 路径），已在别处登录时走顶号协商、拒绝本次登录。
func Connect(sock *cosnet.Socket, guid string, value values.Values) (data *session.Data, err error) {
	if err = Negotiate(guid, sock.RemoteAddr().String(), sock); err != nil {
		return nil, err
	}
	if _, data, err = Login(guid, value); err == nil {
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
	if data = sock.Data(); data != nil {
		return //已在线,直接返回现有会话,避免调用方对 nil 解引用
	}
	s := session.New()
	if err = s.Verify(secret); err != nil {
		return
	}
	_, err = s.Refresh() //刷线TOKEN
	data = s.Data
	Replace(data, sock)
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
