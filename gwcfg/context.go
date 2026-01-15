package gwcfg

import (
	"bytes"

	"github.com/hwcer/cosgo/binder"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
)

type Context interface {
	Path() (string, error)
	Login(guid string, value values.Values) (string, error) //通过业务服激活登录信息
	Logout() error                                          //退出登录
	Accept() binder.Binder                                  //客户端接受的编码方式
	Buffer() (buf *bytes.Buffer, err error)                 //数据包
	Verify() (*session.Data, error)                         //验证登录信息
	//Session() *session.Data                                 //获取 session 信息
	Metadata() values.Metadata // query,转换成 rpc Metadata
	RemoteAddr() string
}
