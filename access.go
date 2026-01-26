package gateway

import (
	"fmt"

	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/gwcfg"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
)

// 接口权限判定 必须注册所有 options.OAuthType

var Access = access{}

func init() {
	Access.Register(gwcfg.OAuthTypeNone, Access.None)
	Access.Register(gwcfg.OAuthTypeOAuth, Access.OAuth)
	Access.Register(gwcfg.OAuthTypeSelect, Access.Player)
	Access.Register(gwcfg.OAuthTypePlayer, Access.Player)
}

type accessSocket interface {
	Socket() *cosnet.Socket
}

type accessFunc func(r Context, req values.Metadata, isMaster bool) (*session.Data, error)

type access struct {
	dict map[gwcfg.OAuthType]accessFunc
}

func (this *access) Register(l gwcfg.OAuthType, f accessFunc) {
	if this.dict == nil {
		this.dict = make(map[gwcfg.OAuthType]accessFunc)
	}
	this.dict[l] = f
}
func (this *access) Verify(c Context, req values.Metadata, servicePath, serviceMethod string) (*session.Data, error) {
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

func (this *access) oauth(r Context, req values.Metadata) (p *session.Data, err error) {
	if p, err = r.Verify(); err != nil {
		return nil, err
	} else if p == nil {
		return nil, errors.ErrLogin
	}
	return
}

// None 普通接口
func (this *access) None(r Context, req values.Metadata, isMaster bool) (p *session.Data, err error) {
	if f, ok := r.(accessSocket); ok {
		sock := f.Socket()
		req[gwcfg.ServiceMetadataSocketId] = fmt.Sprintf("%d", sock.Id())
	}
	req[gwcfg.ServiceMetadataClientIp] = r.RemoteAddr()
	return
}

// OAuth 账号登录
func (this *access) OAuth(r Context, req values.Metadata, needMaster bool) (p *session.Data, err error) {
	if p, err = this.oauth(r, req); err != nil {
		return nil, err
	}
	if f, ok := r.(accessSocket); ok {
		sock := f.Socket()
		req[gwcfg.ServiceMetadataSocketId] = fmt.Sprintf("%d", sock.Id())
	}
	req[gwcfg.ServiceMetadataGUID] = p.UUID()
	req[gwcfg.ServiceMetadataClientIp] = r.RemoteAddr()
	if needMaster && !this.IsDeveloper(p) {
		err = errors.ErrNeedGameDeveloper
	}
	return
}

// Player 必须选择角色
func (this *access) Player(r Context, req values.Metadata, needDeveloper bool) (p *session.Data, err error) {
	if p, err = this.oauth(r, req); err != nil {
		return nil, err
	}
	uid := p.GetString(gwcfg.ServiceMetadataUID)
	if uid == "" {
		return nil, errors.ErrNotSelectRole
	}
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
	if gm := p.GetInt32(gwcfg.ServiceMetadataDeveloper); gm == 1 {
		return true
	}
	return false
}
