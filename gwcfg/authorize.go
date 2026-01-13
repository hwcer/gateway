package gwcfg

import (
	"path"
	"strings"
)

// 接口权限设置

type OAuthType int8

const (
	OAuthTypeNone   OAuthType = iota //不需要登录
	OAuthTypeOAuth                   //需要认证
	OAuthTypeSelect                  //需要选择角色,但不需要进入用户协程，无法直接操作用户数据
	OAuthTypePlayer                  // 需要选择角色,并进入用户协程 默认
)

var Authorize = authorize{dict: map[string]OAuthType{}, prefix: map[string]OAuthType{}, v: OAuthTypePlayer}

type authorize struct {
	v         OAuthType //默认
	dict      map[string]OAuthType
	prefix    map[string]OAuthType //按前缀匹配
	developer map[string]struct{}  //开发者模式，想要启用开发者,GM模式才能使用
}

func (auth *authorize) Format(s ...string) string {
	var r string
	if len(s) > 1 {
		r = path.Join(s...)
	} else if len(s) == 1 {
		r = s[0]
	} else {
		return ""
	}

	r = strings.ToLower(r)
	if !strings.HasPrefix(r, "/") {
		r = "/" + r
	}
	return r
}

func (auth *authorize) Set(servicePath, serviceMethod string, i OAuthType) {
	r := auth.Format(servicePath, serviceMethod)
	auth.dict[r] = i
}

func (auth *authorize) Get(s ...string) (v OAuthType, path string) {
	path = auth.Format(s...)
	var ok bool
	if v, ok = auth.dict[path]; ok {
		return
	}
	var k string
	for k, v = range auth.prefix {
		if strings.HasPrefix(path, k) {
			return
		}
	}
	v = auth.v
	return
}

func (auth *authorize) Prefix(servicePath, serviceMethod string, i OAuthType) {
	r := auth.Format(servicePath, serviceMethod)
	auth.prefix[r] = i
}

// Default 设置,获取默认值
func (auth *authorize) Default(l ...OAuthType) OAuthType {
	if len(l) > 0 {
		auth.v = l[0]
	}
	return auth.v
}

// SetMaster 前缀模式匹配
func (auth *authorize) SetMaster(servicePath string, serviceMethod string) {
	if auth.developer == nil {
		auth.developer = map[string]struct{}{}
	}
	r := auth.Format(servicePath, serviceMethod)
	auth.developer[r] = struct{}{}
}

func (auth *authorize) IsMaster(path string) bool {
	for p, _ := range auth.developer {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
