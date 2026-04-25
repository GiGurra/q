package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	hash := q.AtCompileTime[string](func() string {
		sum := md5.Sum([]byte("hello, comptime"))
		return hex.EncodeToString(sum[:])
	})
	repeated := q.AtCompileTime[string](func() string {
		return strings.Repeat("ab", 5)
	})
	upper := q.AtCompileTime[string](func() string {
		return strings.ToUpper("hello world")
	})
	fmt.Println("hash:", hash)
	fmt.Println("repeated:", repeated)
	fmt.Println("upper:", upper)
}
