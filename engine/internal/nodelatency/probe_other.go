//go:build !windows

package nodelatency

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"
)

func boundDialer(Request, string) *net.Dialer { return &net.Dialer{} }
func probe(context.Context, Request, netip.Addr, int, bool) (time.Duration, error) {
	return 0, errors.New("仅支持 Windows")
}
