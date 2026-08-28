package gateway

import (
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/context"
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
func reconnect(c context.Context) any {
	if h, ok := Setting.Handler.(C2SReconnect); ok {
		return h.C2SReconnect(c) //业务 Handler 实现则优先
	}
	body, err := c.Buffer()
	if err != nil {
		return err
	}
	secret := string(body)
	if secret == "" {
		return values.Error("secret empty")
	}
	sock := c.Socket()
	if sock == nil {
		return values.Error("reconnect requires a socket")
	}
	p, e := players.Reconnect(sock, secret)
	if e != nil {
		return e
	}
	//回"已处理的最大请求号",供客户端对账断线瞬间在飞的业务包。
	//⚠️ 用 values.Message{Code,Data} 而不是裸 int32:它是框架层表达回包的通用结构
	//(与客户端 Cosnet 的 IS2CConfirm 同构),业务层的 Serialize 认得出这是"成功且带数据",
	//裸数字只能靠对方的 default 分支去猜。
	//也不能返回 []byte —— cosnet 对字节是直接发的、绕过 Serialize,客户端解不出 Code。
	return values.Message{Code: 0, Data: p.GetInt32(gwcfg.ServiceMetadataRequestId)}
}
