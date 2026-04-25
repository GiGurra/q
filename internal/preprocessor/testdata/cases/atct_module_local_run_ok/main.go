package main

import (
	"fmt"

	"fixture/util"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	hash := q.AtCompileTime[uint32](func() uint32 {
		return util.CRC32("comptime!")
	})
	rev := q.AtCompileTime[string](func() string {
		return util.Reverse("comptime!")
	})
	combined := q.AtCompileTime[string](func() string {
		return util.Reverse("hello") + "/" + util.Reverse("world")
	})
	fmt.Println("hash:", hash)
	fmt.Println("rev:", rev)
	fmt.Println("combined:", combined)
}
