package players

import (
	"maps"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/channel"
	"github.com/hwcer/gateway/gwcfg"
)

// recordingStorage 记录写穿到存储的 Update 调用(内存后端的 Update 本是 no-op,
// 包一层只为观察 supersede 是否真的落盘)
type recordingStorage struct {
	session.Storage
	mu      sync.Mutex
	updates []map[string]any
}

func (f *recordingStorage) Update(p *session.Data, data map[string]any) error {
	cp := make(map[string]any, len(data))
	maps.Copy(cp, data)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, cp)
	return nil
}

func setup(t *testing.T) *recordingStorage {
	t.Helper()
	rs := &recordingStorage{Storage: session.NewMemory(64)}
	session.Options.Storage = rs
	return rs
}

func newPlayer(t *testing.T, guid string) *session.Data {
	t.Helper()
	_, data, err := Create(guid, values.Values{})
	if err != nil {
		t.Fatalf("create session error:%v", err)
	}
	return data
}

// selectUID 模拟选角回包落地(CookiesUpdate→Update 路径)
func selectUID(p *session.Data, uid string) {
	Update(p, values.Values{gwcfg.ServiceMetadataUID: uid})
}

// TestRebindTakeoverSupersedesHolder 换会话接管:表项换人、老会话 uid 清空(含存储)、
// 频道身份释放
func TestRebindTakeoverSupersedesHolder(t *testing.T) {
	rs := setup(t)
	pa := newPlayer(t, "g1")
	selectUID(pa, "8001")
	if Get("8001") != pa {
		t.Fatal("前提:选角后应入表")
	}
	channel.Join(pa, "tk", "r1")
	defer channel.Delete("tk", "r1")

	pb := newPlayer(t, "g1")
	selectUID(pb, "8001")

	if Get("8001") != pb {
		t.Fatal("接管后表项必须指向新会话")
	}
	if pa.GetString(gwcfg.ServiceMetadataUID) != "" {
		t.Fatal("被接管会话的 uid 必须清空")
	}
	if _, ok := channel.NewSetter(pa).Get("tk"); ok {
		t.Fatal("被接管会话的频道身份未释放")
	}
	written := false
	rs.mu.Lock()
	for _, u := range rs.updates {
		if v, ok := u[gwcfg.ServiceMetadataUID]; ok && v == "" {
			written = true
		}
	}
	rs.mu.Unlock()
	if !written {
		t.Fatal("uid 清除必须写穿存储(Redis 后端一致性)")
	}
}

// TestRebindSameSessionNotSuperseded Redis 后端重连还原出同 id 新实例:
// 不许处置"自己"——不清 uid、不 Replaced、频道身份随会话保留,只换表项指针。
func TestRebindSameSessionNotSuperseded(t *testing.T) {
	setup(t)
	pa := newPlayer(t, "g1")
	selectUID(pa, "8002")
	channel.Join(pa, "ss", "r1")
	defer channel.Delete("ss", "r1")

	//模拟 Redis Verify:从存储数据新建同 id 副本
	restored := session.NewData(pa.Id(), pa.Values())
	rebind(restored, "", "8002")

	if Get("8002") != restored {
		t.Fatal("同会话重连应把表项换到新实例")
	}
	if restored.GetString(gwcfg.ServiceMetadataUID) != "8002" {
		t.Fatal("重连实例不应被清 uid")
	}
	if pa.GetString(gwcfg.ServiceMetadataUID) != "8002" {
		t.Fatal("老副本是同一会话,不是被顶,uid 不该被清")
	}
	if _, ok := channel.NewSetter(restored).Get("ss"); !ok {
		t.Fatal("重连实例应保有频道记录")
	}
	//老副本事后走下线清理,不得误摘新实例的表项
	Delete(pa)
	if Get("8002") != restored {
		t.Fatal("老副本清理不得误摘重连实例的表项")
	}
}

// TestRebindConcurrentSameUID 并发抢同一角色:Swap 仲裁出唯一持有者,
// 竞争输家必须被处置(uid 清空),不得出现两个会话都自认为持有角色。
func TestRebindConcurrentSameUID(t *testing.T) {
	setup(t)
	pa := newPlayer(t, "g1")
	selectUID(pa, "8009")

	b := newPlayer(t, "g2")
	c := newPlayer(t, "g3")
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); selectUID(b, "8009") }()
	go func() { defer wg.Done(); selectUID(c, "8009") }()
	wg.Wait()

	holder := Get("8009")
	if holder != b && holder != c {
		t.Fatal("表项必须指向两个竞争者之一")
	}
	loser := c
	if holder == c {
		loser = b
	}
	if loser.GetString(gwcfg.ServiceMetadataUID) != "" {
		t.Fatal("竞争输家的 uid 必须被清空")
	}
	if holder.GetString(gwcfg.ServiceMetadataUID) != "8009" {
		t.Fatal("赢家的 uid 不应被动")
	}
	if pa.GetString(gwcfg.ServiceMetadataUID) != "" {
		t.Fatal("原持有者应已被处置")
	}
}

