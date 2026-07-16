package channel

import "strings"

func Name(name, value string) string {
	return strings.Join([]string{name, value}, ".")
}
func Split(id string) (name, value string) {
	// 与 Name 对称:仅在第一个 "." 处切分,value 含 "." 时也能完整还原
	arr := strings.SplitN(id, ".", 2)
	name = arr[0]
	if len(arr) > 1 {
		value = arr[1]
	}
	return
}
