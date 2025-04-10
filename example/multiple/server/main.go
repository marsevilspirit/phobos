package main

import (
	"flag"

	"github.com/marsevilspirit/phobos/example"
	"github.com/marsevilspirit/phobos/server"
)

var (
	addr1 = flag.String("addr1", "localhost:30000", "server1 address")
	addr2 = flag.String("addr2", "localhost:30001", "server2 address")
)

func main() {
	flag.Parse()
	go createServer(*addr1)
	go createServer(*addr2)
	select {}
}
func createServer(addr string) {
	s := server.NewServer()
	s.RegisterWithName("HelloWorld", new(example.HelloWorld), "")
	s.Serve("tcp", addr)
}
