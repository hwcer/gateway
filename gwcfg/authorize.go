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

// Authorize 接口权限表。查询按**从具体到笼统**四级 fallback，见 Get。
var Authorize = authorize{
	dict:    map[string]OAuthType{},
	prefix:  map[string]OAuthType{},
	service: map[string]OAuthType{},
	v:       OAuthTypePlayer,
}

// authorize 权限档位的四级来源,优先级从高到低:
//
//	dict     接口级  精确到一条路由        Set("game","/guild/create",Player)
//	prefix   前缀级  一批路由共用          Prefix("game","/debug",OAuth)
//	service  服务级  整个微服务的默认      Service("social",Select)
//	v        全局级  兜底                  Default(Player)
//
// 🔴 **服务级不是"更长的前缀"**,两者解决的是不同问题:前缀说的是"这批路由长得像",
// 服务级说的是"这个进程能提供的上限"。典型例子是没有玩家容器的微服务(如社交服):
// 它给不了 Player 级——那一档接入层要 players.Get,而本进程根本没有玩家容器。
// 这是**整个服务的物理属性**,不该靠"记得给每条路由都写一遍"来保证。
//
// 用前缀冒充服务级还有个隐患:前缀是裸字符串匹配,注册 "/social" 会连带命中
// "/socialx/..."。服务级按**服务名精确匹配**,不会误伤。
type authorize struct {
	v         OAuthType            //全局默认
	dict      map[string]OAuthType //接口级:完整路径 → 档位
	prefix    map[string]OAuthType //前缀级:路径前缀 → 档位
	service   map[string]OAuthType //服务级:服务名 → 该服务的默认档位
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

// Get 取一条路由的权限档位,四级 fallback:接口 → 前缀 → 服务 → 全局。
//
// 惯常调用形态是 Get(servicePath, serviceMethod);传单个完整路径也可以,
// 服务名从第一段取(见 serviceOf)。
func (auth *authorize) Get(s ...string) (v OAuthType, path string) {
	path = auth.Format(s...)
	//① 接口级:精确命中即用,不再往下找
	if hit, ok := auth.dict[path]; ok {
		return hit, path
	}
	//② 前缀级:最长前缀匹配 —— 多个前缀同时命中时取最具体的,
	//避免 map 遍历顺序随机导致结果不确定
	longest := -1
	for k, pv := range auth.prefix {
		if len(k) > longest && strings.HasPrefix(path, k) {
			longest = len(k)
			v = pv
		}
	}
	if longest >= 0 {
		return
	}
	//③ 服务级:按服务名精确匹配,不是前缀 —— "/social" 不会串到 "/socialx/..."
	if hit, ok := auth.service[auth.serviceOf(path)]; ok {
		return hit, path
	}
	//④ 全局兜底
	v = auth.v
	return
}

// Service 设置**整个微服务**的默认权限档位。
//
// 用它而不是给每条路由 Set 一遍:服务级档位描述的是这个进程的能力上限
// (例如没有玩家容器的服务给不了 Player 级),将来该服务新增接口会自动继承,
// 不会因为有人忘了登记而悄悄落回全局默认。
//
// 单条例外仍用 Set 覆盖 —— 接口级优先级最高。
func (auth *authorize) Service(servicePath string, i OAuthType) {
	if auth.service == nil {
		auth.service = map[string]OAuthType{}
	}
	auth.service[auth.Format(servicePath)] = i
}

// serviceOf 取路径的服务名段:"/game/handle/x" → "/game";"/game" → "/game"。
func (auth *authorize) serviceOf(path string) string {
	if len(path) < 2 {
		return path
	}
	if i := strings.Index(path[1:], "/"); i >= 0 {
		return path[:i+1]
	}
	return path
}

// Prefix 按路径前缀设置一批路由的档位(如 Prefix("game","/debug",OAuthTypeOAuth))。
//
// ⚠ 它是**裸字符串前缀**匹配,"/social" 会连带命中 "/socialx/..."。
// 想表达"整个服务的默认档位"请用 Service —— 那个按服务名精确匹配。
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
	for p := range auth.developer {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
