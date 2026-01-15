package errors

import "github.com/hwcer/cosgo/values"

var (
	ErrLogin             = values.Errorf(501, "not login")                        //请重新登录
	ErrNotSelectRole     = values.Errorf(502, "not select role")                  //请先选择角色
	ErrNeedGameDeveloper = values.Errorf(503, "developer permission is required") //需要GM权限
	ErrServerMaintenance = values.Errorf(505, "server maintenance in progress")
)
