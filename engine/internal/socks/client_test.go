package socks

import (
	"bytes"
	"testing"
)

func TestAddresses(t *testing.T) {
	for _, address := range []string{"1.2.3.4:53", "[2001:db8::1]:3074", "example.com:443"} {
		encoded, err := EncodeAddress(address)
		if err != nil {
			t.Fatal(err)
		}
		actual, err := ReadAddress(bytes.NewReader(encoded))
		if err != nil || actual != address {
			t.Fatalf("%s decoded as %s, %v", address, actual, err)
		}
	}
}

func TestRejectNonLoopbackAndBadPorts(t *testing.T) {
	for _, address := range []string{"192.168.1.1:7890", "0.0.0.0:7890", "localhost:7890", "127.0.0.1:0", "127.0.0.1:65536", "not-an-address"} {
		if (Client{Address: address}).Validate() == nil {
			t.Fatalf("accepted %s", address)
		}
	}
}

func TestMalformedDatagrams(t *testing.T) {
	for _, datagram := range [][]byte{nil, {0, 0}, {1, 0, 0, 1}, {0, 0, 1, 1}, {0, 0, 0, 9}, {0, 0, 0, 1, 127}, {0, 0, 0, 3, 255}} {
		if _, _, err := DecodeDatagram(datagram); err == nil {
			t.Fatalf("accepted malformed datagram %v", datagram)
		}
	}
}

func FuzzDecodeDatagram(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 1, 2, 3, 4, 0, 53, 0x42})
	f.Add([]byte{0, 0, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		DecodeDatagram(data)
	})
}
