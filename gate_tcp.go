package gateway

import (
	"fmt"
	"net"
	"net/url"
	"time"

	"github.com/hwcer/cosgo/binder"
	"github.com/hwcer/cosrpc"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
	"github.com/hwcer/gateway/token"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/tcp"
	"github.com/hwcer/logger"
)

// NewTCPServer 创建TCP服务器实例
// 返回值:
//   - *TcpServer: TCP服务器实例
func NewTCPServer() *TcpServer {
	s := &TcpServer{}
	// 独占实例，不用 cosnet.Default：网关会对所持实例做全局性动作（关心跳计时器、
	// session 心跳遍历整池、注册 Replaced/Disconnect/Authentication 回调、Start/Close），
	// 共用全局池会把同进程里别人用 cosnet.Connect 建的连接一起卷进来。
	// ⚠️ 由此带来的约束：**任何按 id 找 socket、或注册 socket 事件的代码都必须走这个实例**
	// （TCP.Get / TCP.On），包级 cosnet.Get / cosnet.On 查的是 Default，在这里恒不生效。
	s.Sockets = cosnet.New()
	return s
}

// TcpServer TCP服务器结构体
// 用于处理TCP长连接请求
type TcpServer struct {
	*cosnet.Sockets
	//Errorf func(*cosnet.Context, error) any
}

// init 初始化TCP服务器
// 设置心跳管理、事件回调和服务注册
// 返回值:
//   - error: 初始化过程中的错误
func (this *TcpServer) init() error {
	session.On(session.EventHeartbeat, this.heartbeat)

	// 注册事件回调
	this.Sockets.On(cosnet.EventTypeReplaced, this.S2CReplaced)
	this.Sockets.On(cosnet.EventTypeDisconnect, this.Disconnect)
	this.Sockets.On(cosnet.EventTypeAuthentication, this.S2CSecret)
	this.Sockets.Options.Heartbeat = 0 //关闭计时器,由session接管
	// 注册服务
	service := this.Sockets.Service()
	for k := range cosrpc.Service {
		_ = service.Register(this.proxy, fmt.Sprintf("/%s/*", k))
	}

	if Setting.C2SOAuth != "" {
		_ = service.Register(this.C2SOAuth, Setting.C2SOAuth) // 注册认证服务
	}
	if Setting.C2SHeartbeat != "" {
		_ = service.Register(this.C2SHeartbeat, Setting.C2SHeartbeat)
	}
	if Setting.C2SReconnect != "" {
		_ = service.Register(this.C2SReconnect, Setting.C2SReconnect)
	}

	// 设置序列化器
	h := this.Sockets.Handler()
	h.SetSerialize(this.serialize)

	return this.Sockets.Start()
}

// serialize 序列化函数
// 用于序列化响应数据
// 参数:
//   - c: cosnet上下文
//   - reply: 要序列化的数据
//
// 返回值:
//   - []byte: 序列化后的数据
//   - error: 序列化过程中的错误
func (this *TcpServer) serialize(c *cosnet.Context, reply any) ([]byte, error) {
	return Setting.Handler.Serialize(c, reply)
}

// Listen 监听TCP端口
// 参数:
//   - address: 监听地址
//
// 返回值:
//   - error: 监听过程中的错误
func (this *TcpServer) Listen(address string) error {
	_, err := this.Sockets.Listen(address)
	if err == nil {
		logger.Trace("网关长连接启动：%v", gwcfg.Options.Gate.Address)
	}
	return err
}

func (this *TcpServer) heartbeat(i any) {
	s, _ := i.(int32)
	if s <= 0 {
		return
	}
	this.Sockets.Heartbeat(s)
}

// Accept 接受TCP连接
// 参数:
//   - ln: 监听器
//
// 返回值:
//   - error: 接受连接过程中的错误
func (this *TcpServer) Accept(ln net.Listener) error {
	this.Sockets.Accept(&tcp.Listener{Listener: ln})
	logger.Trace("网关长连接启动：%v", gwcfg.Options.Gate.Address)
	return nil
}

// C2SHeartbeat 处理心跳请求
// 参数:
//   - c: cosnet上下文
//
// 返回值:
//   - any: 当前时间戳（毫秒）
func (this *TcpServer) C2SHeartbeat(c *cosnet.Context) any {
	if h, ok := Setting.Handler.(C2SHeartbeat); ok {
		return h.C2SHeartbeat(c)
	}
	ms := time.Now().UnixMilli()
	return ms
}

