package gateway

import (
	"bytes"
	"fmt"
	"time"

	"github.com/hwcer/cosrpc/selector"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"

	"github.com/hwcer/cosgo/registry"
	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosrpc"
	"github.com/hwcer/cosrpc/client"
	"github.com/hwcer/logger"
)

// ElapsedMillisecond 高延时请求阈值
// 当请求处理时间超过此值时，会记录告警日志
var ElapsedMillisecond = 500 * time.Millisecond

// proxy 代理转发函数
// 用于处理所有协议的请求转发，包括HTTP、TCP和WebSocket
// 参数:
//   - h: 上下文对象，包含请求的元数据、路径、缓冲区等信息
//
// 返回值:
//   - reply: 服务返回的数据
//   - err: 处理过程中的错误
func proxyRequest(ctx Proxy, path string) (reply []byte, err error) {
	// 异常捕获和错误处理
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
		if err != nil && Setting.Errorf != nil {
			reply = Setting.Errorf(err)
			err = nil
		}
	}()

	// 获取请求元数据和创建响应元数据
	req := ctx.Metadata()
	res := make(values.Metadata)

	// 获取请求路径
	//var path string
	//if path, err = h.Path(); err != nil {
	//	return nil, err
	//}

	// 路由解析和权限验证
	var p *session.Data
	var servicePath, serviceMethod string

	// 路由解析：将请求路径映射到具体的服务和方法
	servicePath, serviceMethod, err = Setting.Router(path, req)
	if err != nil {
		return nil, err
	}

	// 权限验证：验证用户是否有权限访问该服务和方法
	if p, err = Access.Verify(ctx, req, servicePath, serviceMethod); err != nil {
		return nil, err
	}

	// 设置网关地址和用户级别微服务筛选器
	req.Set(gwcfg.ServiceMetadataGateway, cosrpc.Address().Encode())
	// 使用用户级别微服务筛选器：如果用户会话中存在该服务的地址，则使用该地址
	if p != nil {
		if serviceAddress := p.GetString(gwcfg.GetServiceSelectorAddress(servicePath)); serviceAddress != "" {
			req.Set(selector.MetaDataAddress, serviceAddress)
		}
	}

	// 获取请求体
	var buff *bytes.Buffer
	if buff, err = ctx.Buffer(); err != nil {
		return nil, err
	}

	// 处理请求：可以在这里对请求进行预处理
	body, err := Setting.Request(ctx, path, req, buff.Bytes())
	if err != nil {
		return nil, err
	}

	// 性能监控：记录高延时请求
	startTime := time.Now()
	defer func() {
		if elapsed := time.Since(startTime); elapsed > ElapsedMillisecond {
			logger.Alert("发现高延时请求,TIME:%v,PATH:%v,BODY:%v", elapsed, path, string(body))
		}
	}()

	// 调用服务
	reply = make([]byte, 0)
	// 如果配置了服务前缀，则添加前缀
	if gwcfg.Options.Gate.Prefix != "" {
		serviceMethod = registry.Join(gwcfg.Options.Gate.Prefix, serviceMethod)
	}
	// 调用远程服务
	if err = client.CallWithMetadata(req, res, servicePath, serviceMethod, body, &reply); err != nil {
		return nil, err
	}

	// 处理响应
	res[gwcfg.ServiceResponseModel] = gwcfg.ResponseTypeNone

	// 如果响应元数据只有响应类型，则直接返回
	if len(res) == 1 {
		return reply, nil
	}

	// 处理登录和退出登录
	// 创建登录信息：如果响应中包含登录标志，则执行登录操作
	if guid, ok := res[gwcfg.ServicePlayerLogin]; ok {
		var token string
		if token, err = ctx.Login(guid, gwcfg.Cookies.Filter(res)); err != nil {
			return nil, err
		}
		if ss := ctx.Session(); ss != nil {
			p = ss.Data
		}
		res[gwcfg.ServicePlayerCookie] = token
	}
	// 退出登录：如果响应中包含退出登录标志，则执行退出登录操作
	if _, ok := res[gwcfg.ServicePlayerLogout]; ok {
		if err = ctx.Logout(); err != nil {
			return nil, err
		} else if p != nil {
			players.Delete(p)
		}
		p = nil
	}

	// 更新用户会话的 cookies 信息
	if p != nil {
		CookiesUpdate(res, p)
	}
	// 可以在这里对响应进行后处理
	reply, err = Setting.Response(ctx, path, res, reply)
	if err != nil {
		return nil, err
	}
	return reply, nil
}
