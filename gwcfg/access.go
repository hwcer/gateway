package gwcfg

import (
	"fmt"
	"gateway/errors"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
)

// 接口权限判定 必须注册所有 options.OAuthType

var Access = access{}

func init() {
	Access.Register(OAuthTypeNone, Access.None)
	Access.Register(OAuthTypeOAuth, Access.OAuth)
	Access.Register(OAuthTypeSelect, Access.Player)
	Access.Register(OAuthTypePlayer, Access.Player)
}

type accessSocket interface {
	Socket() *cosnet.Socket
}

type accessFunc func(r Context, req values.Metadata, isMaster bool) (*session.Data, error)

type access struct {
	dict map[OAuthType]accessFunc
}

func (this *access) Register(l OAuthType, f accessFunc) {
	if this.dict == nil {
		this.dict = make(map[OAuthType]accessFunc)
	}
	this.dict[l] = f
}
func (this *access) Verify(c Context, req values.Metadata, servicePath, serviceMethod string) (*session.Data, error) {
	l, s := Authorize.Get(servicePath, serviceMethod)
	isMaster := Authorize.IsMaster(s)
	f, ok := this.dict[l]
	if !ok {
		return nil, fmt.Errorf("unknown authorization type: %d", l)
	}
	p, err := f(c, req, isMaster)
	if err != nil {
		return nil, err
	}
	req.Set(ServiceMetadataApi, l)
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
		req[ServiceMetadataSocketId] = fmt.Sprintf("%d", sock.Id())
	}
	req[ServiceMetadataClientIp] = r.RemoteAddr()
	return
}

// OAuth 账号登录
func (this *access) OAuth(r Context, req values.Metadata, needMaster bool) (p *session.Data, err error) {
	if p, err = this.oauth(r, req); err != nil {
		return nil, err
	}
	if f, ok := r.(accessSocket); ok {
		sock := f.Socket()
		req[ServiceMetadataSocketId] = fmt.Sprintf("%d", sock.Id())
	}
	req[ServiceMetadataGUID] = p.UUID()
	req[ServiceMetadataClientIp] = r.RemoteAddr()
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
	uid := p.GetString(ServiceMetadataUID)
	if uid == "" {
		return nil, errors.ErrNotSelectRole
	}
	req[ServiceMetadataUID] = uid
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
	if gm := p.GetInt32(ServiceMetadataDeveloper); gm == 1 {
		return true
	}
	return false
}
