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
// Data 是协商剩余秒数——老连接收到通知后还能活这么久，之后被断开，
// 新端**届时重新登录即可直接上线**。客户端拿这个秒数决定什么时候重试。
// 复用 session 的 209，客户端不必为顶号单独认一个码。
func ErrReplaced(countdown int32) *values.Message {
	return &values.Message{Code: session.ErrorSessionReplaced.Code, Data: countdown}
}
