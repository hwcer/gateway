package gateway

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
	"github.com/hwcer/gateway/token"

	"github.com/hwcer/cosgo/binder"
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
	return s
}

// TcpServer TCP服务器结构体
// 用于处理TCP长连接请求
type TcpServer struct {
	//Errorf func(*cosnet.Context, error) any
}

// init 初始化TCP服务器
// 设置心跳管理、事件回调和服务注册
// 返回值:
//   - error: 初始化过程中的错误
func (this *TcpServer) init() error {
	// 关闭 cosnet 计时器,由session接管
	cosnet.Options.Heartbeat = 0
	session.Heartbeat.On(cosnet.Heartbeat)

	// 注册事件回调
	cosnet.On(cosnet.EventTypeReplaced, this.S2CReplaced)
	cosnet.On(cosnet.EventTypeDisconnect, this.Disconnect)
	cosnet.On(cosnet.EventTypeAuthentication, this.S2CSecret)

	// 注册服务
	service := cosnet.Service()
	_ = service.Register(this.proxy, "*")      // 注册代理服务，处理所有请求
	_ = service.Register(this.C2SPing, "ping") // 注册心跳服务
	if Setting.C2SOAuth != "" {
		_ = service.Register(this.C2SOAuth, Setting.C2SOAuth) // 注册认证服务
	}
	_ = service.Register(this.C2SReconnect, "C2SReconnect") // 注册重连服务

	// 设置序列化器
	h := service.Handler().(*cosnet.Handler)
	h.SetSerialize(this.serialize)
	return nil
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
	return Setting.Serialize(c, reply)
}

// Listen 监听TCP端口
// 参数:
//   - address: 监听地址
//
// 返回值:
//   - error: 监听过程中的错误
func (this *TcpServer) Listen(address string) error {
	_, err := cosnet.Listen(address)
	if err == nil {
		logger.Trace("网关长连接启动：%v", gwcfg.Options.Gate.Address)
	}
	return err
}

// Accept 接受TCP连接
// 参数:
//   - ln: 监听器
//
// 返回值:
//   - error: 接受连接过程中的错误
func (this *TcpServer) Accept(ln net.Listener) error {
	cosnet.Accept(&tcp.Listener{Listener: ln})
	logger.Trace("网关长连接启动：%v", gwcfg.Options.Gate.Address)
	return nil
}

// C2SPing 处理心跳请求
// 参数:
//   - c: cosnet上下文
//
// 返回值:
//   - any: 当前时间戳（毫秒）
func (this *TcpServer) C2SPing(c *cosnet.Context) any {
	ms := time.Now().UnixMilli()
	s := strconv.Itoa(int(ms))
	return []byte(s)
}

// C2SOAuth 处理认证请求
// 参数:
//   - c: cosnet上下文
//
// 返回值:
//   - any: 认证结果
func (this *TcpServer) C2SOAuth(c *cosnet.Context) any {
	var err error
	args := &token.Args{}
	if err = c.Bind(&args); err != nil {
		return err
	}
	// 验证token
	data, err := args.Verify()
	if err != nil {
		return err
	}
	// 创建 socket 代理并登录
	h := socketProxy{Context: c}
	vs := values.Values{}
	if data.Developer {
		vs.Set(gwcfg.ServiceMetadataDeveloper, "1")
	} else {
		vs.Set(gwcfg.ServiceMetadataDeveloper, "")
	}
	if _, err = h.Login(data.Openid, vs); err != nil {
		return err
	}

	if Setting.G2SOAuth == "" {
		return nil
	}

	var reply []byte
	if reply, err = proxy(Setting.G2SOAuth, &h, nil); err != nil {
		return err
	}
	return reply
}

