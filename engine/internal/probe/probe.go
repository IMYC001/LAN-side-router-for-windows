package probe

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"time"

	"accelerator/engine/internal/socks"
)

type Result struct {
	TCP     bool   `json:"tcp"`
	UDP     bool   `json:"udp"`
	TCPMs   int64  `json:"tcpMs"`
	UDPMs   int64  `json:"udpMs"`
	TCPInfo string `json:"tcpInfo"`
	UDPInfo string `json:"udpInfo"`
	Scope   string `json:"scope"`
}

func Run(ctx context.Context, address string) Result {
	result := Result{Scope: "只验证所填 SOCKS5 入口到 example.com 的 TLS 和到 1.1.1.1:53 的 DNS；不代表 PS5 游戏、丢包率或 NAT 类型已通过。"}
	client := socks.Client{Address: address}
	if err := client.Validate(); err != nil {
		result.TCPInfo, result.UDPInfo = err.Error(), err.Error()
		return result
	}
	start := time.Now()
	tcpCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	conn, err := client.DialTCP(tcpCtx, "example.com:443")
	if err == nil {
		secure := tls.Client(conn, &tls.Config{ServerName: "example.com", MinVersion: tls.VersionTLS12})
		err = secure.HandshakeContext(tcpCtx)
		secure.Close()
	}
	cancel()
	result.TCPMs = time.Since(start).Milliseconds()
	result.TCP = err == nil
	if err != nil {
		result.TCPInfo = err.Error()
	} else {
		result.TCPInfo = "SOCKS5 CONNECT 与 TLS 握手成功"
	}
	start = time.Now()
	udpCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	association, err := client.AssociateUDP(udpCtx)
	if err == nil {
		defer association.Close()
		association.SetDeadline(time.Now().Add(5 * time.Second))
		var transaction [2]byte
		if _, err = rand.Read(transaction[:]); err == nil {
			query := append(transaction[:], 1, 0, 0, 1, 0, 0, 0, 0, 0, 0)
			query = append(query, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1)
			if err = association.WriteTo(query, "1.1.1.1:53"); err == nil {
				buffer := make([]byte, 65535)
				var reply []byte
				var from string
				reply, from, err = association.ReadFrom(buffer)
				if err == nil && (from != "1.1.1.1:53" || len(reply) < 12 || binary.BigEndian.Uint16(reply[:2]) != binary.BigEndian.Uint16(transaction[:]) || reply[2]&0x80 == 0 || reply[3]&15 != 0 || binary.BigEndian.Uint16(reply[6:8]) == 0) {
					err = fmt.Errorf("收到的 UDP DNS 回复未通过来源、事务和响应校验")
				}
			}
		}
	}
	result.UDPMs = time.Since(start).Milliseconds()
	result.UDP = err == nil
	if err != nil {
		result.UDPInfo = err.Error()
	} else {
		result.UDPInfo = "SOCKS5 UDP ASSOCIATE 与 DNS 往返成功"
	}
	return result
}
