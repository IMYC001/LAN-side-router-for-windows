package subscriptionfetch

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"accelerator/engine/internal/subscription"
)

func quietTLS(t *testing.T, handler http.Handler, tls13 bool) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	if tls13 {
		server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func trustedTransport(t *testing.T, server *httptest.Server) *http.Transport {
	t.Helper()
	transport := newTransport()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = pool
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

func TestTLS13GzipAndMetadata(t *testing.T) {
	var tlsVersion atomic.Uint32
	server := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tlsVersion.Store(uint32(r.TLS.Version))
		if r.URL.RequestURI() != "/sub?token=TEST-SECRET" ||
			r.Header.Get("User-Agent") != "ClashMeta/Accelerator-Preflight" ||
			r.Header.Get("Accept-Encoding") != "gzip" {
			t.Error("request address or negotiated headers changed")
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Subscription-Userinfo", "upload=1; download=2; total=10")
		w.Header().Set("Content-Disposition", "attachment; filename=Game.yaml")
		compressed := gzip.NewWriter(w)
		_, _ = compressed.Write([]byte("proxies: []"))
		_ = compressed.Close()
	}), true)
	result := fetch(context.Background(), Request{URL: server.URL + "/sub?token=TEST-SECRET"}, trustedTransport(t, server))
	if result.Failure != "" || string(result.Content) != "proxies: []" || result.UserInfo != "upload=1; download=2; total=10" ||
		result.Disposition != "attachment; filename=Game.yaml" || tlsVersion.Load() != tls.VersionTLS13 {
		t.Fatal("TLS 1.3 download or metadata failed", result.Failure)
	}
}

func TestTLS12SupportedAndOldTLSRefused(t *testing.T) {
	server := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxies: []"))
	}), false)
	transport := trustedTransport(t, server)
	transport.TLSClientConfig.MaxVersion = tls.VersionTLS12
	if result := fetch(context.Background(), Request{URL: server.URL}, transport); result.Failure != "" {
		t.Fatal(result.Failure)
	}
	defaults := newTransport()
	defer defaults.CloseIdleConnections()
	if defaults.TLSClientConfig.MinVersion != tls.VersionTLS12 || defaults.TLSClientConfig.InsecureSkipVerify ||
		defaults.TLSClientConfig.RootCAs != nil || defaults.TLSClientConfig.VerifyPeerCertificate != nil ||
		defaults.TLSClientConfig.VerifyConnection != nil || defaults.Proxy != nil {
		t.Fatal("production TLS trust or proxy policy changed")
	}
	old := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("old TLS endpoint must not receive the request")
	}), false)
	old.TLS.MaxVersion = tls.VersionTLS11
	if result := fetch(context.Background(), Request{URL: old.URL}, trustedTransport(t, old)); result.Failure == "" {
		t.Fatal("old TLS accepted")
	}
}

func TestUntrustedCertificateRefused(t *testing.T) {
	var requests atomic.Int32
	server := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
	}), true)
	result := Fetch(context.Background(), Request{URL: server.URL + "/sub?token=TEST-SECRET"})
	if result.Failure != "certificate-trust" || requests.Load() != 0 || len(result.Content) != 0 {
		t.Fatal("untrusted certificate was not rejected", result.Failure)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "TEST-SECRET") || strings.Contains(string(encoded), server.URL) {
		t.Fatal("certificate failure exposed request credentials")
	}
}

