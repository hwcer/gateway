package gateway

import (
	"net"
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/tcp"
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
	ss := cosnet.New()
	ss.Options.Heartbeat = 0 //不启动 daemon，倒计时由测试自己判断
	return ss
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
	if sec, _ := msg.Data.(int32); sec <= 0 {
		t.Fatalf("必须带上剩余秒数供新端定时重试,拿到 %v", msg.Data)
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
