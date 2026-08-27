package gateway

import (
	"github.com/hwcer/cosnet"
	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/players"
)

// negotiate 顶号处置：通知老连接，并按 Setting.ForceReplace 决定新端能不能立刻上线。
//
// 三条登录路径（TCP / WSS / HTTP）都必须在 players.Login **之前**过这里——
// Login 会 ss.Refresh() 强制旧 TOKEN 失效，协商模式下顶号被拒却把老玩家的 secret
// 作废了，他连断线重连都回不来。
//
// 老连接的处置与策略无关（一律进入"只收不发"的存活期），差别只有新端等不等它，
// 所以策略判断收在这一个函数里，players 那层只提供不带策略的原语。
func negotiate(guid, ip string, sock *cosnet.Socket) error {
	countdown := players.Negotiate(guid, ip, sock)
	if countdown <= 0 {
		return nil //没有活着的老连接，直接上线
	}
	if Setting.ForceReplace {
		return nil //强制顶号：新端立即接管，老连接把在途回包发完即可
	}
	return errors.ErrReplaced(countdown) //协商顶号：本次登录被拒，带上剩余秒数供新端重试
}
