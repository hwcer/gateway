package gateway

import (
	"github.com/hwcer/cosgo/binder"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/message"
	"github.com/hwcer/gateway/context"
)

// socketContext 是 context.Context 的"脱离入站请求"实现：
// 用于服务器推送(send/broadcast)、顶号/秘钥下发(S2CSecret/S2CReplaced)以及 Forward 的钩子阶段，
// 携带该阶段正确的 path/flag/metadata/session/socket，与具体入站请求(HttpRequest/SocketRequest)解耦。
type socketContext struct {
	sock   *cosnet.Socket
	data   *session.Data
	path   string
	body   []byte
	flag   message.Flag
	meta   values.Metadata
	index  int32
	header map[string]string
}

// newSocketContext 从**入站请求**派生响应上下文：sock / session / index / path 都在 c 上，
// 不必逐个传；只有响应侧才有的东西（body / flag / meta）单独给。
//
// path 取的是 c.Path()——proxyRequest 转发前已经把**转发目标**写进去了（c.Path(path)），
// 业务钩子按目标服分流靠的就是它。别改成拿 servicePath/serviceMethod 拼：业务层的 Router
// 不保证可逆，项目里带分区号的路径解析后 servicePath 会被替换成服务名，拼不回原样。
func newSocketContext(c context.Context, body []byte, flag message.Flag, meta values.Metadata) *socketContext {
	r := newSenderContext(c.Socket(), c.Path(), body, flag, meta)
	r.data = c.Session()
	r.index = c.Index()
	return r
}

// newSenderContext 从**一条连接**构造推送上下文：推送与广播手里根本没有入站请求，
// 也就没有 session 与包序号可言（Session() 会自动回落到 sock.Data()）。
// sock 为 nil 即广播——不针对任何一条具体连接。
func newSenderContext(sock *cosnet.Socket, path string, body []byte, flag message.Flag, meta values.Metadata) *socketContext {
	if meta == nil {
		meta = values.Metadata{}
	}
	return &socketContext{sock: sock, path: path, body: body, flag: flag, meta: meta}
}

func (this *socketContext) Path(set ...string) string {
	if len(set) > 0 {
		this.path = set[0]
	}
	return this.path
}
func (this *socketContext) Flag(set ...message.Flag) message.Flag {
	if len(set) > 0 {
		this.flag = set[0]
	}
	return this.flag
}
func (this *socketContext) Index() int32 {
	return this.index
}
func (this *socketContext) Socket() *cosnet.Socket {
	return this.sock
}
func (this *socketContext) Buffer(b ...[]byte) ([]byte, error) {
	if len(b) > 0 {
		this.body = b[0]
	}
	return this.body, nil
}

func (this *socketContext) Accept() binder.Binder {
	if this.sock == nil {
		return nil
	}
	if m := message.Magics.Get(this.sock.Magic()); m != nil {
		return m.Binder
	}
	return nil
}

func (this *socketContext) Header() map[string]string {
	return this.header
}
func (this *socketContext) Session() *session.Data {
	if this.data != nil {
		return this.data
	}
	if this.sock != nil {
		return this.sock.Data()
	}
	return nil
}
func (this *socketContext) Metadata() values.Metadata {
	return this.meta
}
func (this *socketContext) RemoteAddr() string {
	if this.sock == nil {
		return ""
	}
	return stripPort(this.sock.RemoteAddr().String())
}

func (this *socketContext) GetHeader(key string) string {
	return this.header[key]
}
func (this *socketContext) SetHeader(key, value string) {
	if this.header == nil {
		this.header = make(map[string]string)
	}
	this.header[key] = value
}

func (this *socketContext) SetMetadata(name, value string) {
	this.meta[name] = value
}
func (this *socketContext) GetMetadata(key string) string {
	return this.meta.GetString(key)
}
