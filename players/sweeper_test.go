package players

import (
	"net"
	"testing"
	"time"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/tcp"
)

// wireReleaseHook 单测环境没有 cosgo 生命周期,手动挂 release 钩子
// (生产由 setting.go 的 EventTypStarted 注册,行为一致)
func wireReleaseHook(t *testing.T) {
	t.Helper()
	session.On(session.EventSessionRelease, func(i any) {
		data, _ := i.(*session.Data)
		if data == nil {
			return
		}
		Delete(data)
	})
}

// 🔴 T1 回归:掉线会话 TTL 清理——TCP 掉线只解绑 socket 不产生 Release,
// Redis 后端唯一 Release 源是 HTTP logout,表项与频道成员会永久滞留

// 断线未超期:保留(重连窗口内)
func TestSweeperKeepsRecentOffline(t *testing.T) {
	setup(t)
	pa := newPlayer(t, "sw1")
	selectUID(pa, "9501")
	//无 socket 绑定 = 已断线
	sweeperLastOffline.Store(pa, time.Now().Add(-time.Second)) //1s 前断线

	orig := Sweeper.Grace
	Sweeper.Grace = 300
	defer func() { Sweeper.Grace = orig }()

	sweep()

	if Get("9501") != pa {
		t.Fatal("重连窗口内的断线会话应保留")
	}
}

// 断线超过 Grace:走标准 Release(表项摘除)
func TestSweeperReleasesExpiredOffline(t *testing.T) {
	setup(t)
	wireReleaseHook(t)
	pa := newPlayer(t, "sw2")
	selectUID(pa, "9502")
	sweeperLastOffline.Store(pa, time.Now().Add(-6*time.Minute))

	orig := Sweeper.Grace
	Sweeper.Grace = 300
	defer func() { Sweeper.Grace = orig }()

	sweep()

	if Get("9502") == pa {
		t.Fatal("超期的断线会话应被 Release 清理")
	}
	//release 钩子(生产为 setting.go 的 release)做的是"摘表项+关连接",
	//不清会话数据里的 uid 字段——断言表项归属即可
	if v, ok := players.Load("9502"); ok && v == any(pa) {
		t.Fatal("被清理会话的表项应已被摘除")
	}
}

// 在线会话(有 socket 绑定)不受影响
func TestSweeperIgnoresOnline(t *testing.T) {
	setup(t)
	ss := cosnet.New()
	sock, stop := newSweeperTestSocket(t, ss)
	defer stop()
	pa := newPlayer(t, "sw3")
	selectUID(pa, "9503")
	Replace(pa, sock) //绑定 socket = 在线

	sweeperLastOffline.Store(pa, time.Now().Add(-time.Hour)) //伪造很早的断线记录

	orig := Sweeper.Grace
	Sweeper.Grace = 300
	defer func() { Sweeper.Grace = orig }()

	sweep()

	if Get("9503") != pa {
		t.Fatal("在线会话不得被清理")
	}
	if _, ok := sweeperLastOffline.Load(pa); ok {
		t.Fatal("在线会话的断线记录应被清掉")
	}
}

// 伪装断线时刻数据完整性:负例——从未记录的断线会话第一轮只登记不清
func TestSweeperFirstSweepOnlyRegisters(t *testing.T) {
	setup(t)
	pa := newPlayer(t, "sw4")
	selectUID(pa, "9504")

	orig := Sweeper.Grace
	Sweeper.Grace = 300
	defer func() { Sweeper.Grace = orig }()

	sweep()
	if Get("9504") != pa {
		t.Fatal("首轮 sweep 只登记断线时刻,不得清理")
	}
	if _, ok := sweeperLastOffline.Load(pa); !ok {
		t.Fatal("首轮 sweep 应登记断线时刻")
	}
}

// newSweeperTestSocket 造一条真实可用的服务端连接(对端只建立不读写),
// Create 走真实状态机,socket 为 Connected=在线
func newSweeperTestSocket(t *testing.T, ss *cosnet.Sockets) (*cosnet.Socket, func()) {
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
