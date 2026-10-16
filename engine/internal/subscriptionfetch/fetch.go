// Package subscriptionfetch supplies a bounded HTTPS client independent of Schannel.
package subscriptionfetch

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"accelerator/engine/internal/subscription"
)

type Request struct {
	URL   string `json:"url"`
	Proxy string `json:"proxy"`
}

type Result struct {
	Content     []byte `json:"content,omitempty"`
	UserInfo    string `json:"userInfo,omitempty"`
	Disposition string `json:"disposition,omitempty"`
	Failure     string `json:"failure,omitempty"`
	Status      int    `json:"status,omitempty"`
}

func newTransport() *http.Transport {
	return &http.Transport{
		DialContext:            (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DisableKeepAlives:      true,
	}
}

func Fetch(ctx context.Context, input Request) Result {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	transport := newTransport()
	defer transport.CloseIdleConnections()
	return fetch(ctx, input, transport)
}

func fetch(ctx context.Context, input Request, transport *http.Transport) Result {
	address, err := url.Parse(input.URL)
	if err != nil || address.Scheme != "https" || address.Hostname() == "" || address.User != nil ||
		len(input.URL) > 65536 {
		return Result{Failure: "address"}
	}
	// A blank proxy explicitly means direct; never inherit environment proxy settings.
	if input.Proxy != "" {
		p, err := url.Parse(input.Proxy)
		if err != nil || p.Hostname() == "" || p.User != nil || p.RawQuery != "" || p.Fragment != "" ||
			(p.Path != "" && p.Path != "/") ||
			(p.Scheme != "http" && p.Scheme != "https" && p.Scheme != "socks5") {
			return Result{Failure: "proxy"}
		}
		transport.Proxy = http.ProxyURL(p)
	} else {
		transport.Proxy = nil
	}
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, input.URL, nil)
	if err != nil {
		return Result{Failure: "address"}
	}
	request.Header.Set("User-Agent", "ClashMeta/Accelerator-Preflight")
	response, err := client.Do(request)
	if err != nil {
		return Result{Failure: classify(err)}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Result{Failure: "http", Status: response.StatusCode}
	}
	// The transport requests and decompresses gzip itself; cap the decoded stream.
	if response.Header.Get("Content-Encoding") != "" {
		return Result{Failure: "encoding"}
	}
	if response.ContentLength > subscription.MaxBytes {
		return Result{Failure: "size"}
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, subscription.MaxBytes+1))
	if err != nil {
		return Result{Failure: classify(err)}
	}
	if len(content) > subscription.MaxBytes {
		return Result{Failure: "size"}
	}
	return Result{
		Content: content, UserInfo: response.Header.Get("Subscription-Userinfo"),
		Disposition: response.Header.Get("Content-Disposition"),
	}
}

func classify(err error) string {
	// Only return fixed categories: error strings may contain subscription credentials.
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verification *tls.CertificateVerificationError
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.As(err, &authority):
		return "certificate-trust"
	case errors.As(err, &hostname):
		return "certificate-name"
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return "certificate-time"
		}
		return "certificate-invalid"
	case errors.As(err, &verification):
		return "certificate-invalid"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "timeout"
	case errors.As(err, &dns):
		return "dns"
	case errors.As(err, &network) && network.Timeout():
		return "timeout"
	default:
		return "connection-or-handshake"
	}
}
