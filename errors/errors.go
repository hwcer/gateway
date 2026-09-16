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

// ErrReplaced UID 级顶号协商中：目标角色已在别处登录，本次登录被拒。
//
// 🔴 Args 顺序固定为 [剩余秒数, 在线端IP]：
//   - 剩余秒数——老连接收到通知后还能活这么久，之后被断开，新端**届时重新登录即可直接上线**。
//     客户端拿这个秒数决定什么时候重试。
//   - 在线端IP——占着角色的那条连接的地址（已去掉端口），供客户端提示"角色正在 xxx 在线"。
//
// 复用 session 的 209，客户端不必为顶号单独认一个码。
//
// ⚠️ 这里**不能**绕过 Clone 原地改 Args（比如先 Errorf 再直接写返回值的 Args 字段）：
// 无需改写字段时 Errorf 原样返回传入的指针，而那是个包级共享哨兵，原地写 Args 会污染全局，
// 之后所有拿到 ErrorSessionReplaced 的地方都带着上一次顶号的 IP。
// Clone 先拷贝再换 Args，不碰哨兵本体——换 Args 的唯一合法姿势。
func ErrReplaced(countdown int32, address string) *values.Message {
	return values.Errorf(session.ErrorSessionReplaced.Code, "session replaced").Clone(countdown, address)
}
