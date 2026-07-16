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
	return map[string]string{gwcfg.ServiceMetadataGUID: ss.Data.UUID()}, nil
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