// C2SOAuth 处理认证请求
// 参数:
//   - c: cosnet上下文
//
// 返回值:
//   - any: 认证结果
func (this *TcpServer) C2SOAuth(c *cosnet.Context) any {
	args := Setting.Handler.Token()
	if err := c.Bind(args); err != nil {
		return err
	}
	// 验证 token
	data, err := token.Verify(args)
	if err != nil {
		return err
	}
	// 创建 socket 代理并登录
	ctx := SocketRequest{Context: c}
	vs := values.Values{}
	if data.Developer {
		vs.Set(gwcfg.ServiceMetadataDeveloper, 1)
	} else {
		vs.Set(gwcfg.ServiceMetadataDeveloper, 0)
	}
	var s string
	if s, err = ctx.login(data.Openid, vs); err != nil {
		return err
	}

	if Setting.G2SOAuth == "" {
		return nil
	}
	//将秘钥 传给 业务服务器由业务层决定要不要在确认包中返回给客户端
	ctx.SetMetadata(gwcfg.ServicePlayerCookie, s)
	ctx.body = []byte{} //oauth 路径显式置空，业务层未设置时也不回退到客户端凭据报文
	if err = args.GetValues(data, &ctx); err != nil {
		return err
	}
	var reply []byte
	if reply, err = proxyRequest(&ctx, Setting.G2SOAuth); err != nil {
		return err
	}
	return reply
}

// S2CSecret 登录成功后下发秘钥（转交 Setting.Handler，默认 Default 以 JSON 下发）
// 参数:
//   - sock: cosnet socket
//   - _: 事件数据（未使用）
func (this *TcpServer) S2CSecret(sock *cosnet.Socket, _ any) {
	data := sock.Data()
	if data == nil {
		return
	}
	ss := session.New(data)
	ts, err := ss.Token()
	if err != nil {
		sock.Errorf(err)
		return
	}
	Setting.Handler.S2CSecret(sock, ts)
}

// S2CReplaced 有人请求顶号时下发协商提示（转交 Setting.Handler，默认 Default 以 JSON 下发）
// 参数:
//   - sock: cosnet socket（进入协商期的老连接）
//   - i: 事件数据 *cosnet.Replaced，含顶号方 IP 与协商剩余秒数
func (this *TcpServer) S2CReplaced(sock *cosnet.Socket, i any) {
	if sock == nil {
		return
	}
	r, ok := i.(*cosnet.Replaced)
	if !ok {
		return
	}
	Setting.Handler.S2CReplaced(sock, r)
}

// C2SReconnect 处理重连请求
// 参数:
//   - c: cosnet上下文
//
// 返回值:
//   - any: 重连结果
func (this *TcpServer) C2SReconnect(c *cosnet.Context) any {
	if h, ok := Setting.Handler.(C2SReconnect); ok {
		return h.C2SReconnect(c) //业务 Handler 实现则优先
	}
	secret := string(c.Message.Body())
	if secret == "" {
		return values.Error("secret empty")
	}
	p, err := players.Reconnect(c.Socket, secret)
	if err != nil {
		return err
	}
	return p.GetInt32(gwcfg.ServiceMetadataRequestId)
}

// Disconnect 处理断开连接事件
// 参数:
//   - sock: cosnet socket
//   - _: 事件数据（未使用）
func (this *TcpServer) Disconnect(sock *cosnet.Socket, _ any) {
	if err := players.Disconnect(sock); err != nil {
		logger.Alert("Disconnect error:%v", err)
	}
}

// proxy 处理TCP请求代理
// 参数:
//   - c: cosnet上下文
//
// 返回值:
//   - any: 代理结果
func (this *TcpServer) proxy(c *cosnet.Context) any {
	path, _, err := c.Path()
	if err != nil {
		return err
	}
	ctx := SocketRequest{Context: c}
	reply, err := proxyRequest(&ctx, path)
	if err != nil {
		return err
	}
	return reply
}

// SocketRequest socket代理结构体
// 实现 gwcfg.Context 接口，用于TCP请求的代理
type SocketRequest struct {
	*cosnet.Context
	body     []byte
	header   map[string]string
	metadata values.Metadata
	path     string
}

// verify 验证会话（gateway 内部）
// 返回值:
//   - *session.Data: 会话数据
//   - error: 验证过程中的错误
func (this *SocketRequest) verify() (*session.Data, error) {
	data := this.Context.Socket.Data()
	if data == nil {
		return nil, session.ErrorSessionNotExist
	}
	return data, nil
}

