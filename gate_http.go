package gateway

import (
	"net"
	"net/http"
	"net/url"
	"strings"

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
// 返回值:
//   - *HttpServer: HTTP服务器实例
func NewHttpServer() *HttpServer {
	s := &HttpServer{}
	return s
}

// HttpServer HTTP服务器结构体
// 用于处理HTTP短连接请求
type HttpServer struct {
	*cosweb.Server
	redis any //是否使用redis存储session信息
}

// init 初始化HTTP服务器
// 设置跨域、注册服务和序列化器
// 返回值:
//   - error: 初始化过程中的错误
func (this *HttpServer) init() (err error) {
	this.Server = cosweb.New()
	// 跨域设置
	allow := middleware.NewAccessControlAllow()
	allow.Origin("*")
	allow.Methods(Method...)
	allow.Headers(strings.Join(Headers, ","))
	this.Server.Use(allow.Handle)
	// 注册服务
	if Setting.C2SOAuth != "" {
		this.Server.Register(Setting.C2SOAuth, this.oauth) // 注册认证服务
	}
	this.Server.Register("*", this.proxy, Method...) // 注册代理服务，处理所有POST请求
	// 设置序列化器
	service := this.Server.Service()
	h := service.Handler().(*cosweb.Handler)
	h.SetSerialize(this.serialize)

	// 静态文件服务
	if gwcfg.Options.Gate.Static != nil && gwcfg.Options.Gate.Static.Root != "" {
		static := this.Server.Static(gwcfg.Options.Gate.Static.Route, gwcfg.Options.Gate.Static.Root, http.MethodGet)
		if gwcfg.Options.Gate.Static.Index != "" {
			static.Index(gwcfg.Options.Gate.Static.Index)
		}
	}
	return nil
}

// serialize 序列化函数
// 用于序列化响应数据
// 参数:
//   - c: cosweb上下文
//   - reply: 要序列化的数据
//
// 返回值:
//   - []byte: 序列化后的数据
//   - error: 序列化过程中的错误
func (this *HttpServer) serialize(c *cosweb.Context, reply any) ([]byte, error) {
	return Setting.Serialize(c, reply)
}

// Listen 监听HTTP端口
// 参数:
//   - address: 监听地址
//
// 返回值:
//   - error: 监听过程中的错误
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
// 参数:
//   - ln: 监听器
//
// 返回值:
//   - error: 接受连接过程中的错误
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
// 参数:
//   - c: cosweb上下文
//
// 返回值:
//   - any: 认证结果，包含会话密钥
func (this *HttpServer) oauth(c *cosweb.Context) any {
	args := &token.Args{}
	if err := c.Bind(&args); err != nil {
		return err
	}
	// 验证 token
	data, err := args.Verify()
	if err != nil {
		return err
	}
	// 创建 http 代理并登录
	h := HttpContent{Context: c}
	vs := values.Values{}
	if data.Developer {
		vs.Set(gwcfg.ServiceMetadataDeveloper, "1")
	} else {
		vs.Set(gwcfg.ServiceMetadataDeveloper, "")
	}

	// 构建响应
	cookie := map[string]string{}
	cookie["key"] = session.Options.Name
	if cookie["val"], err = h.login(data.Openid, vs); err != nil {
		return err
	}
	if Setting.G2SOAuth == "" {
		return cookie
	}
	var reply []byte
	if reply, err = proxy(Setting.G2SOAuth, &h); err != nil {
		return err
	}
	return reply
}

// proxy 处理HTTP请求代理
// 参数:
//   - c: cosweb上下文
//
// 返回值:
//   - any: 代理结果
func (this *HttpServer) proxy(c *cosweb.Context) (r any) {
	// 创建 http 代理并处理请求
	h := HttpContent{Context: c}
	reply, err := proxy(c.Request.URL.Path, &h)
	if err != nil {
		return err
	}
	return reply
}

// HttpContent HTTP代理结构体
// 实现gwcfg.Context接口，用于HTTP请求的代理
type HttpContent struct {
	*cosweb.Context
	uri *url.URL
	//data *session.Data
	//cookie   *http.Cookie
	metadata values.Metadata
}

// Login 登录
// 参数:
//   - guid: 用户GUID
//   - value: 登录值
//
// 返回值:
//   - token: 登录令牌
//   - error: 登录过程中的错误
func (this *HttpContent) login(guid string, value values.Values) (token string, err error) {
	var data *session.Data
	token, data, err = players.Login(guid, value)
	if err != nil {
		return
	}
	// 长连接顶号：如果用户已在其他地方登录，会顶掉旧连接
	players.Replace(data, nil, this.Context.RemoteAddr())

	// 设置cookie
	cookie := &http.Cookie{Name: session.Options.Name, Path: "/", Value: token}
	http.SetCookie(this.Context.Response, cookie)
	// 设置响应头
	header := this.Context.Header()
	header.Set("X-Forwarded-Key", session.Options.Name)
	header.Set("X-Forwarded-Val", cookie.Value)
	//this.Context.Set(session.Setting.Name, cookie.Value)
	//this.cookie = cookie
	this.Context.Session = session.New(data)
	return
}

// Logout 登出
// 返回值:
//   - error: 登出过程中的错误
func (this *HttpContent) logout() error {
	return this.Context.Session.Delete()
}

// Verify 验证会话
// 返回值:
//   - *session.Data: 会话数据
//   - error: 验证过程中的错误
func (this *HttpContent) verify() (*session.Data, error) {
	// 如果会话已存在且有效，直接返回
	if this.Context.Session != nil && this.Context.Session.Data != nil {
		return this.Context.Session.Data, nil
	}
	// 获取 token
	//var s string
	//if this.cookie != nil {
	//	s = this.cookie.Value
	//} else {
	//	s = this.Context.GetString(session.Options.Name, cosweb.RequestDataTypeCookie, cosweb.RequestDataTypeQuery, cosweb.RequestDataTypeHeader)
	//}
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

func (this *HttpContent) Header() values.Metadata {
	// 设置 Content-Type
	r := make(values.Metadata)
	if t := this.getContentType(binder.HeaderContentType, ";"); t != "" {
		r.Set(binder.HeaderContentType, t)
	} else {
		r.Set(binder.HeaderContentType, gwcfg.Options.Binder)
	}
	// 设置 Accept
	if t := this.getContentType(binder.HeaderAccept, ","); t != "" {
		r.Set(binder.HeaderAccept, t)
	}
	return r
}
func (this *HttpContent) Session() *session.Session {
	if ss := this.Context.Session; ss != nil && ss.Data != nil {
		return ss
	}
	return nil
}

// Metadata 获取请求元数据
// 返回值:
//   - values.Metadata: 请求元数据
func (this *HttpContent) Metadata() values.Metadata {
	if this.metadata != nil {
		return this.metadata
	}
	this.metadata = make(values.Metadata)
	// 从 URL 查询参数中获取元数据
	q := this.Context.Request.URL.Query()
	for k, _ := range q {
		this.metadata[k] = q.Get(k)
	}
	return this.metadata
}

// RemoteAddr 获取远程地址
// 返回值:
//   - string: 远程地址
func (this *HttpContent) RemoteAddr() string {
	ip := this.Context.RemoteAddr()
	if i := strings.Index(ip, ":"); i > 0 {
		ip = ip[0:i]
	}
	return ip
}

// getContentType 获取内容类型
// 从请求头中获取指定的内容类型
// 参数:
//   - name: 头名称
//   - split: 分隔符
//
// 返回值:
//   - string: 内容类型
func (this *HttpContent) getContentType(name string, split string) string {
	t := this.Context.Request.Header.Get(name)
	if t == "" {
		return ""
	}
	arr := strings.Split(t, split)
	for _, s := range arr {
		if b := binder.Get(s); b != nil {
			return b.Name()
		}
	}
	return ""
}
