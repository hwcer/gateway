package gateway

import (
	"net"
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/tcp"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
)

// newReplacedTestSocket 造一条真实可用的服务端连接（对端不读不写，只保证 conn 不为 nil）。
func newReplacedTestSocket(t *testing.T, ss *cosnet.Sockets) (*cosnet.Socket, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error:%v", err)
	}
	done := make(chan net.Conn, 1)
	go func() {
		if c, e := ln.Accept(); e == nil {
			done <- c
		}
	}()
	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial error:%v", err)
	}
	sock, err := ss.Create(tcp.NewConn(<-done))
	if err != nil {
		t.Fatalf("create error:%v", err)
	}
	return sock, func() {
		_ = client.Close()
		_ = ln.Close()
	}
}

// loginTestPlayer 建一个已登录、且绑定了长连接的玩家（走与 gate_tcp 相同的 Connect）。
func loginTestPlayer(t *testing.T, ss *cosnet.Sockets, guid string) (*cosnet.Socket, *session.Data, func()) {
	t.Helper()
	sock, stop := newReplacedTestSocket(t, ss)
	if _, err := players.Connect(sock, guid, values.Values{}); err != nil {
		t.Fatalf("login must succeed:%v", err)
	}
	p := sock.Data()
	return sock, p, stop
}

func newTestSockets() *cosnet.Sockets {
	session.Options.Storage = session.NewMemory(16)
	//⚠️ 必须用**网关自己的**实例:resolveTarget 里的 TCP.Get 查的就是它。
	//另起一个 cosnet.New() 的话两个池子不相通、恒查不到——与 write 注释里那个坑同源,
	//而且症状一样隐蔽:推送全被判成"原连接已失效"然后静默丢弃。
	TCP.Sockets.Options.Heartbeat = 0 //不启动 daemon，倒计时由测试自己判断
	return TCP.Sockets
}

// TestSelectTakeover UID 级顶号：同角色第二条连接在**选角落地**时强制接管。
//
// 🔴 钉的是新顶号语义的完整闭环：
//   - 登录阶段不踢任何人（同账号多角色并行是合法状态），占用在选角回包落地时才处理
//   - 老连接进入"只收不发"存活期：在途回包照常送达（仍可写），新请求被拒（不可读）
//   - 老会话被 supersede：uid 清空、频道身份释放——随后的断开/清理不再以该角色行动
//   - 隔离：老会话事后断开/清理，不得误删新会话的表项与频道成员
func TestSelectTakeover(t *testing.T) {
	ss := newTestSockets()
	sockA, pa, stopA := loginTestPlayer(t, ss, "guid-takeover")
	defer stopA()
	const uid = "8001"
	players.Update(pa, values.Values{gwcfg.ServiceMetadataUID: uid})
	if players.Get(uid) != pa {
		t.Fatalf("选角后应入表")
	}

	name, value := "takeover", "room1"
	channel.Join(pa, name, value)
	defer channel.Delete(name, value)

	//第二条连接:同账号同角色,选角落地 → 接管
	sockB, pb, stopB := loginTestPlayer(t, ss, "guid-takeover")
	defer stopB()
	players.Update(pb, values.Values{gwcfg.ServiceMetadataUID: uid})

	if players.Get(uid) != pb {
		t.Fatal("接管后表项必须指向新会话")
	}
	if got := pa.GetString(gwcfg.ServiceMetadataUID); got != "" {
		t.Fatalf("被接管的老会话 uid 必须清空,拿到 %q", got)
	}
	if _, ok := channel.NewSetter(pa).Get(name); ok {
		t.Fatal("被接管的老会话频道身份未释放")
	}
	//老连接存活期:不能读(新请求被拒)、还能写(在途回包发完)
	if sockA.CanRead() {
		t.Fatal("老连接必须停止受理新请求")
	}
	if !sockA.CanWrite() {
		t.Fatal("老连接必须仍然可写,否则在途回包全丢")
	}
	if sockA.Countdown() <= 0 {
		t.Fatalf("老连接应处于存活期,Countdown=%d", sockA.Countdown())
	}
	//新连接正常工作
	if !sockB.CanRead() || !sockB.CanWrite() {
		t.Fatal("新连接不应受限")
	}

	//隔离:新会话入频道;老会话随后走完整下线流程,不得波及
	channel.Join(pb, name, value)
	if n := channelMemberCount(name, value); n != 1 {
		t.Fatalf("新会话入房后成员数=%d, want 1", n)
	}
	if err := players.Disconnect(sockA); err != nil { //老连接断开事件
		t.Fatalf("disconnect error:%v", err)
	}
	channel.Release(pa) //会话清理(setting.release 同款两连)
	players.Delete(pa)

	if players.Get(uid) != pb {
		t.Fatal("老会话清理误摘了新会话的表项")
	}
	if n := channelMemberCount(name, value); n != 1 {
		t.Fatalf("老会话清理波及了新会话的频道成员,成员数=%d, want 1", n)
	}
}

// TestReconnectKeepsRole 闪断重连（secret 路径）：角色未被顶，重连即恢复原状。
// 存储还原的会话对象在 Redis 后端下与表里的是两个实例——rebind 幂等入表兜住这一点。
func TestReconnectKeepsRole(t *testing.T) {
	ss := newTestSockets()
	_, pa, stopA := loginTestPlayer(t, ss, "guid-reconnect")
	defer stopA()
	const uid = "8002"
	players.Update(pa, values.Values{gwcfg.ServiceMetadataUID: uid})

	secret, err := session.New(pa).Refresh()
	if err != nil {
		t.Fatalf("refresh error:%v", err)
	}

	sockC, stopC := newReplacedTestSocket(t, ss)
	defer stopC()
	if _, err := players.Reconnect(sockC, secret); err != nil {
		t.Fatalf("reconnect error:%v", err)
	}
	pc := sockC.Data()
	if pc == nil {
		t.Fatal("重连后必须绑定会话")
	}
	if players.Get(uid) != pc {
		t.Fatal("重连后表项必须指向重连会话")
	}
}

