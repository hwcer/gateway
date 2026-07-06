package token

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/context"
	"github.com/hwcer/gateway/errors"
	"github.com/hwcer/gateway/gwcfg"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/utils"
)

type Args interface {
	GetGuid() string
	GetAccess() string
	GetSecret() string
	GetValues(*Result, context.Context) error //由业务层直接向 context.Context 写入透传给后端服务的 body/header
}

var NewArgs = func() Args {
	return &Default{}
}

// Result 默认的 认证方式
type Result struct {
	Appid     string        `json:"appid"`
	Openid    string        `json:"openid"`
	Expire    int64         `json:"expire"`
	Attach    values.Values `json:"attach"`
	Developer bool          `json:"developer"`
}

type Default struct {
	Guid   string `json:"guid"`
	Access string `json:"access"`
	Secret string `json:"secret"`
}

func (t *Default) GetGuid() string {
	return t.Guid
}
func (t *Default) GetAccess() string {
	return t.Access
}
func (t *Default) GetSecret() string {
	return t.Secret
}
func (t *Default) GetValues(*Result, context.Context) error {
	return nil //默认不透传任何参数，由业务层的 Args 实现按需设置
}

func Verify(args Args) (r *Result, err error) {
	r = &Result{}
	//是否开启 GM
	if secret := args.GetSecret(); secret != "" {
		if gwcfg.Options.Developer == "" {
			return nil, fmt.Errorf("GM commands are disabled")
		}
		if secret != gwcfg.Options.Developer {
			return nil, fmt.Errorf("GM commands error")
		}
		r.Developer = true
	}
	//GM 模式允许快速登录
	if guid := args.GetGuid(); guid != "" && r.Developer {
		if err = validateAccountComprehensive(guid); err != nil {
			return
		}
		r.Openid = guid
		return
	}
	//正常游戏模式
	access := args.GetAccess()
	if access == "" {
		return nil, session.ErrorSessionEmpty
	}
	if gwcfg.Options.Secret == "" {
		return nil, session.Errorf("Options.Secret is empty")
	}
	var s string
	if s, err = utils.Crypto.GCMDecrypt(access, gwcfg.Options.Secret, nil); err != nil {
		return nil, session.Errorf(err)
	}
	if err = json.Unmarshal([]byte(s), r); err != nil {
		return nil, session.Errorf(err)
	}
	if r.Openid == "" {
		return nil, session.Errorf("access guid empty")
	}
	if r.Expire > 0 && r.Expire < time.Now().Unix() {
		return nil, session.ErrorSessionExpired
	}
	if r.Appid != gwcfg.Options.Appid {
		return nil, session.Errorf("access appid error")
	}
	if gwcfg.Options.Maintenance && !r.Developer {
		return nil, errors.ErrServerMaintenance
	}
	return
}

var accountPattern = regexp.MustCompile(`^[a-zA-Z0-9~!@#$%^&*()_+\-=\[\]\\{}|;':",./<>?]{2,64}$`)

func validateAccountComprehensive(account string) error {
	if !accountPattern.MatchString(account) {
		return fmt.Errorf("账号规则不合法")
	}
	return nil
}
