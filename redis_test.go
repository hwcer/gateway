package gateway

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/gateway/players"
)

// newRedisStorage 连接本地 Redis 做真实双后端集成测试;
// 不可达时 skip(CI 无 Redis 也不断)。用纳秒前缀隔离键,避免污染/串扰。
func newRedisStorage(t *testing.T) *session.Redis {
	t.Helper()
	addr := os.Getenv("GATEWAY_TEST_REDIS")
	if addr == "" {
		//本地开发默认(docker redis,requirepass=123456);环境变量可覆盖
		addr = "tcp://127.0.0.1:6379?password=123456"
	}
	r, err := session.NewRedis(addr, fmt.Sprintf("gw-it-%d", time.Now().UnixNano()))
	if err != nil {
		t.Skipf("redis unavailable:%v", err)
	}
	//功能探测:Redis 连接是惰性的,写读一次才知道通不通
	probe := session.NewData("probe", values.Values{"ok": 1}) //空 map 会让 HMSET 报参数错误
	if err = r.New(probe); err != nil {
		t.Skipf("redis unavailable:%v", err)
	}
	if _, err = r.Get("probe"); err != nil {
		t.Skipf("redis unavailable:%v", err)
	}
	return r
}

// setupRedisTests Redis 后端专属基建:网络栈 + Redis 存储(结束恢复)
func setupRedisTests(t *testing.T) {
	t.Helper()
	newTestSockets()        //网络栈(内部先设内存存储)
	r := newRedisStorage(t) //再探测 Redis,不通则 skip
	old := session.Options.Storage
	session.Options.Storage = r
	t.Cleanup(func() {
		session.Options.Storage = old
	})
}

// TestRedisCreateSecretWriteThrough 秘钥必须写穿存储:Create 之后,
// 一个"全新"的 Verify(等价 Redis 后端从存储还原)必须能用 token 还原会话。
// 内存后端同实例测不出这个差异,历史上漏过。
func TestRedisCreateSecretWriteThrough(t *testing.T) {
	setupRedisTests(t)
	token, _, err := players.Create("g-redis-wt", values.Values{})
	if err != nil {
		t.Fatalf("create error:%v", err)
	}
	s := session.New()
	if err = s.Verify(token); err != nil {
		t.Fatalf("Create 后从存储 Verify 必须成功(秘钥写穿),拿到:%v", err)
	}
}

// TestRedisDoubleReconnect 断线重连必须可重复:Reconnect 内部 Refresh 了秘钥,
// 必须写穿存储,否则第二次重连 Verify 还原出旧秘钥、前缀比对失败,
// 重连永久失效(Redis 后端专属 bug,-race/内存后端测不出)。
func TestRedisDoubleReconnect(t *testing.T) {
	setupRedisTests(t)
	ss := TCP.Sockets
	token, _, err := players.Create("g-redis-2r", values.Values{})
	if err != nil {
		t.Fatalf("create error:%v", err)
	}
	const uid = "9100"

	//第一次重连
	sockA, stopA := newReplacedTestSocket(t, ss)
	defer stopA()
	if _, err = players.Reconnect(sockA, token); err != nil {
		t.Fatalf("第一次重连失败:%v", err)
	}
	players.Update(sockA.Data(), values.Values{gwcfg.ServiceMetadataUID: uid})
	if players.Get(uid) != sockA.Data() {
		t.Fatal("前提:重连后应入表")
	}

	//第二次重连:token 来自第一次 Refresh 后的内存态(新秘钥),
	//存储里若没写穿,这里 Verify 还原出旧秘钥 → ErrorSessionReplaced
	token2, err := session.New(sockA.Data()).Token()
	if err != nil {
		t.Fatalf("token error:%v", err)
	}
	sockB, stopB := newReplacedTestSocket(t, ss)
	defer stopB()
	pb, err := players.Reconnect(sockB, token2)
	if err != nil {
		t.Fatalf("第二次重连失败(秘钥未写穿存储):%v", err)
	}
	if players.Get(uid) != pb {
		t.Fatal("第二次重连后表项必须指向新连接的会话")
	}
}

