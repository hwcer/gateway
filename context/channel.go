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

// Join 加入频道
func (this *Channel) Join(name, value string) {
	s := strings.Join([]string{gwcfg.ServicePlayerChannelJoin, name}, "")
	this.SetMetadata(s, value)
}

// Leave  退出频道
func (this *Channel) Leave(name, value string) {
	s := strings.Join([]string{gwcfg.ServicePlayerChannelLeave, name}, "")
	this.SetMetadata(s, value)
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
	if err := client.CallWithMetadata(req, nil, gwcfg.ServiceName, "channel/broadcast", args, nil); err != nil {
		logger.Debug("频道广播失败:%v", err)
	}
}
