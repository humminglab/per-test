package main

import (
	"context"
	"flag"
	"fmt"
	"pertest/per"
)

func main() {
	fmt.Println("PER Tester v0.1")

	server := flag.Bool("s", false, "Server Mode")

	host := flag.String("h", "", "Remote Host Name")
	port := flag.Int("p", 5201, "Remote Host Port")
	count := flag.Int("n", 100, "Test Packet Count")
	interval := flag.Int("i", 100, "Interval (ms)")
	size := flag.Int("l", 100, "Packet Size (byte)")

	flag.Parse()

	if !*server && (len(*host) <= 0 || *port <= 0 || *count <= 0 || *interval <= 0 || *size <= 0) {
		fmt.Println("Invalid Parameter")
		flag.Usage()
		return
	}

	nonce := per.Nonce{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	ctx := context.Background()

	if *server {
		fmt.Println("Server Mode")
		n := per.New("", *port, *port, uint32(*count), *interval, *size, &nonce)
		n.Run(ctx)
	} else {
		fmt.Println("Client Mode")
		n := per.New(*host, *port, *port, uint32(*count), *interval, *size, &nonce)
		n.Run(ctx)
	}
}