func TestHTTPRefusalAndRedirect(t *testing.T) {
	var destinationHits atomic.Int32
	destination := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationHits.Add(1)
	}), false)
	for _, status := range []int{http.StatusForbidden, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("TEST-SECRET"))
			}), false)
			result := fetch(context.Background(), Request{URL: server.URL}, trustedTransport(t, server))
			if result.Failure != "http" || result.Status != status || len(result.Content) != 0 {
				t.Fatal("HTTP refusal not preserved")
			}
		})
	}
	if destinationHits.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestDecodedSizeLimitsAndTruncation(t *testing.T) {
	for _, mode := range []string{"declared", "chunked", "gzip", "truncated", "encoding"} {
		t.Run(mode, func(t *testing.T) {
			server := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "declared":
					w.Header().Set("Content-Length", "8388609")
				case "chunked":
					w.(http.Flusher).Flush()
					_, _ = io.Copy(w, io.LimitReader(&zeroReader{}, subscription.MaxBytes+1))
				case "gzip":
					w.Header().Set("Content-Encoding", "gzip")
					compressed := gzip.NewWriter(w)
					_, _ = io.Copy(compressed, io.LimitReader(&zeroReader{}, subscription.MaxBytes+1))
					_ = compressed.Close()
				case "truncated":
					w.Header().Set("Content-Length", "100")
					_, _ = w.Write([]byte("short"))
				case "encoding":
					w.Header().Set("Content-Encoding", "unsupported")
				}
			}), false)
			result := fetch(context.Background(), Request{URL: server.URL}, trustedTransport(t, server))
			expected := "size"
			if mode == "truncated" {
				expected = "connection-or-handshake"
			} else if mode == "encoding" {
				expected = "encoding"
			}
			if result.Failure != expected || len(result.Content) != 0 {
				t.Fatalf("expected %s, got %s", expected, result.Failure)
			}
		})
	}
}

type zeroReader struct{}

func (*zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestBodyDeadline(t *testing.T) {
	server := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}), false)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result := fetch(ctx, Request{URL: server.URL}, trustedTransport(t, server))
	if result.Failure != "timeout" {
		t.Fatal("body deadline not enforced", result.Failure)
	}
}

func TestExplicitHTTPProxy(t *testing.T) {
	endpoint := quietTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxies: []"))
	}), true)
	target, _ := url.Parse(endpoint.URL)
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != target.Host {
			t.Error("unexpected proxy request")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		upstream, err := net.Dial("tcp", target.Host)
		if err != nil {
			t.Error("local endpoint unavailable")
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		connection, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error("local CONNECT failed")
			return
		}
		defer connection.Close()
		connects.Add(1)
		_, _ = buffered.WriteString("HTTP/1.1 200 Connection established\r\n\r\n")
		_ = buffered.Flush()
		done := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, buffered); _ = upstream.Close(); close(done) }()
		_, _ = io.Copy(connection, upstream)
		_ = connection.Close()
		<-done
	}))
	defer proxy.Close()
	result := fetch(context.Background(), Request{URL: endpoint.URL, Proxy: proxy.URL}, trustedTransport(t, endpoint))
	if result.Failure != "" || connects.Load() != 1 || string(result.Content) != "proxies: []" {
		t.Fatal("explicit proxy route failed", result.Failure)
	}
}

func TestInputAndSafeErrorCategories(t *testing.T) {
	for _, input := range []Request{
		{URL: "http://example.invalid/sub?token=TEST-SECRET"},
		{URL: "https://user:TEST-SECRET@example.invalid"},
		{URL: "https://example.invalid", Proxy: "file:///TEST-SECRET"},
		{URL: "https://example.invalid", Proxy: "http://user:TEST-SECRET@127.0.0.1:7890"},
	} {
		result := Fetch(context.Background(), input)
		if result.Failure != "address" && result.Failure != "proxy" {
			t.Fatal("invalid input accepted")
		}
	}
	for _, fixture := range []struct {
		err  error
		code string
	}{
		{x509.UnknownAuthorityError{}, "certificate-trust"},
		{x509.HostnameError{}, "certificate-name"},
		{x509.CertificateInvalidError{Reason: x509.Expired}, "certificate-time"},
		{x509.CertificateInvalidError{Reason: x509.IncompatibleUsage}, "certificate-invalid"},
		{&net.DNSError{}, "dns"},
		{context.DeadlineExceeded, "timeout"},
		{errors.New("https://example.invalid/sub?token=TEST-SECRET"), "connection-or-handshake"},
	} {
		if classify(fixture.err) != fixture.code {
			t.Fatal("incorrect failure category", fixture.code)
		}
	}
}
