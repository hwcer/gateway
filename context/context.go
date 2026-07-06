package context

import (
	"github.com/hwcer/cosgo/binder"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/message"
)

// Context 网关统一请求上下文：入站请求(HttpRequest/SocketRequest)与服务器推送(socketContext)共用。
// 负责处理网关请求信息、透传设置、响应元数据等。
type Context interface {
	Path(set ...string) string             //无参取值；传参设置(路由 path)
	Flag(set ...message.Flag) message.Flag //无参取值；传参设置
	Buffer(set ...[]byte) ([]byte, error)  //数据包
	Header() map[string]string             //HTTP 请求头,tcp/wss 只有 Accept,Content-Type
	Accept() binder.Binder                 //响应序列化方式
	Socket() *cosnet.Socket                //长连接 socket，HTTP 返回 nil
	Session() *session.Data                //登录信息，可能为空
	Metadata() values.Metadata             //query,转换成 rpc Metadata
	RemoteAddr() string                    //客户端地址
	GetHeader(key string) string           //获取头信息
	SetHeader(key, value string)           //设置透传请求头，如 Content-Type
	SetMetadata(name, value string)        //设置转发元数据
	GetMetadata(key string) string         //读取转发元数据
}
