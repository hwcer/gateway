package context

import (
	"github.com/hwcer/cosgo/binder"
)

//负责处理网关响应,元数据等

type Context interface {
	Accept() binder.Binder
	SetMetadata(name, value string)
	GetMetadata(key string) (val string)
}
