package natprobe

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/pion/stun/v3"
)

func TestDecodeBindingRejectsInvalidReplies(t *testing.T) {
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	mapped := stun.XORMappedAddress{IP: net.ParseIP("198.51.100.17"), Port: 40123}
	valid := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID),
		stun.BindingSuccess, &mapped, stun.Fingerprint)
	got, err := decode(valid.Raw, request.TransactionID)
	if err != nil || got != netip.MustParseAddrPort("198.51.100.17:40123") {
		t.Fatalf("valid binding: %v %v", got, err)
	}
	corrupt := append([]byte(nil), valid.Raw...)
	corrupt[len(corrupt)-1] ^= 1
	wrongTransaction := stun.MustBuild(stun.TransactionID, stun.BindingSuccess, &mapped)
	wrongType := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingRequest, &mapped)
	missing := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess)
	private := stun.XORMappedAddress{IP: net.ParseIP("192.168.1.1"), Port: 40123}
	privateReply := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess, &private)
	for _, data := range [][]byte{{1, 2, 3}, corrupt, wrongTransaction.Raw, wrongType.Raw, missing.Raw, privateReply.Raw} {
		if _, err := decode(data, request.TransactionID); err == nil {
			t.Fatal("invalid binding accepted")
		}
	}
}

type fakeDatagrams struct {
	request *stun.Message
	writes  int
	reads   int
}

func (f *fakeDatagrams) SetDeadline(time.Time) error { return nil }
func (f *fakeDatagrams) WriteTo(data []byte, _ string) error {
	f.request = &stun.Message{Raw: append([]byte(nil), data...)}
	f.writes++
	return f.request.Decode()
}
func (f *fakeDatagrams) ReadFrom(_ []byte) ([]byte, string, error) {
	f.reads++
	mapped := stun.XORMappedAddress{IP: net.ParseIP("198.51.100.17"), Port: 40123}
	reply := stun.MustBuild(stun.NewTransactionIDSetter(f.request.TransactionID), stun.BindingSuccess, &mapped)
	if f.reads == 1 {
		return reply.Raw, "203.0.113.99:3478", nil
	}
	if f.reads == 2 {
		reply = stun.MustBuild(stun.TransactionID, stun.BindingSuccess, &mapped)
	}
	return reply.Raw, "203.0.113.1:3478", nil
}

func TestBindingValidatesServerAndTransaction(t *testing.T) {
	fake := &fakeDatagrams{}
	mapped, err := binding(context.Background(), fake, netip.MustParseAddrPort("203.0.113.1:3478"))
	if err != nil || mapped.Port() != 40123 || fake.writes != 1 || fake.reads != 3 {
		t.Fatalf("unexpected exchange: %v %v %+v", mapped, err, fake)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := binding(ctx, fake, netip.MustParseAddrPort("203.0.113.1:3478")); err == nil || fake.writes != 1 {
		t.Fatal("cancelled binding sent data")
	}
}
