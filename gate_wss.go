package gateway

import (
	"net/http"
	"strings"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/coswss"
	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
	"github.com/hwcer/logger"
)

func init() {
	coswss.Options.Verify = WSVerify
	coswss.Options.Accept = WSAccept
}

const (
	WS_Auth_Sec_WebSocket_Protocol = "auth"
	// wssTokenMeta WSVerify→WSAccept 之间传递原始 token 的内部键(不出网关、不进业务 metadata)
	wssTokenMeta = "_wss_token"
)

func WSVerify(_ http.ResponseWriter, r *http.Request) (meta map[string]string, err error) {
	qs := r.URL.Query()
	if gwcfg.Options.Maintenance {
		secret := qs.Get("secret")
		if secret == "" || secret != gwcfg.Options.Developer {
			return nil, errors.ErrServerMaintenance
		}
	}
	// 优先从次级协议获取 token（格式: "auth, <token>"），其次从 query 获取
	var token string
	if proto := r.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		if parts := strings.SplitN(proto, ",", 2); len(parts) == 2 && strings.TrimSpace(parts[0]) == WS_Auth_Sec_WebSocket_Protocol {
			token = strings.TrimSpace(parts[1])
		}
	}
	if token == "" {
		token = qs.Get(session.Options.Name)
	}
	if token == "" {
		return nil, nil
	}
	// 原则：连接时可以不认证，但一旦传了 token 就必须校验通过，失败即拒绝连接
	ss := session.New()
	if err = ss.Verify(token); err != nil {
		return nil, err
	}
	//会话 id 已不透明化,账号身份存 values(见 players.Create)
	guid := ss.Data.GetString(gwcfg.ServiceMetadataGUID)
	if guid == "" {
		return nil, nil
	}
	return map[string]string{gwcfg.ServiceMetadataGUID: guid, wssTokenMeta: token}, nil
}

// WSAccept token 重连要**还原原会话**而不是新建:带 token 重连的客户端是断线后
// 自己回来的,凭 token 就能找回会话(含已选的 uid);新建会把原会话丢在表里、
// 把客户端踢回选角界面——旧实现按 guid 复用会话,这条语义必须保住。
// 还原走 players.Reconnect(Verify+Refresh+Replace+rebind),与新 secret 的下发
// (S2CSecret)同 TCP 重连一套契约。
func WSAccept(sock *cosnet.Socket, meta map[string]string) {
	if len(meta) == 0 {
		return
	}
	guid, ok := meta[gwcfg.ServiceMetadataGUID]
	if !ok {
		return
	}
	if token := meta[wssTokenMeta]; token != "" {
		if _, err := players.Reconnect(sock, token); err == nil {
			return
		}
		//token 二次校验失败(理论上到不了,WSVerify 已拒过):退回新建保连接可用
	}
	value := gwcfg.Cookies.Filter(meta)
	//顶号是 UID 级的,发生在选角回包落地时(见 players.rebind)——登录不做占用判断
	if _, err := players.Connect(sock, guid, value); err != nil {
		logger.Alert("wss session create fail:%v", err)
	}

}
