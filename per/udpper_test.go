package per

import (
	"context"
	"log"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUdpPerNew(t *testing.T) {
	n := New("", 5201, 5201, 100, 10, 100, nil)

	assert.NotEmpty(t, n, "New() should return a new UdpPer struct")
	assert.NotEqual(t, n.Nonce, Nonce{}, "Nonce should be initialized to zero")

	nonce := Nonce{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	n = New("hello.com", 5201, 5201, 101, 102, 103, &nonce)

	assert.Equal(t, n.Nonce, nonce, "Nonce is not initialized")
	assert.Equal(t, n.Remote, "hello.com", "Invalid remote")
	assert.EqualValues(t, n.Port, 5201, "Invalid port")
	assert.EqualValues(t, n.Count, 101, "Invalid count")
	assert.EqualValues(t, n.Interval, 102, "Invalid interval")
	assert.EqualValues(t, n.Length, 103, "Invalid length")
}

func TestUdpResolve(t *testing.T) {
	n, err := net.ResolveUDPAddr("udp", "localhost:5201")
	assert.NoError(t, err, "ResolveUDPAddr() should not return error")
	assert.Equal(t, n.Port, 5201, "Invalid port")
	assert.Equal(t, n.IP, net.IPv4(127, 0, 0, 1), "Invalid IP")
}

func TestUdpRun(t *testing.T) {
	count := uint32(10)
	interval := 1

	nonce := Nonce{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	n := New("localhost", 5201, 5201, count, interval, 100, &nonce)

	ctx := context.Background()
	s, err := n.Run(ctx)

	log.Printf("%+v", s)
	assert.NoError(t, err, "Run() should not return error")
	assert.EqualValues(t, s.TxSeq, count, "Invalid TxSeq")

}