// login 登录（gateway 内部）
// 参数:
//   - guid: 用户GUID
//   - value: 登录值
//
// 返回值:
//   - token: 登录令牌
//   - error: 登录过程中的错误
func (this *SocketRequest) login(guid string, value values.Values) (token string, err error) {
	data := this.Context.Socket.Data()
	if data != nil {
		if data.UUID() != guid {
			return "", fmt.Errorf("please do not login again")
		}
	} else if data, err = players.Connect(this.Context.Socket, guid, value); err != nil {
		return
	}
	ss := session.New(data)
	return ss.Token()
}

// logout 登出（gateway 内部）
// 返回值:
//   - error: 登出过程中的错误
func (this *SocketRequest) logout() error {
	this.Context.Socket.Close()
	return nil
}

// Flag 直接由内嵌的 cosnet.Context 提供：出站(确认包)flag，与入站 Message.Flag() 独立

func (this *SocketRequest) Index() int32 {
	return this.Context.Message.Index()
}

func (this *SocketRequest) Session() *session.Data {
	return this.Context.Socket.Data()
}

// Socket 获取socket
// 返回值:
//   - *cosnet.Socket: cosnet socket
func (this *SocketRequest) Socket() *cosnet.Socket {
	return this.Context.Socket
}

// Buffer 无参取值(默认读消息体)；传参设置透传请求体
func (this *SocketRequest) Buffer(set ...[]byte) ([]byte, error) {
	if len(set) > 0 {
		this.body = set[0]
	}
	if this.body != nil {
		return this.body, nil
	}
	return this.Context.Message.Body(), nil
}

// SetHeader 业务层设置透传请求头，如 Content-Type
func (this *SocketRequest) SetHeader(key, value string) {
	if this.header == nil {
		this.header = make(map[string]string)
	}
	this.header[key] = value
}

// GetHeader 读取请求头（不构造完整 map）
func (this *SocketRequest) GetHeader(key string) string {
	if this.header != nil {
		if v, ok := this.header[key]; ok {
			return v
		}
	}
	if key == binder.HeaderAccept || key == binder.HeaderContentType {
		return this.Message.Magic().Binder.Name()
	}
	return ""
}
func (this *SocketRequest) Header() map[string]string {
	// 设置 Content-Type
	r := make(map[string]string)
	magic := this.Message.Magic()
	b := magic.Binder.Name()
	r[binder.HeaderAccept] = b
	r[binder.HeaderContentType] = b
	if this.header == nil {
		return r
	}
	for k, v := range this.header {
		r[k] = v
	}
	return r
}

// Metadata 获取请求元数据
// 返回值:
//   - values.Metadata: 请求元数据
func (this *SocketRequest) Metadata() values.Metadata {
	if this.metadata == nil {
		// 静态字段缓存一次：查询参数 + RequestId
		this.metadata = values.Metadata{}
		if _, q, _ := this.Context.Path(); q != "" {
			query, _ := url.ParseQuery(q)
			for k := range query {
				this.metadata[k] = query.Get(k)
			}
		}
		this.metadata[gwcfg.ServiceMetadataRequestId] = fmt.Sprintf("%d", this.Context.Message.Index())
	}
	// 仅 header 派生字段每次刷新，反映业务 SetHeader 的改动
	ct := this.GetHeader(binder.HeaderContentType)
	this.metadata[binder.HeaderContentType] = ct
	if accept := this.GetHeader(binder.HeaderAccept); accept != "" && accept != ct {
		this.metadata[binder.HeaderAccept] = accept
	} else {
		delete(this.metadata, binder.HeaderAccept)
	}
	return this.metadata
}

// RemoteAddr 获取远程地址
// 返回值:
//   - string: 远程地址
func (this *SocketRequest) RemoteAddr() string {
	return stripPort(this.Context.RemoteAddr().String())
}

// Path 无参取值(默认消息路径)；传参设置路由 path（遮蔽内嵌 cosnet.Context 的三值 Path）
func (this *SocketRequest) Path(set ...string) string {
	if len(set) > 0 {
		this.path = set[0]
	}
	if this.path == "" {
		this.path, _, _ = this.Context.Path()
	}
	return this.path
}

// SetMetadata 设置转发元数据
func (this *SocketRequest) SetMetadata(name, value string) {
	this.Metadata()[name] = value
}

// GetMetadata 读取转发元数据
func (this *SocketRequest) GetMetadata(key string) string {
	return this.Metadata().GetString(key)
}
