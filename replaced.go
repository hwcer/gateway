package gateway

import (
	"github.com/hwcer/cosnet"
	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/players"
)

// negotiate 顶号处置：通知老连接，并按 Setting.ForceReplace 决定新端能不能立刻上线。
//
// UID 级：目标是"即将落地的角色已被别的在线会话占用"。唯一的调用点在 forward——
// 选角回包/换角回包携带的 uid 与会话当前 uid 不同时、在 uid 落地
//（CookiesUpdate→rebind）**之前**过这里。协商被拒时新端拿到 ErrReplaced，
// uid 不落表，老会话毫发无损。
//
// 三条入口在 UID 模型下天然收敛到这一个点：TCP/WSS/HTTP 的认证虽然建会话
//（players.Create），但会话无角色、不在会话表里（表键=uid），认证无从"占用"
// 任何东西；角色占用只可能在 uid 落地时发生。WSS token 重连走 Reconnect
// 还原原会话，是同一个客户端自己回来，不经协商。
//（账号级旧实现要在 TCP/WSS/HTTP 三个 login 里各自前置 negotiate，因为它
// 认证即把会话放进表、把同账号老连接顶掉；这里认证不触碰表，不需要。）
//
// 老连接的处置与策略无关（一律进入"只收不发"的存活期，由 players.Negotiate
// 完成），差别只有新端等不等它，所以策略判断收在这一个函数里，players 那层
// 只提供不带策略的原语。
func negotiate(uid, ip string, sock *cosnet.Socket) error {
	countdown, address := players.Negotiate(uid, ip, sock)
	if countdown <= 0 {
		return nil //没有活着的老连接，直接上线
	}
	if Setting.ForceReplace {
		return nil //强制顶号：新端立即接管，老连接把在途回包发完即可
	}
	//协商顶号：本次登录被拒，带上剩余秒数供新端重试、以及在线端 IP 供客户端提示
	return errors.ErrReplaced(countdown, address)
}
