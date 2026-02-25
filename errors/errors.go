package errors

import "github.com/hwcer/cosgo/values"

var (
	ErrNotFount          = values.Errorf(404, "page not found")
	ErrNotSelectRole     = values.Errorf(405, "not select role")                  //请先选择角色
	ErrNeedGameDeveloper = values.Errorf(406, "developer permission is required") //需要GM权限
	ErrServerMaintenance = values.Errorf(407, "server maintenance in progress")   //维护模式
)
