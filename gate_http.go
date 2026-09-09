package gateway

import (
	"fmt"
	"maps"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/hwcer/cosgo"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/cosnet/message"
	"github.com/hwcer/cosrpc"
	"github.com/hwcer/coswss"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
	"github.com/hwcer/gateway/token"

	"github.com/hwcer/cosgo/binder"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosweb"
	"github.com/hwcer/cosweb/middleware"
	"github.com/hwcer/logger"
)

// Method 支持的HTTP请求方法
var Method = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}

// Headers 支持的HTTP请求头
var Headers = []string{
	session.Options.Name,
	"Accept", "Content-Type", "Content-Length", "Accept-Encoding", "Authorization",
	"X-CSRF-Token", "X-Requested-With", "X-Unity-Version", "x-Forwarded-Key", "x-Forwarded-Val",
}

// NewHttpServer 创建HTTP服务器实例
func NewHttpServer() *HttpServer {
	s := &HttpServer{}
	return s
}

// HttpServer 短连接网关（HTTP）
type HttpServer struct {
	*cosweb.Server
	static *cosweb.Static
}

// init 初始化HTTP服务器
// 设置跨域、注册服务和序列化器
func (this *HttpServer) init() (err error) {
	this.Server = cosweb.New()
	// 跨域设置
	allow := middleware.NewAccessControlAllow()
	allow.Origin("*")
	allow.Methods(Method...)
	allow.Headers(Headers...)
	this.Server.Use(allow.Middleware)

	for k := range cosrpc.Service {
		this.Server.Register(fmt.Sprintf("/%s/*", k), this.proxy, Method...)
	}

	// 设置序列化器
	h := this.Server.Handler()
	h.SetSerialize(this.serialize)
	// 注册服务
	if Setting.C2SOAuth != "" {
		this.Server.Register(Setting.C2SOAuth, this.oauth, Method...) // 注册认证服务
	}
	if Setting.C2SHeartbeat != "" {
		this.Server.Register(Setting.C2SHeartbeat, this.C2SHeartbeat, Method...) // 注册心跳服务
	}

	// 静态文件服务
	if gwcfg.Options.Gate.Static != nil && gwcfg.Options.Gate.Static.Root != "" {
		this.static = this.Server.Static(gwcfg.Options.Gate.Static.Route, gwcfg.Options.Gate.Static.Root, http.MethodGet)
		if cosgo.Debug() {
			this.static.Nocache(true)
		}
		if gwcfg.Options.Gate.Static.Index != "" {
			this.static.Index(gwcfg.Options.Gate.Static.Index)
		}
	}
	return nil
}
func (this *HttpServer) WebSocket(c *cosweb.Context, next cosweb.Next) error {
	if !coswss.IsWebSocket(c.Request) {
		return next()
	}
	coswss.Handler(TCP.Sockets, c.Response, c.Request)
	return nil
}
func (this *HttpServer) wss() error {
	handler := this.Server.Handler(gwcfg.Options.Gate.Websocket)
	handler.Use(this.WebSocket)
	return nil
}

// serialize cosweb 的回包序列化出口
func (this *HttpServer) serialize(c *cosweb.Context, reply any) ([]byte, error) {
	return Setting.Handler.Serialize(c, reply)
}

// Listen 监听HTTP端口
func (this *HttpServer) Listen(address string) (err error) {
	if gwcfg.Options.Gate.KeyFile != "" && gwcfg.Options.Gate.CertFile != "" {
		err = this.Server.TLS(address, gwcfg.Options.Gate.CertFile, gwcfg.Options.Gate.KeyFile)
	} else {
		err = this.Server.Listen(address)
	}
	if err == nil {
		logger.Trace("网关短连接启动：%v", gwcfg.Options.Gate.Address)
	}
	return
}

// Accept 接受HTTP连接
func (this *HttpServer) Accept(ln net.Listener) (err error) {
	if gwcfg.Options.Gate.KeyFile != "" && gwcfg.Options.Gate.CertFile != "" {
		err = this.Server.TLS(ln, gwcfg.Options.Gate.CertFile, gwcfg.Options.Gate.KeyFile)
	} else {
		err = this.Server.Accept(ln)
	}
	if err == nil {
		logger.Trace("网关短连接启动：%v", gwcfg.Options.Gate.Address)
	}
	return
}

