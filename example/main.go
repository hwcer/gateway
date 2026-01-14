package main

import (
	"fmt"
	"gateway"

	"github.com/hwcer/cosgo"
)

func main() {
	cosgo.SetBanner(banner)
	cosgo.Use(gateway.New())
	cosgo.Start(true)

}

func banner() {
	str := "\n大威天龙，大罗法咒，般若诸佛，般若巴嘛空。\n"
	fmt.Printf(str)
}
