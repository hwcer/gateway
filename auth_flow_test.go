package gateway

import (
	"testing"

	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/gateway/players"
)

// secretProbe 包住业务 Handler 只记录 S2CSecret 是否下发
type secretProbe struct {
	Handler
	sent *bool
}

func (s secretProbe) S2CSecret(sock *cosnet.Socket, secret string) {
	*s.sent = true
}

// TestS2CSecretTwoPhase 秘钥只在 LOGIN(选角落地)时下发:
// 认证阶段 S2CSecret 必须跳过——否则会在未落库的伪会话上现生成一个
// 不落存储的废秘钥发给客户端,重连永远验不过。
func TestS2CSecretTwoPhase(t *testing.T) {
	ss := newTestSockets()
	sock, stop := newReplacedTestSocket(t, ss)
	defer stop()

	sent := false
	old := Setting.Handler
	Setting.Handler = secretProbe{old, &sent}
	defer func() { Setting.Handler = old }()

	p, err := players.Auth(sock, "g-secret", values.Values{})
	if err != nil {
		t.Fatalf("auth error:%v", err)
	}
	TCP.S2CSecret(sock, nil)
	if sent {
		t.Fatal("认证态会话不得下发秘钥")
	}

	if _, err := players.Login(sock, p); err != nil {
		t.Fatalf("login error:%v", err)
	}
	TCP.S2CSecret(sock, nil)
	if !sent {
		t.Fatal("LOGIN 后必须下发秘钥")
	}
}
