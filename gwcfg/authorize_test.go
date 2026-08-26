package gwcfg

import "testing"

// TestAuthorizeFallback 四级 fallback 的优先级:接口 → 前缀 → 服务 → 全局。
//
// 这条测试钉住的是**顺序**。四张表各自都能命中同一条路径,顺序错了不会报错、
// 不会崩,只会让某些接口悄悄跑在比预期更宽或更严的档位上 —— 宽了是越权,
// 严了是整块功能报「未选角」,两种都要等真机才发现。
func TestAuthorizeFallback(t *testing.T) {
	a := authorize{
		dict:    map[string]OAuthType{},
		prefix:  map[string]OAuthType{},
		service: map[string]OAuthType{},
		v:       OAuthTypePlayer,
	}
	a.Service("social", OAuthTypeSelect)  //服务级:整个 social 默认 Select
	a.Prefix("social", "/open", OAuthTypeNone) //前缀级:比服务级更具体
	a.Set("social", "/open/secret", OAuthTypeOAuth) //接口级:最具体

	for _, c := range []struct {
		svc, method string
		want        OAuthType
		why         string
	}{
		{"social", "/open/secret", OAuthTypeOAuth, "接口级最优先,压过前缀与服务"},
		{"social", "/open/list", OAuthTypeNone, "前缀级压过服务级"},
		{"social", "/guild/info", OAuthTypeSelect, "没有更具体的,落到服务级"},
		{"social", "/whatever/new", OAuthTypeSelect, "新接口自动继承服务级"},
		{"game", "/guild/info", OAuthTypePlayer, "别的服务不受影响,落到全局默认"},
	} {
		if got, path := a.Get(c.svc, c.method); got != c.want {
			t.Fatalf("%v 得到 %v,期望 %v(%v)", path, got, c.want, c.why)
		}
	}
}

// TestAuthorizeServiceNotPrefix 服务级按**服务名精确匹配**,不是前缀。
//
// 🔴 这正是 Service 存在的理由:用 Prefix("social",...) 冒充服务级时,
// "/social" 会连带命中 "/socialxxx/..." —— 一个刚上线的、名字恰好以 social
// 开头的服务会静默继承别人的权限档位。
func TestAuthorizeServiceNotPrefix(t *testing.T) {
	a := authorize{
		dict:    map[string]OAuthType{},
		prefix:  map[string]OAuthType{},
		service: map[string]OAuthType{},
		v:       OAuthTypePlayer,
	}
	a.Service("social", OAuthTypeSelect)
	if got, path := a.Get("socialxxx", "/a/b"); got != OAuthTypePlayer {
		t.Fatalf("%v 得到 %v:服务名 socialxxx 不该命中 social 的服务级设置", path, got)
	}

	//对照组:换成 Prefix 就会误伤 —— 保留这个断言是为了说明两者不可互相替代
	b := authorize{
		dict:    map[string]OAuthType{},
		prefix:  map[string]OAuthType{},
		service: map[string]OAuthType{},
		v:       OAuthTypePlayer,
	}
	b.Prefix("social", "", OAuthTypeSelect)
	if got, _ := b.Get("socialxxx", "/a/b"); got != OAuthTypeSelect {
		t.Fatalf("对照组失效:Prefix 本应误伤 socialxxx,实得 %v。"+
			"若前缀语义已改,请同步 Service 的文档说明", got)
	}
}

// TestAuthorizeServiceOf 服务名段的切法,含单段与超短路径这两个边界。
func TestAuthorizeServiceOf(t *testing.T) {
	a := authorize{}
	for in, want := range map[string]string{
		"/game/handle/guild/info": "/game",
		"/social/guild/info":      "/social",
		"/game":                   "/game",
		"/":                       "/",
		"":                        "",
	} {
		if got := a.serviceOf(in); got != want {
			t.Fatalf("serviceOf(%q) = %q,期望 %q", in, got, want)
		}
	}
}