// TestRedisSupersedeClearsUidInStorage 被接管会话的 uid 清除必须写穿存储:
// Redis 后端每次 Verify 都从存储新建副本,只清内存的话它持 secret 重连会
// 还原出带 uid 的副本并经 rebind 夺回角色。正确行为:重连落地为未选角状态。
func TestRedisSupersedeClearsUidInStorage(t *testing.T) {
	setupRedisTests(t)
	ss := TCP.Sockets
	const uid = "9101"

	tokenA, _, err := players.Create("g-redis-sup-a", values.Values{})
	if err != nil {
		t.Fatalf("create A error:%v", err)
	}
	sockA, stopA := newReplacedTestSocket(t, ss)
	defer stopA()
	if _, err = players.Reconnect(sockA, tokenA); err != nil {
		t.Fatalf("reconnect A error:%v", err)
	}
	pa := sockA.Data()
	players.Update(pa, values.Values{gwcfg.ServiceMetadataUID: uid})
	if players.Get(uid) != pa {
		t.Fatal("前提:A 选角后应入表")
	}

	//B(另一账号)接管同一角色 → supersede(A)
	_, pb, err := players.Create("g-redis-sup-b", values.Values{})
	if err != nil {
		t.Fatalf("create B error:%v", err)
	}
	players.Update(pb, values.Values{gwcfg.ServiceMetadataUID: uid})
	if players.Get(uid) != pb {
		t.Fatal("前提:B 应已接管")
	}

	//从存储还原 A:uid 必须已清空(空即未选角),不得经 rebind 夺回角色
	//⚠️ 用 A 重连后的现役 token——Reconnect 的 Refresh 已作废初始 token
	tokenA2, err := session.New(pa).Token()
	if err != nil {
		t.Fatalf("token error:%v", err)
	}
	s := session.New()
	if err = s.Verify(tokenA2); err != nil {
		t.Fatalf("A 的 token 必须仍可还原:%v", err)
	}
	if got := s.Data.GetString(gwcfg.ServiceMetadataUID); got != "" {
		t.Fatalf("A 的 uid 清除必须写穿存储,存储里仍是 %q", got)
	}

	//A 持 secret 重连:落地为未选角,角色仍归 B
	sockC, stopC := newReplacedTestSocket(t, ss)
	defer stopC()
	pc, err := players.Reconnect(sockC, tokenA2)
	if err != nil {
		t.Fatalf("A 重连失败:%v", err)
	}
	if got := pc.GetString(gwcfg.ServiceMetadataUID); got != "" {
		t.Fatalf("被接管会话重连后必须为未选角状态,拿到 %q", got)
	}
	if players.Get(uid) != pb {
		t.Fatal("重连不得夺回已被接管的角色")
	}
}

// TestRedisRebindUidSurvivesStorageRoundTrip uid 落地必须写穿:
// 新实例从存储还原后仍带 uid,并经 rebind 幂等入表(双实例是 Redis 形态)。
func TestRedisRebindUidSurvivesStorageRoundTrip(t *testing.T) {
	setupRedisTests(t)
	ss := TCP.Sockets
	const uid = "9102"
	token, _, err := players.Create("g-redis-rb", values.Values{})
	if err != nil {
		t.Fatalf("create error:%v", err)
	}
	sockA, stopA := newReplacedTestSocket(t, ss)
	defer stopA()
	pa, err := players.Reconnect(sockA, token)
	if err != nil {
		t.Fatalf("reconnect error:%v", err)
	}
	players.Update(pa, values.Values{gwcfg.ServiceMetadataUID: uid})

	//直接从存储还原(等价另一进程/另一请求的 Verify):uid 必须在
	//⚠️ 用重连后的现役 token(初始 token 已被 Reconnect 的 Refresh 作废)
	token2, err := session.New(pa).Token()
	if err != nil {
		t.Fatalf("token error:%v", err)
	}
	s := session.New()
	if err = s.Verify(token2); err != nil {
		t.Fatalf("verify error:%v", err)
	}
	if got := s.Data.GetString(gwcfg.ServiceMetadataUID); got != uid {
		t.Fatalf("uid 必须写穿存储,存储里是 %q", got)
	}
	//还原实例经 rebind 幂等入表,表项换到新实例
	sockB, stopB := newReplacedTestSocket(t, ss)
	defer stopB()
	pb, err := players.Reconnect(sockB, token2)
	if err != nil {
		t.Fatalf("reconnect error:%v", err)
	}
	if players.Get(uid) != pb {
		t.Fatal("重连后表项必须指向还原的新实例")
	}
	_ = pa
}