// S2CSecret 发送断线重连密钥
// 默认的发送断线重连密钥
// 参数:
//   - sock: cosnet socket
//   - _: 事件数据（未使用）
func (this *TcpServer) S2CSecret(sock *cosnet.Socket, _ any) {
	data := sock.Data()
	if data == nil {
		return
	}
	ss := session.New(data)
	if s, err := ss.Token(); err != nil {
		sock.Errorf(err)
	} else if Setting.S2CSecret != nil {
		Setting.S2CSecret(sock, s)
	} else {
		sock.Send(0, "S2CSecret", []byte(s))
	}
}

// S2CReplaced 顶号提示
// 默认的顶号提示
// 参数:
//   - sock: cosnet socket
//   - i: 事件数据，包含顶号IP
func (this *TcpServer) S2CReplaced(sock *cosnet.Socket, i any) {
	if sock == nil {
		return
	}
	ip, ok := i.(string)
	if !ok {
		return
	}
	if Setting.S2CReplaced != nil {
		Setting.S2CReplaced(sock, ip)
	} else {
		sock.Send(0, "S2CReplaced", []byte(ip))
	}
}

// C2SReconnect 处理重连请求
// 参数:
//   - c: cosnet上下文
//
// 返回值:
//   - any: 重连结果
func (this *TcpServer) C2SReconnect(c *cosnet.Context) any {
	secret := string(c.Message.Body())
	if secret == "" {
		return values.Error("secret empty")
	}
	if _, err := players.Reconnect(c.Socket, secret); err != nil {
		return err
	}
	return true
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
	h := socketProxy{Context: c}
	reply, err := proxy(path, &h, nil)
	if err != nil {
		return err
	}
	return reply
}

// socketProxy socket代理结构体
// 实现gwcfg.Context接口，用于TCP请求的代理
type socketProxy struct {
	*cosnet.Context
}

// Verify 验证会话
// 返回值:
//   - *session.Data: 会话数据
//   - error: 验证过程中的错误
func (this *socketProxy) Verify() (*session.Data, error) {
	data := this.Context.Socket.Data()
	if data == nil {
		return nil, session.ErrorSessionNotExist
	}
	return data, nil
}

// Login 登录
// 参数:
//   - guid: 用户GUID
//   - value: 登录值
//
// 返回值:
//   - token: 登录令牌
//   - error: 登录过程中的错误
func (this *socketProxy) Login(guid string, value values.Values) (token string, err error) {
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

// Logout 登出
// 返回值:
//   - error: 登出过程中的错误
func (this *socketProxy) Logout() error {
	this.Context.Socket.Close()
	return nil
}

// Socket 获取socket
// 返回值:
//   - *cosnet.Socket: cosnet socket
func (this *socketProxy) Socket() *cosnet.Socket {
	return this.Context.Socket
}

// Buffer 获取请求体
// 返回值:
//   - *bytes.Buffer: 请求体缓冲区
//   - error: 获取过程中的错误
func (this *socketProxy) Buffer() (buf *bytes.Buffer, err error) {
	buff := bytes.NewBuffer(this.Context.Message.Body())
	return buff, nil
}

// Metadata 获取请求元数据
// 返回值:
//   - values.Metadata: 请求元数据
func (this *socketProxy) Metadata() values.Metadata {
	meta := values.Metadata{}
	if _, q, _ := this.Context.Path(); q != "" {
		query, _ := url.ParseQuery(q)
		for k, _ := range query {
			meta[k] = query.Get(k)
		}
	}
	magic := this.Message.Magic()
	meta[binder.HeaderContentType] = magic.Binder.Name()
	meta[gwcfg.ServiceMetadataRequestId] = fmt.Sprintf("%d", this.Context.Message.Index())
	return meta
}

// RemoteAddr 获取远程地址
// 返回值:
//   - string: 远程地址
func (this *socketProxy) RemoteAddr() string {
	ip := this.Context.RemoteAddr().String()
	if i := strings.Index(ip, ":"); i > 0 {
		ip = ip[0:i]
	}
	return ip
}
