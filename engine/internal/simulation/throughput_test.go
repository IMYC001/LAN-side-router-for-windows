package simulation

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"accelerator/engine/internal/relay"
	"accelerator/engine/internal/socks"
	"accelerator/engine/internal/testproxy"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

func transferBulk(ctx context.Context, size int) error {
	server, err := testproxy.Start()
	if err != nil {
		return err
	}
	defer server.Close()
	engine, err := relay.New(socks.Client{Address: server.Address()})
	if err != nil {
		return err
	}
	defer engine.Close()
	device, cleanup, err := makeDevice(ctx, engine)
	if err != nil {
		return err
	}
	defer cleanup()
	stop := context.AfterFunc(ctx, engine.Close)
	defer stop()
	conn, err := gonet.DialContextTCP(ctx, device, tcpip.FullAddress{
		NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 9}), Port: 443,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	chunk := bytes.Repeat([]byte{0x31, 0x9a, 0x00, 0xff}, 16384)
	writeDone := make(chan error, 1)
	go func() {
		for remaining := size; remaining > 0; {
			n := min(remaining, len(chunk))
			written, err := io.CopyN(conn, bytes.NewReader(chunk[:n]), int64(n))
			if err != nil {
				writeDone <- err
				return
			}
			remaining -= int(written)
		}
		writeDone <- nil
	}()
	reply := make([]byte, len(chunk))
	var readErr error
	for remaining := size; remaining > 0; {
		n := min(remaining, len(reply))
		if _, readErr = io.ReadFull(conn, reply[:n]); readErr != nil {
			break
		}
		if !bytes.Equal(reply[:n], chunk[:n]) {
			readErr = fmt.Errorf("bulk TCP payload mismatch")
			break
		}
		remaining -= n
	}
	if readErr != nil {
		conn.Close()
	}
	writeErr := <-writeDone
	if readErr != nil {
		return readErr
	}
	if writeErr != nil {
		return writeErr
	}
	if engine.Snapshot().Failed != 0 {
		return fmt.Errorf("bulk TCP relay failure")
	}
	return nil
}

func TestTCPBulkTransfer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	const size = 8 << 20
	if err := transferBulk(ctx, size); err != nil {
		t.Fatal(err)
	}
	t.Logf("8 MiB validated in %.3fs; echo download %.1f Mbit/s (memory IP stacks + loopback SOCKS only)",
		time.Since(start).Seconds(), float64(size)*8/time.Since(start).Seconds()/1e6)
}

func BenchmarkTCPBulkTransfer(b *testing.B) {
	const size = 8 << 20
	b.SetBytes(size)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := transferBulk(ctx, size)
		cancel()
		if err != nil {
			b.Fatal(err)
		}
	}
}
