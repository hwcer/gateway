package gateway

import (
	"bytes"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
)

type Context interface {
	Header() map[string]string //HTTP 请求头,tcp,wss 只有 Accept, Content-Type
	Session() *session.Session //登录信息，可能为空，未登录
	Metadata() values.Metadata // query,转换成 rpc Metadata
	RemoteAddr() string        //客户端地址
}

// Proxy 内部转发时使用
type Proxy interface {
	Context

	Login(guid string, value values.Values) (string, error) //通过业务服激活登录信息
	Logout() error                                          //退出登录
	Verify() (*session.Data, error)                         //验证登录信息
	Buffer() (buf *bytes.Buffer, err error)                 //数据包

}

// Received 收到消息时使用的 Context
type Received struct {
	socket *cosnet.Socket
}

func (this *Received) Header() map[string]string {
	return nil
}
func (this *Received) Session() *session.Session {
	if this.socket == nil {
		return nil
	}
	data := this.socket.Data()
	if data == nil {
		return nil
	}
	return session.New(data)
}
func (this *Received) Metadata() values.Metadata {
	return nil
}
func (this *Received) RemoteAddr() string {
	if this.socket == nil {
		return ""
	}
	return this.socket.RemoteAddr().String()
}
