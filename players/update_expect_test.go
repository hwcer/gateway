package players

import (
	"testing"

	"github.com/hwcer/cosgo/session"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/gateway/gwcfg"
)

// 🔴 P0 回归:推送路径的身份基线校验——推送从定位到落地之间隔着换角/顶号窗口,
// 在飞推送不得把会话翻回旧 uid(网关与业务服身份分叉),也不得帮僵尸会话经 rebind 夺回表项。

// 换角场景:基线 8001,落地前会话已换到 8002 → uid 键被丢弃,其余键照常应用
func TestUpdateExpectStaleBaselineDropsUID(t *testing.T) {
	setup(t)
	pa := newPlayer(t, "g1")
	selectUID(pa, "8001")
	selectUID(pa, "8002") //换角发生

	vs := values.Values{
		gwcfg.ServiceMetadataUID: "8001",
		"other.cookie":           "keep",
	}
	UpdateExpect(pa, vs, "8001")

	if got := pa.GetString(gwcfg.ServiceMetadataUID); got != "8002" {
		t.Fatalf("会话身份不得被在飞推送翻回,当前 uid:%q", got)
	}
	if pa.GetString("other.cookie") != "keep" {
		t.Fatal("非身份键应照常应用")
	}
	if Get("8002") != pa {
		t.Fatal("8002 表项应不受影响")
	}
	if _, ok := players.Load("8001"); ok {
		t.Fatal("不得因在飞推送产生 8001 的幽灵表项")
	}
}

// 顶号场景:僵尸会话(已被清空 uid)拿着旧基线落地,不得夺回角色
func TestUpdateExpectSupersededSessionCannotRecapture(t *testing.T) {
	setup(t)
	ph := newPlayer(t, "h1")
	selectUID(ph, "9001")
	pn := newPlayer(t, "h2")
	selectUID(pn, "9001") //顶号:pn 接管,ph 的 uid 被清空

	vs := values.Values{gwcfg.ServiceMetadataUID: "9001"}
	UpdateExpect(ph, vs, "9001")

	if got := ph.GetString(gwcfg.ServiceMetadataUID); got != "" {
		t.Fatalf("僵尸会话不得经推送夺回 uid:%q", got)
	}
	if Get("9001") != pn {
		t.Fatal("表项应仍指向新会话")
	}
}

// 基线一致时照常应用(常态推送:反复携带同一 uid)
func TestUpdateExpectMatchedBaselineApplies(t *testing.T) {
	setup(t)
	pa := newPlayer(t, "g2")
	selectUID(pa, "7001")

	vs := values.Values{gwcfg.ServiceMetadataUID: "7001", "rid": int64(7)}
	UpdateExpect(pa, vs, "7001")

	if got := pa.GetString(gwcfg.ServiceMetadataUID); got != "7001" {
		t.Fatalf("基线一致时 uid 应照常落地:%q", got)
	}
	if pa.GetString("rid") == "" {
		t.Fatal("其余键应被应用")
	}
}

// 编译期钉住:UpdateExpect 不产生事件与 rebind,身份永不因推送路径变更
var _ = func() { UpdateExpect(nil, nil, "") }

var _ = session.Setter{}
