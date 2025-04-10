package main

import (
	"flag"
	"fmt"
	"net/http"

	"github.com/marsevilspirit/phobos/example"
	"github.com/marsevilspirit/phobos/server"
	"github.com/marsevilspirit/phobos/serverplugin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	addr = flag.String("addr", "localhost:30000", "server address")
)

func main() {
	flag.Parse()

	s := server.NewServer()

	metrics := serverplugin.NewMetricsPlugin()
	s.Plugins.Add(metrics)

	s.RegisterWithName("HelloWorld", new(example.HelloWorld), "")
	go s.Serve("tcp", *addr)

	// 启动 Prometheus HTTP 服务器
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		fmt.Println("Starting Prometheus metrics server on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil {
			fmt.Println("Error starting metrics server:", err)
		}
	}()

	select {}
}
