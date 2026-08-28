package gateway

import (
	"fmt"

	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/gwcfg"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
)

// 接口权限判定 必须注册所有 options.OAuthType

var Access = access{}

func init() {
	Access.Register(gwcfg.OAuthTypeNone, Access.None)
	Access.Register(gwcfg.OAuthTypeOAuth, Access.OAuth)
	Access.Register(gwcfg.OAuthTypeSelect, Access.Player)
	Access.Register(gwcfg.OAuthTypePlayer, Access.Player)
}

type accessFunc func(r inbound, req values.Metadata, isMaster bool) (*session.Data, error)

type access struct {
	dict map[gwcfg.OAuthType]accessFunc
}

func (this *access) Register(l gwcfg.OAuthType, f accessFunc) {
	if this.dict == nil {
		this.dict = make(map[gwcfg.OAuthType]accessFunc)
	}
	this.dict[l] = f
}
func (this *access) Verify(c inbound, req values.Metadata, servicePath, serviceMethod string) (*session.Data, error) {
	l, s := gwcfg.Authorize.Get(servicePath, serviceMethod)
	isMaster := gwcfg.Authorize.IsMaster(s)
	f, ok := this.dict[l]
	if !ok {
		return nil, fmt.Errorf("unknown authorization type: %d", l)
	}
	p, err := f(c, req, isMaster)
	if err != nil {
		return nil, err
	}
	req.Set(gwcfg.ServiceMetadataPermission, l)
	return p, nil
}

func (this *access) oauth(r inbound, req values.Metadata) (p *session.Data, err error) {
	if p, err = r.verify(); err != nil {
		return nil, err
	} else if p == nil {
		return nil, session.ErrorSessionUnknown
	}
	return
}

// None 普通接口
// SocketId 由 Forward 统一下发（所有档位一致），这里不再单独塞
func (this *access) None(r inbound, req values.Metadata, isMaster bool) (p *session.Data, err error) {
	req[gwcfg.ServiceMetadataAddress] = r.RemoteAddr()
	return
}

// OAuth 账号登录
func (this *access) OAuth(r inbound, req values.Metadata, needMaster bool) (p *session.Data, err error) {
	if p, err = this.oauth(r, req); err != nil {
		return nil, err
	}
	req[gwcfg.ServiceMetadataGUID] = p.UUID()
	req[gwcfg.ServiceMetadataAddress] = r.RemoteAddr()
	if needMaster && !this.IsDeveloper(p) {
		err = errors.ErrNeedGameDeveloper
	}
	if p.GetInt32(gwcfg.ServiceMetadataDeveloper) > 0 {
		req.Set(gwcfg.ServiceMetadataDeveloper, 1)
	}

	return
}

// Player 必须选择角色（OAuthTypeSelect 与 OAuthTypePlayer 共用）
//
// 🔴 GUID 与 SocketId 与 UID 一并下发,缺一不可 —— 业务服要往回推消息时,
// 网关的 send() 按 **GUID** 定位会话(players.Get(guid)),write() 按 SocketId
// 定位连接;UID 只是发出去之前的一道防串号校验。
//
// 游戏服看不出这个缺失:它的 handler 里 c.Player 非空,推送走 player.Send,
// 自带 GUID。但**没有玩家容器的服务**(社交服等)只能走 Context.Send 的另一条分支,
// 那条分支拿不到 GUID/SocketId 就直接丢弃并打 Alert —— 表现是"接口调通了、
// 客户端什么也没收到",而且只有翻日志才看得见。
func (this *access) Player(r inbound, req values.Metadata, needDeveloper bool) (p *session.Data, err error) {
	if p, err = this.oauth(r, req); err != nil {
		return nil, err
	}
	uid := p.GetString(gwcfg.ServiceMetadataUID)
	if uid == "" {
		return nil, errors.ErrNotSelectRole
	}
	if sock := r.Socket(); sock != nil {
		req[gwcfg.ServiceMetadataSocketId] = fmt.Sprintf("%d", sock.Id())
	}
	req[gwcfg.ServiceMetadataGUID] = p.UUID()
	req[gwcfg.ServiceMetadataUID] = uid
	if needDeveloper && !this.IsDeveloper(p) {
		err = errors.ErrNeedGameDeveloper
	}
	return
}

// IsDeveloper 开发者模式
func (this *access) IsDeveloper(p *session.Data) bool {
	if p == nil {
		return false
	}
	return p.GetInt32(gwcfg.ServiceMetadataDeveloper) > 0
}
