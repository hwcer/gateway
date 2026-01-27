package gateway

import (
	"bytes"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
)

type Context interface {
	login(guid string, value values.Values) (string, error) //通过业务服激活登录信息
	verify() (*session.Data, error)                         //验证登录信息
	logout() error                                          //退出登录

	Header() values.Metadata                //HTTP 请求头,tcp,wss 只有 Accept, Content-Type
	Buffer() (buf *bytes.Buffer, err error) //数据包
	Session() *session.Session              //登录信息，可能为空，未登录
	Metadata() values.Metadata              // query,转换成 rpc Metadata
	RemoteAddr() string                     //客户端地址
}
