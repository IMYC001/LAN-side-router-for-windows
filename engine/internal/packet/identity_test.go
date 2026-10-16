package packet

import (
	"encoding/binary"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func identityQuery(t *testing.T, name string) []byte {
	t.Helper()
	message := dnsmessage.Message{
		Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}},
	}
	dns, err := message.Pack()
	if err != nil {
		t.Fatal(err)
	}
	ip := make([]byte, 28+len(dns))
	ip[0], ip[9] = 0x45, 17
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(ip)))
	binary.BigEndian.PutUint16(ip[22:24], 53)
	binary.BigEndian.PutUint16(ip[24:26], uint16(len(ip)-20))
	copy(ip[28:], dns)
	return ip
}

func TestIdentityHintsNeverClaimExactModel(t *testing.T) {
	for name, want := range map[string]string{
		"auth.api.playstation.net.": "疑似 PlayStation",
		"AUTH.XBOXLIVE.COM.":        "疑似 Xbox 设备",
		"conntest.nintendo.net.":    "疑似 Nintendo 设备",
		"evilplaystation.net.":      "已连接设备",
		"playstation.net.evil.com.": "已连接设备",
	} {
		var hint IdentityHint
		hint.Observe(identityQuery(t, name))
		kind, _ := hint.Label()
		if kind != want {
			t.Fatalf("%s: %s", name, kind)
		}
	}
	var hint IdentityHint
	hint.Observe(identityQuery(t, "api.playstation.net."))
	hint.Observe(identityQuery(t, "api.xboxlive.com."))
	if kind, _ := hint.Label(); kind != "已连接设备" {
		t.Fatal("mixed platform traffic cannot identify hardware")
	}
}

func TestIdentityIgnoresMalformedAndNonDNS(t *testing.T) {
	query := identityQuery(t, "api.playstation.net.")
	for n := 0; n < len(query); n++ {
		var hint IdentityHint
		hint.Observe(query[:n])
		if kind, _ := hint.Label(); kind != "已连接设备" {
			t.Fatal("truncated packet accepted")
		}
	}
	for _, change := range []func([]byte){
		func(p []byte) { p[6] = 0x20 }, // fragmented
		func(p []byte) { p[9] = 6 },
		func(p []byte) { p[23] = 54 },
		func(p []byte) { p[30] = 0x80 }, // DNS response
		func(p []byte) { p[0] = 0x4f },
		func(p []byte) { p[40], p[41] = 0xc0, 12 }, // pointer loop
	} {
		ip := append([]byte(nil), query...)
		change(ip)
		var hint IdentityHint
		hint.Observe(ip)
		if kind, _ := hint.Label(); kind != "已连接设备" {
			t.Fatal("invalid input became an identity hint")
		}
	}
}
