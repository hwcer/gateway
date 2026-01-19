package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hwcer/cosnet"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"

	"github.com/hwcer/cosgo"
	"github.com/hwcer/cosgo/binder"
	"github.com/hwcer/cosgo/registry"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
)

func init() {
	cosgo.On(cosgo.EventTypStarted, func() error {
		//监控登录信息
		session.OnRelease(func(data *session.Data) {
			_ = players.Delete(data)
			channel.Release(data)
		})
		return nil
	})
}

type Accept interface {
	Accept() binder.Binder
}

var Setting = struct {
	Errorf      func(err error) []byte                                                                //格式化输出网关错误
	Router      router                                                                                //路由处理规则
	C2SOAuth    string                                                                                //网关登录,置空时不启用默认验证方式
	G2SOAuth    string                                                                                //游戏服登录验证,网关登录登录成功后继续使用GUID去游戏服验证,留空不进行验证
	Request     func(p *session.Data, path string, req values.Metadata, args []byte) ([]byte, error)  //网关转发消息时,如果数据有加密，可以在解密之后转发
	Response    func(p *session.Data, path string, res values.Metadata, reply []byte) ([]byte, error) //rpc 返回数据时
	Serialize   func(accept Accept, reply any) ([]byte, error)                                        //序列化方式
	S2CSecret   func(sock *cosnet.Socket, secret string)                                              //登录成功时给客户端发送秘钥,空值不处理
	S2CReplaced func(sock *cosnet.Socket, address string)                                             //被顶号时给客户端发送的顶号提示,空值不处理
}{
	Errorf:    defaultErrorf,
	Router:    defaultRouter,
	C2SOAuth:  "oauth",
	Request:   defaultRequest,
	Response:  defaultResponse,
	Serialize: defaultSerialize,
}

type router func(path string, req values.Metadata) (servicePath, serviceMethod string, err error)

var defaultErrorf = func(err error) []byte {
	b, _ := json.Marshal(values.Error(err))
	return b
}

// Router 默认路由处理方式
var defaultRouter router = func(path string, req values.Metadata) (servicePath, serviceMethod string, err error) {
	path = strings.TrimPrefix(path, "/")
	i := strings.Index(path, "/")
	if i < 0 {
		err = errors.ErrNotFount
		return
	}
	servicePath = registry.Formatter(path[0:i])
	serviceMethod = registry.Formatter(path[i:])
	return
}

func defaultRequest(p *session.Data, path string, req values.Metadata, args []byte) ([]byte, error) {
	return args, nil
}

func defaultResponse(p *session.Data, path string, res values.Metadata, data []byte) ([]byte, error) {
	rt := res.GetString(gwcfg.ServiceMetadataResponseType)
	if rt == gwcfg.ResponseTypeReceived && p != nil {
		i := p.Atomic()
		res[gwcfg.ServiceMetadataRequestId] = fmt.Sprintf("%d", -i)
	}
	return data, nil
}

func defaultSerialize(accept Accept, reply any) ([]byte, error) {
	b := accept.Accept()
	v := values.Parse(reply)
	return b.Marshal(v)
}
