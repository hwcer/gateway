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
// 被接管会话不得夺回角色——🔴 但机制不是"清 uid 写穿",而是同账号模型下
// 接管者的登录 Create 已 DEL+HMSET 重建共享记录(账号=存储键):被顶者的旧
// secret 必然 Verify 失效,连还原都还原不出来。supersede 只清内存副本,
// 不写穿——写穿反而会把新持有者刚落的 uid 覆盖成空(见
// TestRedisSameGuidTakeoverKeepsNewHolderUid)。旧版用不同 guid 构造"跨账号
// 接管"来断言写穿,那是业务层归属校验拦死的不可达路径,测的是幽灵语义。
func TestRedisSupersedeOldTokenRevoked(t *testing.T) {
	setupRedisTests(t)
	ss := TCP.Sockets
	const guid = "g-redis-sup"
	const uid = "9101"

	tokenA, _, err := players.Create(guid, values.Values{})
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

	//B 同账号登录并接管同一角色 → supersede(A)
	_, pb, err := players.Create(guid, values.Values{})
	if err != nil {
		t.Fatalf("create B error:%v", err)
	}
	players.Update(pb, values.Values{gwcfg.ServiceMetadataUID: uid})
	if players.Get(uid) != pb {
		t.Fatal("前提:B 应已接管")
	}

	//A 的旧 token(含重连后 Refresh 的现役 token)必须已随记录重建而失效
	tokenA2, err := session.New(pa).Token()
	if err != nil {
		t.Fatalf("token error:%v", err)
	}
	if err = session.New().Verify(tokenA2); err == nil {
		t.Fatal("被顶者的旧 secret 必须失效(Create 重建共享记录),否则夺回防护失效")
	}

	//旧 token 重连直接被拒,不存在"还原出带 uid 的副本"这条夺回路径
	sockC, stopC := newReplacedTestSocket(t, ss)
	defer stopC()
	if _, err = players.Reconnect(sockC, tokenA2); err == nil {
		t.Fatal("被顶者持旧 secret 重连必须被拒")
	}
	//角色仍归 B,存储记录的 uid 也仍是 B 的
	if players.Get(uid) != pb {
		t.Fatal("角色归属必须保持为接管者")
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

// TestRedisSameGuidTakeoverKeepsNewHolderUid 🔴 同账号双设备顶号(账号=存储键,
// 同 guid 会话共享一条记录):接管者的 uid 写穿**不得**被 supersede 的清空写穿覆盖。
// 旧实现 supersede 无条件 Submit,把新持有者刚落的 uid 盖成空串——双设备顶号后
// 新端一重连/Verify 即落地为未选角。跨账号场景仍写穿(见 TestRedisSupersedeClearsUidInStorage)。
func TestRedisSameGuidTakeoverKeepsNewHolderUid(t *testing.T) {
	setupRedisTests(t)
	const guid = "g-redis-same"
	const uid = "9102"

	tokenA, _, err := players.Create(guid, values.Values{})
	if err != nil {
		t.Fatalf("create A error:%v", err)
	}
	sockA, stopA := newReplacedTestSocket(t, TCP.Sockets)
	defer stopA()
	if _, err = players.Reconnect(sockA, tokenA); err != nil {
		t.Fatalf("reconnect A error:%v", err)
	}
	pa := sockA.Data()
	players.Update(pa, values.Values{gwcfg.ServiceMetadataUID: uid})
	if players.Get(uid) != pa {
		t.Fatal("前提:A 选角后应入表")
	}

	//B 同账号登录并接管同一角色
	tokenB, pb, err := players.Create(guid, values.Values{})
	if err != nil {
		t.Fatalf("create B error:%v", err)
	}
	players.Update(pb, values.Values{gwcfg.ServiceMetadataUID: uid})
	if players.Get(uid) != pb {
		t.Fatal("前提:B 应已接管")
	}

	//存储记录(账号=键)的 uid 必须仍是 B 刚写的值——supersede 不得覆盖
	s := session.New()
	if err = s.Verify(tokenB); err != nil {
		t.Fatalf("B 的 token 必须可还原:%v", err)
	}
	if got := s.Data.GetString(gwcfg.ServiceMetadataUID); got != uid {
		t.Fatalf("同账号接管后存储 uid 被覆盖成 %q,新持有者重连将变未选角(want %q)", got, uid)
	}
}
