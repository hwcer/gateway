package gwcfg

import "testing"

// 🔴 回归:客户端 query 不得注入受信保留键——非开发者可预置 dev=1 走 GM 接口、
// 可注入 uid/sid 干扰业务身份判定。黑名单制:内置键默认禁止,业务可追加
func TestMetadataReservedBuiltins(t *testing.T) {
	for _, k := range []string{"uid", "guid", "sid", "dev", "per", "_addr", "_sock", "_gate", "player.selector.region"} {
		if !MetadataReserved(k) {
			t.Fatalf("内置保留键 %q 应被禁止注入", k)
		}
	}
	//🔴 _rid 是客户端合法传入的重连对账序号(用户拍板):不得拦,否则 HTTP 的
	//Index() 恒 0,重连对账全部错乱;TCP 侧由网关用帧头序号覆写注入,不受影响
	for _, k := range []string{"_rid"} {
		if MetadataReserved(k) {
			t.Fatalf("客户端合法键 %q 不应被拦(query 唯一通道,见 MetadataReserved 例外说明)", k)
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
