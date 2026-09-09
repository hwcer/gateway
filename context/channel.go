package context

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hwcer/cosgo/binder"
	"github.com/hwcer/cosgo/values"
	"github.com/hwcer/cosrpc/client"
	"github.com/hwcer/gateway/gwcfg"
	"github.com/hwcer/logger"
)

func NewChannel(c Context) *Channel {
	return &Channel{Context: c}
}

func ChannelNameEncode(name, value string) string {
	roomArr := []string{name, value}
	roomByte, _ := json.Marshal(&roomArr)
	return string(roomByte)
}
func ChannelNameParse(s string) (k, v string, err error) {
	var r []string
	if err = json.Unmarshal([]byte(s), &r); err != nil {
		return
	}
	if len(r) < 2 {
		err = fmt.Errorf("channel Broadcast args error :%s", s)
	}
	k = r[0]
	v = r[1]
	return
}

type Channel struct {
	Context
}

// metadata 频道命令统一编码:key = 命令前缀 + ["name","value"],频道身份由key表达;
// value 仅 Kick 使用(被踢玩家UID),Join/Leave 为空
func (this *Channel) metadata(prefix, name, value string) string {
	return strings.Join([]string{prefix, ChannelNameEncode(name, value)}, "")
}

// Join 加入频道
func (this *Channel) Join(name, value string) {
	this.SetMetadata(this.metadata(gwcfg.ServicePlayerChannelJoin, name, value), "")
}

// Leave  退出频道
func (this *Channel) Leave(name, value string) {
	this.SetMetadata(this.metadata(gwcfg.ServicePlayerChannelLeave, name, value), "")
}

// Kick 将指定玩家踢出频道,uid 为被踢玩家的角色ID
// 与 Join/Leave 一样挂在请求者响应 metadata 上,由网关在回包时执行
func (this *Channel) Kick(name, value string, uid string) {
	this.SetMetadata(this.metadata(gwcfg.ServicePlayerChannelKick, name, value), uid)
}

// Broadcast  频道广播
func (this *Channel) Broadcast(path string, args any, name, value string, req values.Metadata) {
	if req == nil {
		req = values.Metadata{}
	}

	if _, ok := req[binder.HeaderContentType]; !ok {
		req[binder.HeaderContentType] = this.Accept().Name()
	}
	req[gwcfg.ServiceMessagePath] = path
	req[gwcfg.ServiceMessageChannel] = ChannelNameEncode(name, value)
	if err := client.CallWithMetadata(req, nil, gwcfg.ServiceTypeGate, "channel/broadcast", args, nil); err != nil {
		logger.Debug("频道广播失败:%v", err)
	}
}
