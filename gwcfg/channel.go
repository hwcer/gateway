package gwcfg

import (
	"encoding/json"
	"fmt"
)

// Channel 频道标识编解码配置:频道身份 = (name, value) 二元组(频道名+频道参数,
// 如 world+1001),经线(网关↔业务服的 metadata)上以单字符串承载。
// Format/Parse 是 Go 惯用的字符串编解码对,实现格式可整体替换(默认 JSON 数组);
// ⚠️ Parse 的输入来自后端 metadata,不可信,实现必须自行防越界/畸形
var Channel = struct {
	Format func(name, value string) string
	Parse  func(s string) (name, value string, err error)
}{
	Format: func(name, value string) string {
		arr := []string{name, value}
		b, _ := json.Marshal(&arr)
		return string(b)
	},
	Parse: func(s string) (name, value string, err error) {
		var r []string
		if err = json.Unmarshal([]byte(s), &r); err != nil {
			return
		}
		if len(r) < 2 {
			//必须return:继续执行r[0]/r[1]会越界panic,该值来自后端metadata,不可信
			return "", "", fmt.Errorf("channel name parse error:%s", s)
		}
		return r[0], r[1], nil
	},
}
