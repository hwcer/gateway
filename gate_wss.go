package gateway

import (
	"net/http"

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

// WSVerify WebSocket 握手前验证
// 维护模式下拒绝非开发者连接；有 session 凭证时自动登录，无凭证允许连接后续通过 C2SOAuth 认证
func WSVerify(_ http.ResponseWriter, r *http.Request) (meta map[string]string, err error) {
	if gwcfg.Options.Maintenance {
		secret := r.URL.Query().Get("secret")
		if secret == "" || secret != gwcfg.Options.Developer {
			return nil, errors.ErrServerMaintenance
		}
	}
	// 从 query 或 cookie 获取 session token，支持握手时自动登录
	token := r.URL.Query().Get(session.Options.Name)
	if token == "" {
		if c, e := r.Cookie(session.Options.Name); e == nil {
			token = c.Value
		}
	}
	if token == "" {
		return nil, nil
	}
	ss := session.New()
	if err = ss.Verify(token); err != nil {
		return nil, err
	}
	meta = map[string]string{gwcfg.ServiceMetadataGUID: ss.Data.UUID()}
	return meta, nil
}
func WSAccept(sock *cosnet.Socket, meta map[string]string) {
	if len(meta) == 0 {
		return
	}
	uuid, ok := meta[gwcfg.ServiceMetadataGUID]
	if !ok {
		return
	}
	value := gwcfg.Cookies.Filter(meta)
	if _, err := players.Connect(sock, uuid, value); err != nil {
		logger.Alert("wss session create fail:%v", err)
	}

}
