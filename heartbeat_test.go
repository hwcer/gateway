package gateway

import (
	"testing"

	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/gwcfg"
)

// hbHandler 实现了 C2SHeartbeat 接口的业务 Handler
type hbHandler struct{ Default }

func (hbHandler) C2SHeartbeat(r Request) any { return nil }

// hbBadRouter 路由永远解析不出来的 Handler
type hbBadRouter struct{ Default }

func (hbBadRouter) Router(string, values.Metadata) (string, string, error) {
	return "", "", errors.ErrNotFount
}

// TestInitHeartbeat 启动期校正 Setting.G2SHeartbeat。
//
// 🔴 钉的是"缺服务名段要补上"：客户端心跳包名是 "heartbeat"（协议号推出来的，已去掉
// C2S_ 前缀并转小写），Router 解析不出 servicePath，直接拿它转发必然失败——而失败是
// 静默降级成网关应答的，现场只有"角色在业务服慢慢被判掉线"，没有任何线索指向心跳。
func TestInitHeartbeat(t *testing.T) {
	oh, og, oc := Setting.Handler, Setting.G2SHeartbeat, Setting.C2SHeartbeat
	defer func() { Setting.Handler, Setting.G2SHeartbeat, Setting.C2SHeartbeat = oh, og, oc }()
	Setting.Handler = Default{}

	for _, c := range []struct{ name, g2s, c2s, want string }{
		{"缺服务名段->补业务服名", "", "heartbeat", "/" + gwcfg.ServiceTypeGame + "/heartbeat"},
		{"已是完整路径->原样保留", "/game/heartbeat", "heartbeat", "/game/heartbeat"},
		{"显式配置优先于回落值", "/world/heartbeat", "heartbeat", "/world/heartbeat"},
		{"两者都空->不转发", "", "", ""},
	} {
		Setting.G2SHeartbeat, Setting.C2SHeartbeat = c.g2s, c.c2s
		initHeartbeat()
		if Setting.G2SHeartbeat != c.want {
			t.Errorf("%s: G2SHeartbeat = %q, want %q", c.name, Setting.G2SHeartbeat, c.want)
		}
	}

	//补完仍然路由不出来 -> 清空(不转发,退回网关自己应答)
	Setting.Handler = hbBadRouter{}
	Setting.G2SHeartbeat, Setting.C2SHeartbeat = "", "heartbeat"
	initHeartbeat()
	if Setting.G2SHeartbeat != "" {
		t.Errorf("路由解析不出来时必须置空,得到 %q", Setting.G2SHeartbeat)
	}

	//业务 Handler 自己接管了心跳，转发路径不该被改动
	Setting.Handler = hbHandler{}
	Setting.G2SHeartbeat, Setting.C2SHeartbeat = "", "heartbeat"
	initHeartbeat()
	if Setting.G2SHeartbeat != "" {
		t.Errorf("Handler 实现了 C2SHeartbeat 时不该校正转发路径,得到 %q", Setting.G2SHeartbeat)
	}
}
