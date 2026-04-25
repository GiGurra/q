package main

import (
	"fmt"

	"fixture/cfg"

	"github.com/GiGurra/q/pkg/q"
)

func main() {
	c := q.AtCompileTime[cfg.Config](func() cfg.Config {
		return cfg.DefaultConfig()
	})
	endpoints := q.AtCompileTime[[]cfg.Endpoint](func() []cfg.Endpoint {
		return []cfg.Endpoint{
			{URL: "/api/v1/users", Method: "GET"},
			{URL: "/api/v1/users", Method: "POST"},
		}
	})
	fmt.Println("name:", c.Name)
	fmt.Println("port:", c.Port)
	fmt.Println("enabled:", c.Enabled)
	fmt.Println("tags:", c.Tags)
	for _, e := range endpoints {
		fmt.Printf("ep: %s %s\n", e.Method, e.URL)
	}
}