// TestReconnectAfterTakeover 断线期间角色被别的会话接管 → 持 secret 重连
// **不得悄悄夺回角色**：supersede 已清掉本会话的 uid，重连落地为未选角状态，
// 要不要回到该角色由玩家重新选角、经业务侧占用判定决定（同账号两台设备
// 争同一角色的拉锯不该由一条重连秘钥决定）。
func TestReconnectAfterTakeover(t *testing.T) {
	ss := newTestSockets()
	_, pa, stopA := loginTestPlayer(t, ss, "guid-reconnect2")
	defer stopA()
	const uid = "8004"
	players.Update(pa, values.Values{gwcfg.ServiceMetadataUID: uid})

	secret, err := session.New(pa).Refresh()
	if err != nil {
		t.Fatalf("refresh error:%v", err)
	}

	_, pb, stopB := loginTestPlayer(t, ss, "guid-reconnect2")
	defer stopB()
	players.Update(pb, values.Values{gwcfg.ServiceMetadataUID: uid}) //B接管
	if players.Get(uid) != pb {
		t.Fatal("前提:B应已接管")
	}

	//A 持 secret 重连:会话还原,但角色已被接管——不得 displacing B
	sockC, stopC := newReplacedTestSocket(t, ss)
	defer stopC()
	if _, err := players.Reconnect(sockC, secret); err != nil {
		t.Fatalf("reconnect error:%v", err)
	}
	pc := sockC.Data()
	if pc == nil {
		t.Fatal("重连后必须绑定会话")
	}
	if players.Get(uid) != pb {
		t.Fatal("重连不得夺回已被接管的角色")
	}
	if got := pc.GetString(gwcfg.ServiceMetadataUID); got != "" {
		t.Fatalf("被接管过的会话重连后应为未选角状态,拿到 %q", got)
	}
	//老会话(pa)的 uid 必须仍为空——内存后端下重连还原的就是它
	if pa.GetString(gwcfg.ServiceMetadataUID) != "" {
		t.Fatal("被接管的会话 uid 不应复活")
	}
}

// TestResolveTargetKeepsResponseWhole 请求驱动的推送必须回到发起它的那条连接。
//
// 🔴 钉的是顶号时"取 ROLE 信息只收到一半"的根因：业务服先推数据、再回确认包，
// 推送走会话（顶号后已指向新端）、确认包走请求自己的 socket（老端），
// 一次响应被劈成两半，两头都拿不全。认 socketId 之后两者一起回到老连接。
func TestResolveTargetKeepsResponseWhole(t *testing.T) {
	ss := newTestSockets()
	os, stop := newReplacedTestSocket(t, ss)
	defer stop()

	//顶号：老连接进入存活期（Closing，仍可写）
	os.Replaced("10.0.0.1")
	if !os.CanWrite() {
		t.Fatal("老连接在存活期内必须仍可写")
	}
	//p 传 nil 模拟"会话已经指向别处"：带了 socketId 就该认它，不看会话
	if got := resolveTarget(nil, os.Id()); got == nil || !got.Is(os) {
		t.Fatal("带 socketId 时必须投给那条连接")
	}
}

// TestResolveTargetNeverRedirects 原连接已销毁时必须丢弃，**绝不回落会话改投新连接**。
func TestResolveTargetNeverRedirects(t *testing.T) {
	ss := newTestSockets()
	ns, stop := newReplacedTestSocket(t, ss)
	defer stop()

	//一个从不存在的 socket id：等价于原连接已经销毁。
	//即便按会话能找到 ns，也不许改投给它。
	if got := resolveTarget(nil, ns.Id()+9999); got != nil {
		t.Fatalf("原连接已失效时必须丢弃,却投给了 socket %d", got.Id())
	}
}

// TestResolveTargetBySession 主动推送（定时器/AOI/跨玩家）没有 socketId，按会话投递。
func TestResolveTargetBySession(t *testing.T) {
	ss := newTestSockets()
	sock, p, stop := loginTestPlayer(t, ss, "guid-resolve")
	defer stop()
	players.Update(p, values.Values{gwcfg.ServiceMetadataUID: "8003"})

	if got := players.Get("8003"); got != p {
		t.Fatal("选角后应能按 uid 取回会话")
	}
	if got := resolveTarget(p, 0); got == nil || !got.Is(sock) {
		t.Fatal("无 socketId 时应投给会话当前的连接")
	}
	if resolveTarget(nil, 0) != nil {
		t.Fatal("两者都没有时应丢弃")
	}
	if players.Get("9999") != nil {
		t.Fatal("不在线的角色不应有表项")
	}
}

// TestSendPrefersSocketOverSession 会话取不到时，只要 socketId 指的连接还在就该照投。
//
// 🔴 钉的是分工：**socketId 定位连接，uid 只用来查会话**。
// 早先 p 取不到就直接丢弃，等于让"socketId 优先"这条规则在登录途中失效。
func TestSendPrefersSocketOverSession(t *testing.T) {
	ss := newTestSockets()
	sock, stop := newReplacedTestSocket(t, ss)
	defer stop()

	//p 为 nil 模拟"会话取不到"（登录途中 / 刚被清理）
	if got := resolveTarget(nil, sock.Id()); got == nil || !got.Is(sock) {
		t.Fatal("会话取不到但连接还在时,必须按 socketId 投递")
	}
}
