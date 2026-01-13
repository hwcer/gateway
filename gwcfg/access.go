package gwcfg

import (
	"fmt"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosnet"
	"github.com/hwcer/yyds/errors"
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
		req[ServiceSocketId] = fmt.Sprintf("%d", sock.Id())
	}
	req[ServiceClientIp] = r.RemoteAddr()
	return
}

// OAuth 账号登录
func (this *access) OAuth(r Context, req values.Metadata, needMaster bool) (p *session.Data, err error) {
	if p, err = this.oauth(r, req); err != nil {
		return nil, err
	}
	if uuid := p.UUID(); uuid == "" {
		return nil, errors.ErrLogin
	} else {
		req[ServiceMetadataGUID] = uuid
	}
	req[ServiceClientIp] = r.RemoteAddr()
	if needMaster && !this.IsMaster(p) {
		err = errors.ErrNeedGameMaster
	}
	return
}

// Player 必须选择角色
func (this *access) Player(r Context, req values.Metadata, needMaster bool) (p *session.Data, err error) {
	if p, err = this.oauth(r, req); err != nil {
		return nil, err
	}
	if uid := p.GetString(ServiceMetadataUID); uid == "" {
		return nil, errors.ErrNotSelectRole
	}
	req[ServiceMetadataUID] = p.GetString(ServiceMetadataUID)
	if needMaster && !this.IsMaster(p) {
		err = errors.ErrNeedGameMaster
	}
	return
}

// IsMaster 是GM
func (this *access) IsMaster(p *session.Data) bool {
	if p == nil {
		return false
	}
	if gm := p.GetInt32(ServiceMetadataDeveloper); gm == 1 {
		return true
	}
	return false
}
