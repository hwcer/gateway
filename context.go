package gateway

import (
	"bytes"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/message"
)

// Proxy 内部转发时使用
type Proxy interface {
	Flag() message.Flag        //TCP 消息标记
	Header() map[string]string //HTTP 请求头,tcp,wss 只有 Accept, Content-Type
	Session() *session.Data    //登录信息，可能为空，未登录
	Metadata() values.Metadata // query,转换成 rpc Metadata
	RemoteAddr() string        //客户端地址

	Login(guid string, value values.Values) (string, error) //通过业务服激活登录信息
	Logout() error                                          //退出登录
	Verify() (*session.Data, error)                         //验证登录信息
	Buffer() (buf *bytes.Buffer, err error)                 //数据包

}

func NewContextWithSocket(path *string, flag *message.Flag, meta values.Metadata, socket *cosnet.Socket) *Context {
	r := Context{path: path, flag: flag, meta: meta}
	if socket != nil {
		r.data = socket.Data()
		r.address = socket.RemoteAddr().String()
	}
	return &r
}
func NewContextWithProxy(path *string, flag *message.Flag, meta values.Metadata, ctx Proxy) *Context {
	r := Context{path: path, flag: flag, meta: meta}
	r.data = ctx.Session()
	r.address = ctx.RemoteAddr()
	return &r
}

// Context 收到消息时使用的
type Context struct {
	path    *string
	flag    *message.Flag
	meta    values.Metadata
	data    *session.Data
	address string //客户端地址
}

// Path 使用指针，必要时可以修改
func (this *Context) Path() *string {
	return this.path
}

// Flag 使用指针，必要时可以修改
func (this *Context) Flag() *message.Flag {
	return this.flag
}

func (this *Context) Header() map[string]string {
	return nil
}
func (this *Context) Session() *session.Data {
	return this.data
	//if this.socket == nil {
	//	return nil
	//}
	//data := this.socket.Data()
	//if data == nil {
	//	return nil
	//}
	//return session.New(data)
}
func (this *Context) Metadata() values.Metadata {
	return this.meta
}

func (this *Context) RemoteAddr() string {
	return this.address
}
