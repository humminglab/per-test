package per

import (
	"context"
	"crypto/rand"
	"log"
	"net"
	"strconv"
	"time"
	"unsafe"

	"github.com/RoaringBitmap/roaring"
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

type PerState struct {
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

type PerReport struct {
	TxTotal uint32
	TxValid uint32
	RxTotal uint32
	RxValid uint32
}

// PER Transmitter
//
// - status.RAddr 로 전송, 단, resolve 가 만족된 이후 전송 시작
func (p *UdpPer) sender(ctx context.Context, status chan uint32, conn *net.UDPConn, last *PerState) error {
	var count uint32 = 0
	var ticker *time.Ticker
	finish := false

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
			if finish {
				hd.txSeq = 0
			} else {
				count++
				hd.txSeq = count
			}
			hd.time = time.Now().UnixNano() / 1000000
			hd.rxSeq = last.RxSeq
			hd.rxRecv = last.RxRecv

			_, err := conn.WriteToUDP(payload, &last.RAddr)
			if err != nil {
				log.Printf("Fail to send #%d packets", count)
				log.Printf("%s", err)
			}
			if !finish {
				status <- count
			}
			if count >= p.Count {
				finish = true
			}
		}
	}
}

// PER Recevier
//
// - raddr 이 nil인 경우, nonce가 맞는 패킷이 오면 raddr를 자동 설정
// - raddr 이 nil이 아닌 경우, raddr로 부터 오는 메시지만 허용
func (p *UdpPer) receiver(ctx context.Context, status chan PerState, raddr *net.UDPAddr, conn *net.UDPConn) error {
	var last uint32
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
			err := conn.SetReadDeadline(time.Now().Add(time.Millisecond * 100))
			if err != nil {
				panic(err)
			}
			n, addr, err := conn.ReadFromUDP(payload)
			if err != nil {
				if neterr, ok := err.(net.Error); ok && neterr.Timeout() {
					break
				}
				return err
			}
			if n < len(payload) || addr == nil {
				// skip bad size packet
				log.Printf("Bad packet size %d from %s", n, addr.String())
				break
			}
			if raddr != nil && addr.String() != raddrs {
				log.Printf("Unexpected remote address: %s", addr.String())
				break
			}

			hd := (*Header)(unsafe.Pointer(&payload[0]))
			if hd.header != hdPrefix {
				log.Printf("Bad packet header %x from %s", hd.header, addr.String())
				break
			}

			if hd.nonce != p.Nonce {
				log.Printf("Unexpected nonce from address: %s", addr.String())
				break
			}

			if raddr == nil {
				raddr = addr
				raddrs = raddr.String()
				log.Printf("Start Remote address: %s", raddrs)
			}

			diff := time.Since(time.UnixMilli(hd.time))
			if hd.txSeq != 0 {
				if last < hd.txSeq {
					if last+1 != hd.txSeq {
						log.Printf("RX: Lost packet #%d !!!", hd.txSeq-1)
					}
					last = hd.txSeq
				}
				log.Printf("RX: Seq=%d, Len=%d, Delay=%s\n", hd.rxSeq, n, diff)
			}
			status <- PerState{
				Time:   hd.time,
				TxSeq:  hd.txSeq,
				RxSeq:  hd.rxSeq,
				RxRecv: hd.rxRecv,
				RAddr:  *addr,
			}
		}
	}
}

func dumpLossPacket(bitmap *roaring.Bitmap, report *PerReport) {
	lost := make([]uint32, 0, 100)
	for i := uint32(1); i < uint32(bitmap.GetCardinality()); i++ {
		if !bitmap.Contains(i) {
			lost = append(lost, i)
		}
	}

	log.Printf("Rx Lost packets: %d\n", len(lost))
	if len(lost) != 0 {
		log.Printf("Detail:\n")
		for _, seq := range lost {
			log.Printf("%d ", seq)
		}
	}
	log.Println()
	log.Println("############################################################################################")
	log.Printf("TX: Total %d, Lost %d, Lost Rate %.2f%%\n", report.TxTotal, report.TxTotal-report.TxValid, float64(report.TxTotal-report.TxValid)/float64(report.TxTotal)*100)
	log.Printf("RX: Total %d, Lost %d, Lost Rate %.2f%%\n", report.RxTotal, report.RxTotal-report.RxValid, float64(report.RxTotal-report.RxValid)/float64(report.RxTotal)*100)

	log.Println(report)
}

func (p *UdpPer) Run(ctx context.Context) (PerReport, error) {
	var s PerState
	var raddr *net.UDPAddr
	var bitmap roaring.Bitmap
	report := PerReport{}
	finish_tx := false
	finish_rx := false
	updated := false
	timerSet := false

	if len(p.Remote) > 0 {
		raddr, _ = net.ResolveUDPAddr("udp", p.Remote+":"+strconv.Itoa(p.Port))
		s.RAddr = *raddr
	}

	laddr := net.UDPAddr{Port: p.LocalPort}
	conn, err := net.ListenUDP("udp", &laddr)
	if err != nil {
		return report, err
	}
	defer conn.Close()

	tx := make(chan uint32)
	txErr := make(chan error)
	rx := make(chan PerState)
	rxErr := make(chan error)

	rctx, rxCancel := context.WithCancel(ctx)
	tctx, txCancel := context.WithCancel(ctx)
	defer txCancel()
	defer rxCancel()

	go func() {
		err := p.receiver(rctx, rx, raddr, conn)
		rxErr <- err
	}()

	txStart := func() {
		go func() {
			err = p.sender(tctx, tx, conn, &s)
			txErr <- err
		}()
	}

	if raddr != nil {
		txStart()
	}

	timer := time.NewTimer(time.Hour * 24 * 365)
	timer.Stop()

	for {
		select {
		case tSeq := <-tx:
			s.TxSeq = tSeq
			report.TxTotal = tSeq
			if tSeq == p.Count {
				finish_tx = true
				updated = true
				log.Println("Tx Finished")
			}
		case <-txErr:
			log.Println("Tx ended")
		case <-rxErr:
			log.Println("Rx ended")
		case remote := <-rx:
			if raddr == nil {
				s.RAddr = remote.RAddr
				raddr = &s.RAddr
				txStart()
			}

			s.Time = remote.Time
			if remote.TxSeq != 0 {
				if bitmap.CheckedAdd(remote.TxSeq) {
					report.RxValid++
				}
				s.RxSeq = remote.TxSeq
			}
			s.RxRecv = report.RxValid
			report.RxTotal = remote.RxSeq
			report.TxValid = remote.RxRecv

			if !finish_rx && (s.TxSeq == 0 || s.TxSeq == p.Count) {
				finish_rx = true
				updated = true
				log.Println("Rx Finished")
			}
		case <-timer.C:
			// 이 시점에서 종료를 하지 않으면 defer로 수행하게 되면 close와 섞여서 계속 connection 에러가 발생한다.
			rxCancel()
			txCancel()

			report.RxTotal = p.Count
			dumpLossPacket(&bitmap, &report)
			return report, nil
		}

		if updated {
			if timerSet {
				if !timer.Stop() {
					<-timer.C
				}
				timerSet = false
			}

			if finish_tx && finish_rx {
				if report.RxTotal == p.Count {
					// 모두 받은 경우
					timer.Reset(time.Millisecond)
				} else {
					// 마지막은 받았지만 loss 가 있는 경우
					timer.Reset(time.Second)
				}
			} else if finish_tx {
				// TX는 끝났지만 RX가 모두 받지 못한 경우
				timer.Reset(time.Second * 10)
			}
			timerSet = true
			updated = false
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