// TestRebindSwitchCharacter 换角:旧 uid 表项摘除、旧频道身份释放、新 uid 入表
func TestRebindSwitchCharacter(t *testing.T) {
	setup(t)
	p := newPlayer(t, "g1")
	selectUID(p, "8011")
	channel.Join(p, "sw", "r1")
	defer channel.Delete("sw", "r1")

	selectUID(p, "8012")

	if Get("8011") != nil {
		t.Fatal("换角后旧 uid 表项必须摘除")
	}
	if Get("8012") != p {
		t.Fatal("换角后新 uid 必须入表")
	}
	if _, ok := channel.NewSetter(p).Get("sw"); ok {
		t.Fatal("换角后旧频道身份必须释放")
	}
}

// disconnectEvent 掉线事件快照:必须在监听器内当场取 uid——事件返回后换角流程
// 会继续把会话的 uid 翻转成新角色,事后读指针只能看到新身份
type disconnectEvent struct {
	data *session.Data
	uid  string
}

// disconnectProbe 库的 On 无反注册,进程内挂一次全局监听;测试用原子换入信道采样,
// 不装信道时恒为 no-op,不影响其他用例
var disconnectProbe atomic.Value

func init() {
	session.On(session.EventSessionDisconnect, func(i any) {
		d, _ := i.(*session.Data)
		if d == nil {
			return
		}
		if ch, ok := disconnectProbe.Load().(chan disconnectEvent); ok && ch != nil {
			select {
			case ch <- disconnectEvent{data: d, uid: d.GetString(gwcfg.ServiceMetadataUID)}:
			default:
			}
		}
	})
}

func expectNoDisconnect(t *testing.T, ch chan disconnectEvent) {
	t.Helper()
	select {
	case e := <-ch:
		t.Fatalf("不该触发掉线事件,却收到 uid=%q", e.uid)
	default:
	}
}

// TestUpdateSwitchEmitsSimulatedLogout 换角时旧角色要走**模拟登出**:uid 翻转前
// 以旧身份触发一次 EventSessionDisconnect,业务侧凭它与真实掉线同路感知旧角色
// 下线;首次选角、uid 未变的重复设置都不发。连接与会话保留:事件后 uid 照常
// 翻转、表项照常换绑。
func TestUpdateSwitchEmitsSimulatedLogout(t *testing.T) {
	setup(t)
	p := newPlayer(t, "g-logout")

	ch := make(chan disconnectEvent, 4)
	disconnectProbe.Store(ch)
	defer disconnectProbe.Store((chan disconnectEvent)(nil))

	selectUID(p, "8021") //首次选角:没有旧角色,不发
	expectNoDisconnect(t, ch)
	Update(p, values.Values{gwcfg.ServiceMetadataUID: "8021"}) //uid 未变:不发
	expectNoDisconnect(t, ch)

	selectUID(p, "8022") //换角:旧角色 8021 模拟登出
	select {
	case e := <-ch:
		if e.data != p {
			t.Fatal("事件载荷必须是会话本身,不是拷贝")
		}
		if e.uid != "8021" {
			t.Fatalf("事件时刻会话必须仍持旧uid(旧身份登出),拿到 %q", e.uid)
		}
	default:
		t.Fatal("换角必须为旧角色触发一次掉线事件")
	}
	expectNoDisconnect(t, ch) //恰好一次

	//模拟登出不影响换角本身:uid 翻转、旧键摘除、新键入表
	if p.GetString(gwcfg.ServiceMetadataUID) != "8022" {
		t.Fatal("换角后会话 uid 必须是 新角色")
	}
	if Get("8021") != nil || Get("8022") != p {
		t.Fatal("换角后表项必须换绑到新角色")
	}
}

// TestRebindSwitchToEmpty 换角到空(退回未选角):表项摘除、uid 清空都必须**写穿存储**,
// 否则 HTTP+Redis 下个请求还原出带旧 uid 的副本而表项已摘,按 uid 推送静默丢失;
// 旧角色的模拟登出事件也必须发(显式清空=旧角色下线)。
func TestRebindSwitchToEmpty(t *testing.T) {
	rs := setup(t)
	p := newPlayer(t, "g1")
	selectUID(p, "8031")

	ch := make(chan disconnectEvent, 4)
	disconnectProbe.Store(ch)
	defer disconnectProbe.Store((chan disconnectEvent)(nil))

	Update(p, values.Values{gwcfg.ServiceMetadataUID: ""}) //显式清空

	if Get("8031") != nil {
		t.Fatal("清空后表项必须摘除")
	}
	if p.GetString(gwcfg.ServiceMetadataUID) != "" {
		t.Fatal("清空后会话 uid 必须为空")
	}
	cleared := false
	rs.mu.Lock()
	for _, u := range rs.updates {
		if v, ok := u[gwcfg.ServiceMetadataUID]; ok && v == "" {
			cleared = true
		}
	}
	rs.mu.Unlock()
	if !cleared {
		t.Fatal("uid 清空必须写穿存储(Redis 后端一致性)")
	}
	select {
	case e := <-ch:
		if e.uid != "8031" {
			t.Fatalf("模拟登出必须以旧角色身份触发,拿到 %q", e.uid)
		}
	default:
		t.Fatal("清空 uid 必须为旧角色触发模拟登出事件")
	}
}