// oauth 处理认证请求
func (this *HttpServer) oauth(c *cosweb.Context) any {
	args := Setting.Handler.Token()
	if err := c.Bind(args); err != nil {
		return err
	}
	// 验证 token
	data, err := token.Verify(args)
	if err != nil {
		return err
	}
	// 创建 http 代理并登录
	ctx := HttpRequest{Context: c}
	vs := values.Values{}
	if data.Developer {
		vs.Set(gwcfg.ServiceMetadataDeveloper, 1)
	} else {
		vs.Set(gwcfg.ServiceMetadataDeveloper, 0)
	}

	// 构建响应
	cookie := map[string]string{}
	cookie["key"] = session.Options.Name
	if cookie["val"], err = ctx.login(data.Openid, vs); err != nil {
		return err
	}
	if Setting.G2SOAuth == "" {
		return cookie
	}
	//将秘钥 传给 业务服务器由业务层决定要不要在确认包中返回给客户端
	ctx.SetMetadata(gwcfg.ServicePlayerCookie, cookie["val"])
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

// C2SHeartbeat 处理短连接心跳请求，逻辑与长连接共用，见 heartbeat。
//
// ⚠️ 原来这里是硬编码的时间戳：既不过业务 Handler 的钩子、也不转发到游戏服，
// 短连接客户端的角色照样会在游戏服那边被判掉线。
func (this *HttpServer) C2SHeartbeat(c *cosweb.Context) any {
	return heartbeat(&HttpRequest{Context: c})
}

// proxy 处理HTTP请求代理
func (this *HttpServer) proxy(c *cosweb.Context) (r any) {
	// 创建 http 代理并处理请求
	ctx := HttpRequest{Context: c}
	reply, err := forward(&ctx, c.Request.URL.Path)
	if err != nil {
		return err
	}
	return reply
}

// HttpRequest 短连接的入站请求，实现 context.Context
type HttpRequest struct {
	*cosweb.Context
	body     []byte
	header   map[string]string
	metadata values.Metadata
	flag     message.Flag
	path     string
}

// login 登录（gateway 内部）
//
// 顶号是 UID 级的（见 players.rebind），登录阶段不做占用判断。
// ⚠️ 也不再收掉会话上的老长连接——多角色并行下那可能是**同一账号另一个角色**
// 正在游戏的活连接，旧账号级顶号语义里"短连接登录踢掉长连接"的行为一并作废。
func (this *HttpRequest) login(guid string, value values.Values) (token string, err error) {
	var data *session.Data
	token, data, err = players.Create(guid, value)
	if err != nil {
		return
	}

	// 设置cookie
	cookie := &http.Cookie{Name: session.Options.Name, Path: "/", Value: token}
	http.SetCookie(this.Context.Response, cookie)
	// 设置响应头
	header := this.Context.Header()
	header.Set("X-Forwarded-Key", session.Options.Name)
	header.Set("X-Forwarded-Val", cookie.Value)
	//this.cookie = cookie
	this.Context.Session = session.New(data)
	return
}

// logout 登出（gateway 内部）
func (this *HttpRequest) logout() error {
	return this.Context.Session.Delete()
}

// verify 验证会话（gateway 内部）
func (this *HttpRequest) verify() (*session.Data, error) {
	// 如果会话已存在且有效，直接返回
	if this.Context.Session != nil && this.Context.Session.Data != nil {
		return this.Context.Session.Data, nil
	}
	s := this.Context.GetString(session.Options.Name, cosweb.RequestDataTypeCookie, cosweb.RequestDataTypeQuery, cosweb.RequestDataTypeHeader)
	// 验证 token
	if s == "" {
		return nil, values.Error("token empty")
	}
	if err := this.Context.Session.Verify(s); err != nil {
		return nil, err
	}
	return this.Context.Session.Data, nil
}

func (this *HttpRequest) Index() int32 {
	rid := this.GetMetadata(gwcfg.ServiceMetadataRequestId)
	if rid == "" {
		return 0
	}
	index, _ := strconv.ParseInt(rid, 10, 32)
	return int32(index)
}

// Buffer 无参取值(默认读请求体)；传参设置透传请求体（遮蔽内嵌 cosweb.Context.Buffer）
func (this *HttpRequest) Buffer(set ...[]byte) ([]byte, error) {
	if len(set) > 0 {
		this.body = set[0]
	}
	if this.body != nil {
		return this.body, nil
	}
	buf, err := this.Context.Buffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SetHeader 业务层设置透传请求头，如 Content-Type
func (this *HttpRequest) SetHeader(key, value string) {
	if this.header == nil {
		this.header = make(map[string]string)
	}
	this.header[key] = value
}

// GetHeader 读取请求头（不构造完整 map）
func (this *HttpRequest) GetHeader(key string) string {
	if this.header != nil {
		if v, ok := this.header[key]; ok {
			return v
		}
	}
	switch key {
	case binder.HeaderContentType:
		if t := this.getContentType(binder.HeaderContentType, ";"); t != "" {
			return t
		}
		return gwcfg.Options.Binder
	case binder.HeaderAccept:
		return this.getContentType(binder.HeaderAccept, ";")
	default:
		return this.Context.Header().Get(key)
	}
}

func (this *HttpRequest) Header() map[string]string {
	// 设置 Content-Type
	r := make(map[string]string)
	header := this.Context.Header()
	for k := range header {
		switch k {
		case binder.HeaderContentType:
			if t := this.getContentType(binder.HeaderContentType, ";"); t != "" {
				r[k] = t
			} else {
				r[k] = gwcfg.Options.Binder
			}
		case binder.HeaderAccept:
			if t := this.getContentType(binder.HeaderAccept, ";"); t != "" {
				r[k] = t
			}
		default:
			r[k] = header.Get(k)
		}
	}
	if this.header == nil {
		return r
	}
	maps.Copy(r, this.header)
	return r
}
func (this *HttpRequest) Session() *session.Data {
	if ss := this.Context.Session; ss != nil {
		return ss.Data
	}
	return nil
}

// Metadata 获取请求元数据
func (this *HttpRequest) Metadata() values.Metadata {
	if this.metadata == nil {
		// 仅缓存 URL 查询参数解析（静态）
		this.metadata = make(values.Metadata)
		q := this.Context.Request.URL.Query()
		for k := range q {
			this.metadata[k] = q.Get(k)
		}
	}
	// header 派生字段每次刷新，反映业务 SetHeader 的改动
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
func (this *HttpRequest) RemoteAddr() string {
	return stripPort(this.Context.RemoteAddr())
}

// stripPort 去掉 host:port 中的端口，IPv4/IPv6 通用；无端口时原样返回
func stripPort(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// Path 无参取值(默认 URL 路径)；传参设置路由 path
func (this *HttpRequest) Path(set ...string) string {
	if len(set) > 0 {
		this.path = set[0]
	}
	if this.path == "" {
		this.path = this.Context.Request.URL.Path
	}
	return this.path
}

// Socket HTTP 无长连接，返回 nil
func (this *HttpRequest) Socket() *cosnet.Socket {
	return nil
}

// SetMetadata 设置转发元数据
func (this *HttpRequest) SetMetadata(name, value string) {
	this.Metadata()[name] = value
}

// GetMetadata 读取转发元数据
func (this *HttpRequest) GetMetadata(key string) string {
	return this.Metadata().GetString(key)
}
func (this *HttpRequest) Flag(set ...message.Flag) message.Flag {
	if len(set) > 0 {
		this.flag = set[0]
	}
	return this.flag
}

// getContentType 获取内容类型
// 从请求头中获取指定的内容类型
func (this *HttpRequest) getContentType(name string, split string) string {
	t := this.Context.Request.Header.Get(name)
	if t == "" {
		return ""
	}
	arr := strings.SplitSeq(t, split)
	for s := range arr {
		if b := binder.Get(s); b != nil {
			return b.Name()
		}
	}
	return ""
}
