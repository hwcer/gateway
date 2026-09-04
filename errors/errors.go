package errors

import (
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
)

var (
	ErrNotFount          = values.Errorf(404, "page not found")
	ErrNotSelectRole     = values.Errorf(405, "not select role")                  //请先选择角色
	ErrNeedGameDeveloper = values.Errorf(406, "developer permission is required") //需要GM权限
	ErrServerMaintenance = values.Errorf(407, "server maintenance in progress")   //维护模式，仅仅允许管理员登录
)

// ErrReplaced 顶号协商中：账号已在别处登录，本次登录被拒。
//
// 🔴 Args 顺序固定为 [剩余秒数, 在线端IP]：
//   - 剩余秒数——老连接收到通知后还能活这么久，之后被断开，新端**届时重新登录即可直接上线**。
//     客户端拿这个秒数决定什么时候重试。
//   - 在线端IP——占着账号的那条连接的地址（已去掉端口），供客户端提示"账号正在 xxx 在线"。
//
// 复用 session 的 209，客户端不必为顶号单独认一个码。
//
// ⚠️ 这里**不能**写成 values.Errorf(0, session.ErrorSessionReplaced).WithArgs(...)：
// 无需改写字段时 Errorf 原样返回传入的指针，而那是个包级共享哨兵，WithArgs 会把 Args
// 写进全局，之后所有拿到 ErrorSessionReplaced 的地方都带着上一次顶号的 IP。
func ErrReplaced(countdown int32, address string) *values.Message {
	return values.Errorf(session.ErrorSessionReplaced.Code, "session replaced").WithArgs(countdown, address)
}
