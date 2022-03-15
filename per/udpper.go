package per

import (
	"context"
	"crypto/rand"
	"log"
	"net"
	"strconv"
	"time"
	"unsafe"
)

var hdPrefix uint32 = 0xdeadbeef

type Nonce [16]byte

type Header struct {
	header uint32
	txSeq  uint32
	rxSeq  uint32
	rxRecv uint32
	time   int64
	nonce  Nonce
}

type PerStatus struct {
	Time   int64
	TxSeq  uint32
	RxSeq  uint32
	RxRecv uint32
	RAddr  net.UDPAddr
}

type UdpPer struct {
	Nonce     Nonce
	Remote    string
	Port      int
	LocalPort int
	Count     uint32
	Interval  int
	Length    int
}

// PER Transmitter
//
// - status.RAddr 로 전송, 단, resolve 가 만족된 이후 전송 시작
func (p *UdpPer) sender(ctx context.Context, report chan uint32, conn *net.UDPConn, status *PerStatus) error {
	var count uint32 = 0
	var ticker *time.Ticker

	payload := make([]byte, p.Length)
	hd := (*Header)(unsafe.Pointer(&payload[0]))
	hd.header = hdPrefix
	hd.nonce = p.Nonce

	ticker = time.NewTicker(time.Duration(p.Interval) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			count++
			hd.time = time.Now().UnixMilli()
			hd.txSeq = count
			hd.rxSeq = status.RxSeq
			hd.rxRecv = status.RxRecv

			_, err := conn.WriteToUDP(payload, &status.RAddr)
			if err != nil {
				log.Printf("Fail to send #%d packets", count)
				log.Printf("%s", err)
			}
			report <- count
			if count >= p.Count {
				return nil
			}
		}
	}
}

// PER Recevier
//
// - raddr 이 nil인 경우, nonce가 맞는 패킷이 오면 raddr를 자동 설정
// - raddr 이 nil이 아닌 경우, raddr로 부터 오는 메시지만 허용
func (p *UdpPer) receiver(ctx context.Context, report chan PerStatus, raddr *net.UDPAddr, conn *net.UDPConn) error {
	payload := make([]byte, p.Length)

	var raddrs string
	if raddr != nil {
		raddrs = raddr.String()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			err := conn.SetReadDeadline(time.Now().Add(time.Millisecond * 1000))
			if err != nil {
				panic(err)
			}
			n, addr, err := conn.ReadFromUDP(payload)
			log.Printf("### result: %d bytes from %s, %s\n", n, addr, err)
			if err != nil {
				if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
					continue
				}
				return err
			}
			if n < len(payload) || addr == nil {
				// skip bad size packet
				log.Printf("Bad packet size %d from %s", n, addr.String())
				continue
			}
			if raddr != nil && addr.String() != raddrs {
				log.Printf("Unexpected remote address: %s", addr.String())
				continue
			}

			hd := (*Header)(unsafe.Pointer(&payload[0]))
			if hd.header != hdPrefix {
				log.Printf("Bad packet header %x from %s", hd.header, addr.String())
				continue
			}

			if hd.nonce != p.Nonce {
				log.Printf("Unexpected nonce from address: %s", addr.String())
				continue
			}

			if raddr == nil {
				raddr = addr
				raddrs = raddr.String()
				log.Printf("Start Remote address: %s", raddrs)
			}

			report <- PerStatus{
				Time:   hd.time,
				TxSeq:  hd.txSeq,
				RxSeq:  hd.rxSeq,
				RxRecv: hd.rxRecv,
				RAddr:  *addr,
			}
		}
	}
}

func (p *UdpPer) Run(ctx context.Context) (PerStatus, error) {
	var s PerStatus
	var raddr *net.UDPAddr
	tx_finished := false
	rx_finished := false

	if len(p.Remote) > 0 {
		raddr, _ = net.ResolveUDPAddr("udp", p.Remote+":"+strconv.Itoa(p.Port))
		s.RAddr = *raddr
	}

	laddr := net.UDPAddr{Port: p.LocalPort}
	conn, err := net.ListenUDP("udp", &laddr)
	if err != nil {
		return s, err
	}
	defer conn.Close()

	tx := make(chan uint32)
	txErr := make(chan error)
	rx := make(chan PerStatus)
	rxErr := make(chan error)

	rctx, rxCancel := context.WithCancel(ctx)

	go func() {
		err := p.receiver(rctx, rx, raddr, conn)
		rxErr <- err
	}()

	txFunc := func() {
		err = p.sender(ctx, tx, conn, &s)
		txErr <- err
	}

	if raddr != nil {
		go func() {
			txFunc()
		}()
	}

	timer := time.NewTimer(time.Hour * 24 * 365)

	for {
		select {
		case c := <-tx:
			s.TxSeq = c
		case <-txErr:
			tx_finished = true
			timer.Reset(time.Second * 10)
			log.Println("Tx Finished")
		case <-rxErr:
			rx_finished = true
			log.Println("Rx ended")
		case r := <-rx:
			s.Time = r.Time
			s.RxSeq = r.TxSeq

			if raddr == nil {
				s.RAddr = r.RAddr
				raddr = &s.RAddr
				go txFunc()
			}

			if s.RxSeq == p.Count {
				rxCancel()
			}

		case <-timer.C:
			rxCancel()
		}

		if tx_finished && rx_finished {
			defer rxCancel()
			return s, nil
		}

	}
}

func New(remote string, port int, localPort int, count uint32, interval int, length int, nonce *Nonce) *UdpPer {
	var n Nonce
	if nonce == nil {
		_, err := rand.Read(n[:])
		if err != nil {
			panic("Fail to generate random nonce")
		}
	} else {
		copy(n[:], nonce[:])
	}

	return &UdpPer{
		Nonce:     n,
		Remote:    remote,
		Port:      port,
		LocalPort: localPort,
		Count:     count,
		Interval:  interval,
		Length:    length,
	}
}
