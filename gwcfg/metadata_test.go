package gwcfg

import "testing"

// 🔴 回归:客户端 query 不得注入受信保留键——非开发者可预置 dev=1 走 GM 接口、
// 可注入 uid/sid 干扰业务身份判定。黑名单制:内置键默认禁止,业务可追加
func TestMetadataReservedBuiltins(t *testing.T) {
	for _, k := range []string{"uid", "guid", "sid", "dev", "per", "_addr", "_sock", "_rid", "_gate", "player.selector.region"} {
		if !MetadataReserved(k) {
			t.Fatalf("内置保留键 %q 应被禁止注入", k)
		}
	}
	//非保留键可自由透传
	for _, k := range []string{"name", "level", "channel_id", "uid2", "device"} {
		if MetadataReserved(k) {
			t.Fatalf("普通键 %q 不应被禁止", k)
		}
	}
}

// 业务层扩展黑名单:AddMetadataReserved 之后生效
func TestAddMetadataReserved(t *testing.T) {
	const key = "vip_level"
	if MetadataReserved(key) {
		t.Fatal("未追加前不应被禁止")
	}
	AddMetadataReserved(key)
	if !MetadataReserved(key) {
		t.Fatal("追加后应被禁止注入")
	}
	AddMetadataReserved("guild_id", "title")
	if !MetadataReserved("guild_id") || !MetadataReserved("title") {
		t.Fatal("批量追加应全部生效")
	}
}
