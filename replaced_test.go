package gateway

import (
	"net"
	"strings"
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/tcp"
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

// loginTestPlayer 建一个已登录、且绑定了长连接的玩家（走与 gate_tcp 相同的两步）。
func loginTestPlayer(t *testing.T, ss *cosnet.Sockets, guid string) (*cosnet.Socket, func()) {
	t.Helper()
	sock, stop := newReplacedTestSocket(t, ss)
	if err := negotiate(guid, sock.RemoteAddr().String(), sock); err != nil {
		t.Fatalf("first login must not be rejected:%v", err)
	}
	if _, err := players.Connect(sock, guid, values.Values{}); err != nil {
		t.Fatalf("first login must succeed:%v", err)
	}
	return sock, stop
}

func newTestSockets() *cosnet.Sockets {
	session.Options.Storage = session.NewMemory(16)
	//⚠️ 必须用**网关自己的**实例:resolveTarget 里的 TCP.Get 查的就是它。
	//另起一个 cosnet.New() 的话两个池子不相通、恒查不到——与 write 注释里那个坑同源,
	//而且症状一样隐蔽:推送全被判成"原连接已失效"然后静默丢弃。
	TCP.Sockets.Options.Heartbeat = 0 //不启动 daemon，倒计时由测试自己判断
	return TCP.Sockets
}

// TestNegotiateForceReplace 强制顶号：新端立即上线，老连接照样收到通知并进入存活期。
//
// 🔴 钉的是"两种策略下老连接的处置完全一样"——强制模式**不是**裸踢：
// 老连接同样只收不发、同样有存活期，在途确认包才发得完；区别只在新端等不等它。
func TestNegotiateForceReplace(t *testing.T) {
	old := Setting.ForceReplace
	defer func() { Setting.ForceReplace = old }()
	Setting.ForceReplace = true

	ss := newTestSockets()
	os, stop := loginTestPlayer(t, ss, "guid-force")
	defer stop()

	ns, stop2 := newReplacedTestSocket(t, ss)
	defer stop2()
	if err := negotiate("guid-force", ns.RemoteAddr().String(), ns); err != nil {
		t.Fatalf("强制顶号下新端必须能直接上线,拿到:%v", err)
	}
	if _, err := players.Connect(ns, "guid-force", values.Values{}); err != nil {
		t.Fatalf("connect error:%v", err)
	}

	if os.CanRead() {
		t.Fatal("老连接必须停止受理新请求")
	}
	if !os.CanWrite() {
		t.Fatal("老连接必须仍然可写,否则在途回包全丢")
	}
	if os.Countdown() <= 0 {
		t.Fatalf("老连接应处于存活期,Countdown=%d", os.Countdown())
	}
	//会话必须已经指向新连接,否则推送还会投给正在退场的老连接
	if p := players.Get("guid-force"); p == nil || !players.Socket(p).Is(ns) {
		t.Fatal("强制顶号后会话必须指向新连接")
	}
}

// TestNegotiateWaitReplace 协商顶号：新端被拒并拿到剩余秒数，会话仍指向老连接。
func TestNegotiateWaitReplace(t *testing.T) {
	old := Setting.ForceReplace
	defer func() { Setting.ForceReplace = old }()
	Setting.ForceReplace = false

	ss := newTestSockets()
	os, stop := loginTestPlayer(t, ss, "guid-wait")
	defer stop()

	ns, stop2 := newReplacedTestSocket(t, ss)
	defer stop2()
	err := negotiate("guid-wait", ns.RemoteAddr().String(), ns)
	if err == nil {
		t.Fatal("协商模式下新端本次登录必须被拒")
	}
	msg, ok := err.(*values.Message)
	if !ok {
		t.Fatalf("错误类型必须是 *values.Message,拿到 %T", err)
	}
	if msg.Code != session.ErrorSessionReplaced.Code {
		t.Fatalf("错误码 = %d, want %d", msg.Code, session.ErrorSessionReplaced.Code)
	}
	//🔴 参数走 Args,顺序 [剩余秒数, 在线端IP]。判据落在**值**上：
	//只断言 len(Args)==2 抓不到"两个 IP 搞反了"这一类退化 —— 发给新端的必须是
	//老连接的地址,不是新端自己的。
	if len(msg.Args) != 2 {
		t.Fatalf("Args = %v, want [剩余秒数, 在线端IP]", msg.Args)
	}
	if sec := values.ParseInt32(msg.Args[0]); sec <= 0 {
		t.Fatalf("必须带上剩余秒数供新端定时重试,拿到 %v", msg.Args[0])
	}
	wantIP := os.RemoteAddr().String()
	if i := strings.Index(wantIP, ":"); i > 0 {
		wantIP = wantIP[:i]
	}
	if got, _ := msg.Args[1].(string); got != wantIP {
		t.Fatalf("在线端IP = %q, want %q(老连接的地址,不是新端自己的)", got, wantIP)
	}
	//🔴 关键:会话不能被换走。换走了就会出现"推送投给新连接、确认包丢在老连接"的割裂
	if p := players.Get("guid-wait"); p == nil || !players.Socket(p).Is(os) {
		t.Fatal("协商模式下会话必须仍然指向老连接")
	}

	//新端反复重试不得刷新倒计时,否则老连接被无限续命、新端永远上不来
	before := os.Countdown()
	if err = negotiate("guid-wait", ns.RemoteAddr().String(), ns); err == nil {
		t.Fatal("重试仍应被拒")
	}
	if after := os.Countdown(); after != before {
		t.Fatalf("重试刷新了倒计时:%d -> %d", before, after)
	}
}

// TestNegotiateNoIncumbent 无人占用时两种策略都必须放行。
func TestNegotiateNoIncumbent(t *testing.T) {
	old := Setting.ForceReplace
	defer func() { Setting.ForceReplace = old }()

	newTestSockets()
	for _, force := range []bool{true, false} {
		Setting.ForceReplace = force
		if err := negotiate("guid-absent", "127.0.0.1:1", nil); err != nil {
			t.Fatalf("ForceReplace=%v 时无人占用仍被拒:%v", force, err)
		}
	}
}

// TestResolveTargetKeepsResponseWhole 请求驱动的推送必须回到发起它的那条连接。
//
// 🔴 钉的是顶号时"取 ROLE 信息只收到一半"的根因：业务服先推数据、再回确认包，
// 推送走 GUID（顶号后已指向新端）、确认包走请求自己的 socket（老端），
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

// TestResolveTargetNeverRedirects 原连接已销毁时必须丢弃，**绝不回落 GUID 改投新连接**。
func TestResolveTargetNeverRedirects(t *testing.T) {
	ss := newTestSockets()
	ns, stop := newReplacedTestSocket(t, ss)
	defer stop()

	//一个从不存在的 socket id：等价于原连接已经销毁。
	//即便按 GUID 能找到 ns，也不许改投给它。
	if got := resolveTarget(nil, ns.Id()+9999); got != nil {
		t.Fatalf("原连接已失效时必须丢弃,却投给了 socket %d", got.Id())
	}
}

// TestResolveTargetByGuid 主动推送（定时器/AOI/跨玩家）没有 socketId，按会话投递。
func TestResolveTargetByGuid(t *testing.T) {
	ss := newTestSockets()
	sock, stop := loginTestPlayer(t, ss, "guid-resolve")
	defer stop()

	p := players.Get("guid-resolve")
	if got := resolveTarget(p, 0); got == nil || !got.Is(sock) {
		t.Fatal("无 socketId 时应投给会话当前的连接")
	}
	if resolveTarget(nil, 0) != nil {
		t.Fatal("两者都没有时应丢弃")
	}
}

// TestResolveTargetByUID 只有UID时经 UID->GUID 全局映射反查定位会话
// （定位优先级 socketId > GUID > UID，见 send）。
func TestResolveTargetByUID(t *testing.T) {
	ss := newTestSockets()
	sock, stop := newReplacedTestSocket(t, ss)
	defer stop()

	guid := "guid-by-uid"
	//登录值带uid:Login 会顺带建立 UID->GUID 映射
	if _, err := players.Connect(sock, guid, values.Values{gwcfg.ServiceMetadataUID: "3001"}); err != nil {
		t.Fatalf("login error:%v", err)
	}
	defer players.Delete(players.Get(guid))

	//UID 反查链路:UID -> GUID -> 会话 -> 连接
	p := players.Get(players.GUID("3001"))
	if p == nil {
		t.Fatal("UID 反查映射未建立")
	}
	if got := resolveTarget(p, 0); got == nil || !got.Is(sock) {
		t.Fatal("按 UID 定位会话后应投给其当前连接")
	}
	if players.GUID("9999") != "" {
		t.Fatal("不在线的UID不应有映射")
	}
}

// TestSendPrefersSocketOverSession GUID 取不到会话时，只要 socketId 指的连接还在就该照投。
//
// 🔴 钉的是分工：**socketId 定位连接，GUID 只用来更新用户信息**。
// 早先 players.Get 取不到就直接丢弃，等于让"socketId 优先"这条规则在登录途中失效。
func TestSendPrefersSocketOverSession(t *testing.T) {
	ss := newTestSockets()
	sock, stop := newReplacedTestSocket(t, ss)
	defer stop()

	//p 为 nil 模拟"会话取不到"（登录途中 / 刚被清理）
	if got := resolveTarget(nil, sock.Id()); got == nil || !got.Is(sock) {
		t.Fatal("会话取不到但连接还在时,必须按 socketId 投递")
	}
}
