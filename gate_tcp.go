package gateway

import (
	"fmt"
	"maps"
	"net"
	"net/url"

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

// TcpServer 长连接网关。TCP 与 WSS 共用它（见 Module.Init），所以 WSS 不必单独实现一套。
type TcpServer struct {
	*cosnet.Sockets
}

// init 注册事件回调与内置路由（认证/心跳/重连），并把各业务服的路由前缀挂上代理
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

// serialize cosnet 的回包序列化出口
func (this *TcpServer) serialize(c *cosnet.Context, reply any) ([]byte, error) {
	return Setting.Handler.Serialize(c, reply)
}

// Listen 监听TCP端口
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
func (this *TcpServer) Accept(ln net.Listener) error {
	this.Sockets.Accept(&tcp.Listener{Listener: ln})
	logger.Trace("网关长连接启动：%v", gwcfg.Options.Gate.Address)
	return nil
}

// C2SHeartbeat 处理长连接心跳请求，逻辑与短连接共用，见 heartbeat。
func (this *TcpServer) C2SHeartbeat(c *cosnet.Context) any {
	return heartbeat(&SocketRequest{Context: c})
}

// C2SOAuth 处理认证请求
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
	developer := 0
	if data.Developer {
		developer = 1
	}
	ctx := SocketRequest{Context: c}
	secret, err := ctx.login(data.Openid, values.Values{gwcfg.ServiceMetadataDeveloper: developer})
	if err != nil {
		return err
	}
	if Setting.G2SOAuth == "" {
		return nil //不需要业务服确认,认证到此为止
	}
	//秘钥交给业务服,由它决定要不要在确认包里回给客户端。
	//两段式登录下认证阶段恒为空——重连秘钥由 S2CSecret 在选角落地(LOGIN)时下发
	if secret != "" {
		ctx.SetMetadata(gwcfg.ServicePlayerCookie, secret)
	}
	ctx.body = []byte{} //oauth 路径显式置空，业务层未设置时也不回退到客户端凭据报文
	if err = args.GetValues(data, &ctx); err != nil {
		return err
	}
	var reply []byte
	if reply, err = forward(&ctx, Setting.G2SOAuth); err != nil {
		return err
	}
	return reply
}

// S2CSecret 登录成功后下发秘钥（转交 Setting.Handler，默认 Default 以 JSON 下发）
func (this *TcpServer) S2CSecret(sock *cosnet.Socket, _ any) {
	data := sock.Data()
	if data == nil {
		return
	}
	//认证态会话(未落库)没有秘钥:选角落地(LOGIN)时 Replace 会再次触发本事件,
	//这里跳过——否则 Token() 会在伪会话上现生成一个不落存储的废秘钥发给客户端
	if !players.Persistent(data) {
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

// S2CReplaced 连接被接管（UID 级顶号）时通知老端（转交 Setting.Handler，默认 Default 以 JSON 下发）
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

// C2SReconnect 处理长连接重连请求，逻辑与短连接共用，见 reconnect。
func (this *TcpServer) C2SReconnect(c *cosnet.Context) any {
	return reconnect(&SocketRequest{Context: c})
}

// Disconnect 处理断开连接事件
func (this *TcpServer) Disconnect(sock *cosnet.Socket, _ any) {
	if err := players.Disconnect(sock); err != nil {
		logger.Alert("Disconnect error:%v", err)
	}
}

// proxy 处理TCP请求代理
func (this *TcpServer) proxy(c *cosnet.Context) any {
	path, _, err := c.Path()
	if err != nil {
		return err
	}
	ctx := SocketRequest{Context: c}
	reply, err := forward(&ctx, path)
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
func (this *SocketRequest) verify() (*session.Data, error) {
	data := this.Context.Socket.Data()
	if data == nil {
		return nil, session.ErrorSessionNotExist
	}
	return data, nil
}

// login 认证（gateway 内部）:只把 GUID 绑到 socket.Data,**不执行 LOGIN**——
// 正式会话(落存储/不透明id/重连秘钥)在选角回包落地时建立(players.Login)。
// 返回的 token 恒为空:秘钥改由 S2CSecret 事件在 LOGIN 时下发。
//
// 顶号是 UID 级的,发生在选角回包落地时(见 players.rebind)——认证阶段不做任何
// 占用判断,同账号多角色并行在线是合法状态,也就不存在"必须先协商"的时序约束。
func (this *SocketRequest) login(guid string, value values.Values) (token string, err error) {
	_, err = players.Auth(this.Context.Socket, guid, value)
	return "", err
}

// retoken 长连接不需要换发:LOGIN 时 Replace 触发的 S2CSecret 已把新秘钥推给客户端
func (this *SocketRequest) retoken(token string) {
}

// logout 登出（gateway 内部）
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
	maps.Copy(r, this.header)
	return r
}

// Metadata 获取请求元数据
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
