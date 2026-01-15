package gateway

import (
	"gateway/gwcfg"
	"gateway/players"
	"gateway/token"
	"net/http"

	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/coswss"
	"github.com/hwcer/logger"
)

// init 初始化WebSocket配置
// 设置WebSocket验证和接受回调函数
func init() {
	coswss.Options.Verify = WSVerify
	coswss.Options.Accept = WSAccept
}

// WSVerify WebSocket连接验证函数
// 用于验证WebSocket连接的合法性，支持从Sec-Websocket-Protocol头或URL query参数获取认证信息
// 参数:
//   - w: HTTP响应写入器
//   - r: HTTP请求对象
//
// 返回值:
//   - meta: 验证通过后返回的元数据，包含用户GUID等信息
//   - err: 验证过程中的错误
func WSVerify(w http.ResponseWriter, r *http.Request) (meta map[string]string, err error) {
	// 记录WebSocket握手请求的相关头信息
	logger.Debug("Sec-Websocket-Extensions:%v", r.Header.Get("Sec-Websocket-Extensions"))
	logger.Debug("Sec-Websocket-Key:%v", r.Header.Get("Sec-Websocket-Key"))
	logger.Debug("Sec-Websocket-Protocol:%v", r.Header.Get("Sec-Websocket-Protocol"))
	logger.Debug("Sec-Websocket-Branch:%v", r.Header.Get("Sec-Websocket-Branch"))

	// 创建token参数对象
	args := &token.Args{}

	// 从URL query参数中获取认证信息
	query := r.URL.Query()
	if access := query.Get("access"); access != "" {
		args.Access = access
	}
	if guid := query.Get("guid"); guid != "" {
		args.Guid = guid
	}
	if secret := query.Get("secret"); secret != "" {
		args.Secret = secret
	}

	// 从Sec-Websocket-Protocol头中获取认证信息（优先级高于query参数）
	if access := r.Header.Get("Sec-Websocket-Protocol"); access != "" && len(access) >= 2 {
		args.Access = access
	}

	// 验证token：如果没有提供access或guid，则返回nil表示不需要验证// 验证 token
	if args.Access == "" && args.Guid == "" {
		return nil, values.Error("token empty")
	}
	// 验证token的合法性
	var data *token.Token
	data, err = args.Verify()
	if err != nil {
		return nil, err
	}

	// 构建返回的元数据
	meta = map[string]string{gwcfg.ServiceMetadataGUID: args.Guid}
	// 设置开发者标志
	if data.Developer {
		meta[gwcfg.ServiceMetadataDeveloper] = "1"
	} else {
		meta[gwcfg.ServiceMetadataDeveloper] = ""
	}
	return
}

// WSAccept WebSocket连接接受函数
// 用于在WebSocket连接验证通过后，处理连接的建立和用户会话的创建
// 参数:
//   - sock: 建立的WebSocket连接
//   - meta: 从WSVerify函数返回的元数据
func WSAccept(sock *cosnet.Socket, meta map[string]string) {
	// 检查元数据是否为空
	if len(meta) == 0 {
		return
	}

	// 从元数据中获取用户GUID
	uuid, ok := meta[gwcfg.ServiceMetadataGUID]
	if !ok {
		return
	}

	// 过滤并处理cookies信息
	value := gwcfg.Cookies.Filter(meta)

	// 为用户创建会话并关联到WebSocket连接
	if _, err := players.Connect(sock, uuid, value); err != nil {
		logger.Alert("wss session create fail:%v", err)
	}
}
