package gateway

import (
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
)

// reconnect 断线重连处理，参数是协议无关的 Request，长短连接共用。
//
// 默认实现：拿包体当 secret 验会话，回包是"服务端已处理的最大请求号"，供客户端对账
// 断线瞬间在飞的业务包（未处理的丢弃、已处理的重登录拉权威数据）。
//
// ⚠️ 默认实现要绑定 socket，短连接没有 socket 可绑，走不到这条路。HTTP 若要支持重连，
// 由业务 Handler 实现 C2SReconnect 自己定义语义（比如只验 secret、只回 handledIndex）
// ——这正是接口参数用 Request 而不是 *cosnet.Context 的原因。
func reconnect(r Request) any {
	if h, ok := Setting.Handler.(C2SReconnect); ok {
		return h.C2SReconnect(r) //业务 Handler 实现则优先
	}
	body, err := r.Buffer()
	if err != nil {
		return err
	}
	secret := string(body)
	if secret == "" {
		return values.Error("secret empty")
	}
	sock := r.Socket()
	if sock == nil {
		return values.Error("reconnect requires a socket")
	}
	p, e := players.Reconnect(sock, secret)
	if e != nil {
		return e
	}
	return p.GetInt32(gwcfg.ServiceMetadataRequestId)
}
