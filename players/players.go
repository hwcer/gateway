package players

import (
	"sync"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/gwcfg"
)

var players = sync.Map{}

// uids UID(角色ID) -> GUID(账号ID) 的全局反向映射。
// 长链接与会话表绑定的是GUID,而频道等业务语义按UID说话(一个账号可能有多个角色,
// 分属不同公会;同一时间只有一个角色在线)。按角色定位会话的操作(如踢人)经此映射换算。
var uids = sync.Map{}

// GUID 按角色ID反查账号ID,角色不在线返回空串
func GUID(uid string) string {
	v, ok := uids.Load(uid)
	if !ok {
		return ""
	}
	g, _ := v.(string)
	return g
}

// Update 更新会话数据并同步 UID->GUID 映射(换角时旧UID自动解绑)
func Update(p *session.Data, vs values.Values) {
	if p == nil || len(vs) == 0 {
		return
	}
	old := p.GetString(gwcfg.ServiceMetadataUID)
	p.Update(vs)
	syncUID(p, old)
}

// syncUID 按会话当前UID刷新映射,oldUID为变更前的UID
func syncUID(p *session.Data, oldUID string) {
	if p == nil {
		return
	}
	uid := p.GetString(gwcfg.ServiceMetadataUID)
	if uid == oldUID {
		if uid != "" {
			uids.Store(uid, p.UUID())
		}
		return
	}
	if oldUID != "" {
		uids.Delete(oldUID)
	}
	if uid != "" {
		uids.Store(uid, p.UUID())
	}
}

func Get(uuid string) *session.Data {
	v, ok := players.Load(uuid)
	if !ok {
		return nil
	}
	p, _ := v.(*session.Data)
	return p
}

func Range(fn func(*session.Data) bool) {
	players.Range(func(k, v interface{}) bool {
		if p, ok := v.(*session.Data); ok {
			return fn(p)
		}
		return true
	})
}

func Delete(p *session.Data) bool {
	if p == nil {
		return false
	}
	// 只有映射仍指向本会话才解绑:换角/转服后同一UID可能已归属别的账号,不能误删
	if uid := p.GetString(gwcfg.ServiceMetadataUID); uid != "" {
		if v, ok := uids.Load(uid); ok {
			if g, _ := v.(string); g == p.UUID() {
				uids.Delete(uid)
			}
		}
	}
	players.Delete(p.UUID())
	sock := Socket(p)
	if sock != nil {
		sock.Close()
	}
	return true
}

func Login(guid string, value values.Values) (token string, data *session.Data, err error) {
	data = session.NewData(guid, value)
	i, loaded := players.LoadOrStore(guid, data)
	if loaded {
		p, _ := i.(*session.Data)
		old := p.GetString(gwcfg.ServiceMetadataUID)
		p.Update(value)
		syncUID(p, old) //复用会话重新登录可能换了角色,旧UID要解绑
		data = p
	} else {
		syncUID(data, "")
	}
	ss := session.New(data)
	if !loaded {
		token, err = ss.New(data)
	} else {
		token, err = ss.Refresh() //刷新TOKEN 强制其他TOKEN失效
	}
	return
}
