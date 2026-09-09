package players

import (
	"net"
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/tcp"
	"github.com/hwcer/gateway/gwcfg"
)

// newTestSocket 造一条真实可用的服务端连接(对端不读不写,只保证 conn 非 nil),
// 与 gateway 根包的 newReplacedTestSocket 同款——players 的测试要自己拿 socket 绑定。
func newTestSocket(t *testing.T) (*cosnet.Socket, func()) {
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
	sock, err := cosnet.New().Create(tcp.NewConn(<-done))
	if err != nil {
		t.Fatalf("create error:%v", err)
	}
	return sock, func() {
		_ = client.Close()
		_ = ln.Close()
	}
}

// TestAuthBindsGUIDOnly 认证成功只把 GUID 绑到 socket.Data:
// 不落存储、无秘钥、无角色;重复认证幂等、换账号被拒。
func TestAuthBindsGUIDOnly(t *testing.T) {
	session.Options.Storage = session.NewMemory(64)
	sock, stop := newTestSocket(t)
	defer stop()

	p, err := Auth(sock, "g1", values.Values{gwcfg.ServiceMetadataDeveloper: 1})
	if err != nil {
		t.Fatalf("auth error:%v", err)
	}
	if sock.Data() != p {
		t.Fatal("认证后伪会话必须绑到 socket.Data")
	}
	if p.GetString(gwcfg.ServiceMetadataGUID) != "g1" {
		t.Fatal("伪会话必须携带 GUID")
	}
	if Logged(p) {
		t.Fatal("认证不是 LOGIN:不得持有秘钥")
	}
	if p.GetString(gwcfg.ServiceMetadataUID) != "" {
		t.Fatal("认证态不得有角色")
	}
	if d, err := session.Options.Storage.Get(p.Id()); err == nil || d != nil {
		t.Fatal("认证态会话不得落存储")
	}

	if again, err := Auth(sock, "g1", nil); err != nil || again != p {
		t.Fatal("同一连接重复认证同账号必须幂等")
	}
	if _, err := Auth(sock, "g2", nil); err == nil {
		t.Fatal("同一连接换账号重登必须被拒")
	}
}

// TestSelectLandsLogin 选角回包落地 = LOGIN:伪会话升级为正式会话
// (落存储/绑定连接/认证期数据带走),uid 随之入表;伪会话本体永不入表。
func TestSelectLandsLogin(t *testing.T) {
	session.Options.Storage = session.NewMemory(64)
	sock, stop := newTestSocket(t)
	defer stop()

	p, err := Auth(sock, "g1", values.Values{gwcfg.ServiceMetadataDeveloper: 1})
	if err != nil {
		t.Fatalf("auth error:%v", err)
	}
	//oauth 回包的 cookies(无 uid)落在伪会话上,不得触发 LOGIN
	Update(p, values.Values{"tz": "8"})
	if Logged(p) {
		t.Fatal("无 uid 的回包不得触发 LOGIN")
	}

	//模拟 forward 的顺序:先 Login 升级,再 CookiesUpdate 落 uid
	real, err := Login(sock, p)
	if err != nil {
		t.Fatalf("login error:%v", err)
	}
	if !Logged(real) {
		t.Fatal("LOGIN 后必须是正式会话")
	}
	if sock.Data() != real {
		t.Fatal("正式会话必须接管 socket.Data")
	}
	if real.GetString(gwcfg.ServiceMetadataGUID) != "g1" {
		t.Fatal("GUID 必须带进正式会话")
	}
	if real.GetInt32(gwcfg.ServiceMetadataDeveloper) != 1 {
		t.Fatal("认证期数据(developer)必须带走")
	}
	if real.Get("tz") == nil {
		t.Fatal("oauth 回包的 cookies 必须带走")
	}

	Update(real, values.Values{gwcfg.ServiceMetadataUID: "9001"})
	if Get("9001") != real {
		t.Fatal("选角后正式会话必须入表")
	}

	//伪会话本体即使被直接塞 uid 也不得入表(rebind 的防御)
	Update(p, values.Values{gwcfg.ServiceMetadataUID: "9002"})
	if Get("9002") != nil {
		t.Fatal("认证态伪会话不得入表")
	}
}

// TestLoginSecretPersists LOGIN 落库的会话,秘钥必须在存储里(重连 Verify 的前提)
func TestLoginSecretPersists(t *testing.T) {
	session.Options.Storage = session.NewMemory(64)
	sock, stop := newTestSocket(t)
	defer stop()

	p, _ := Auth(sock, "g1", nil)
	real, err := Login(sock, p)
	if err != nil {
		t.Fatalf("login error:%v", err)
	}
	restored, err := session.Options.Storage.Get(real.Id())
	if err != nil || restored == nil {
		t.Fatalf("正式会话必须落存储:%v", err)
	}
	if restored.GetString(session.TokenSecretName) == "" {
		t.Fatal("存储里的会话必须带秘钥,否则重连 Verify 必失败")
	}
}
