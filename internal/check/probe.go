package check

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ResultOK      = "ok"
	ResultFail    = "fail"
	ResultSkipped = "skipped"
)

// Endpoint describes a local proxy listener used by a bounded probe.
type Endpoint struct {
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     uint16 `json:"port"`
}

func (e Endpoint) AddressPort() string {
	return net.JoinHostPort(e.Address, strconv.Itoa(int(e.Port)))
}

// ProbeResult is a stable machine-readable result for one bounded check step.
type ProbeResult struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
}

func OK(id, name string, latency time.Duration, detail string) ProbeResult {
	return ProbeResult{ID: id, Name: name, Status: ResultOK, LatencyMS: latencyMillis(latency), Detail: detail}
}

func Fail(id, name string, latency time.Duration, err error) ProbeResult {
	message := "failed"
	if err != nil {
		message = err.Error()
	}
	return ProbeResult{ID: id, Name: name, Status: ResultFail, LatencyMS: latencyMillis(latency), Error: message}
}

func Skipped(id, name, detail string) ProbeResult {
	return ProbeResult{ID: id, Name: name, Status: ResultSkipped, Detail: detail}
}

// MeasureTCP measures a bounded direct TCP handshake to the profile server.
// It intentionally does not copy the endpoint or net.OpError text into Detail or
// Error because profile endpoints are sensitive provider material in CLI output.
func MeasureTCP(ctx context.Context, address string, timeout time.Duration) ProbeResult {
	name := "Server TCP handshake"
	if strings.TrimSpace(address) == "" {
		return Skipped("server_tcp", name, "profile does not expose a single server endpoint")
	}
	timeout = normalizedTimeout(timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", address)
	latency := time.Since(start)
	if err != nil {
		return Fail("server_tcp", name, latency, errors.New("tcp connect to profile server failed"))
	}
	_ = conn.Close()
	return OK("server_tcp", name, latency, "tcp connect to profile server")
}

// ProbeHTTPSOverHTTPProxy performs one low-impact HTTPS probe through an HTTP proxy listener.
func ProbeHTTPSOverHTTPProxy(ctx context.Context, endpoint Endpoint, target Target, timeout time.Duration) ProbeResult {
	proxyURL := url.URL{Scheme: "http", Host: endpoint.AddressPort()}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(&proxyURL),
		DialContext:           (&net.Dialer{Timeout: normalizedTimeout(timeout)}).DialContext,
		TLSHandshakeTimeout:   normalizedTimeout(timeout),
		ResponseHeaderTimeout: normalizedTimeout(timeout),
	}
	defer transport.CloseIdleConnections()
	return probeHTTPS(ctx, &http.Client{Transport: transport, Timeout: normalizedTimeout(timeout)}, target, timeout)
}

// ProbeHTTPSOverSOCKS performs one low-impact HTTPS probe through a SOCKS5 listener.
func ProbeHTTPSOverSOCKS(ctx context.Context, endpoint Endpoint, target Target, timeout time.Duration) ProbeResult {
	timeout = normalizedTimeout(timeout)
	proxyAddress := endpoint.AddressPort()
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" && network != "tcp4" && network != "tcp6" {
				return nil, fmt.Errorf("unsupported SOCKS network %q", network)
			}
			return dialSOCKS5(ctx, proxyAddress, address, timeout)
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
	defer transport.CloseIdleConnections()
	return probeHTTPS(ctx, &http.Client{Transport: transport, Timeout: timeout}, target, timeout)
}

func probeHTTPS(ctx context.Context, client *http.Client, target Target, timeout time.Duration) ProbeResult {
	if target.ProbeType != ProbeTypeHTTPS {
		return Fail(target.ID, target.DisplayName, 0, fmt.Errorf("unsupported probe type %q", target.ProbeType))
	}
	if strings.TrimSpace(target.URL) == "" {
		return Fail(target.ID, target.DisplayName, 0, errors.New("target URL is empty"))
	}
	timeout = normalizedTimeout(timeout)
	if target.Timeout > 0 && timeout == DefaultTimeout {
		timeout = target.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return Fail(target.ID, target.DisplayName, 0, err)
	}
	req.Header.Set("User-Agent", "podlaz-check/0")
	req.Header.Set("Range", "bytes=0-1023")

	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return Fail(target.ID, target.DisplayName, latency, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 500 {
		return Fail(target.ID, target.DisplayName, latency, fmt.Errorf("HTTP %d does not satisfy %s", resp.StatusCode, target.SuccessCondition))
	}
	return OK(target.ID, target.DisplayName, latency, fmt.Sprintf("HTTP %d", resp.StatusCode))
}

func dialSOCKS5(ctx context.Context, proxyAddress, targetAddress string, timeout time.Duration) (net.Conn, error) {
	timeout = normalizedTimeout(timeout)
	conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", proxyAddress)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return nil, fmt.Errorf("SOCKS greeting: %w", err)
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return nil, fmt.Errorf("SOCKS greeting response: %w", err)
	}
	if greeting[0] != 0x05 || greeting[1] != 0x00 {
		return nil, fmt.Errorf("SOCKS proxy did not accept no-auth method")
	}

	request, err := socksConnectRequest(targetAddress)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(request); err != nil {
		return nil, fmt.Errorf("SOCKS connect request: %w", err)
	}
	if err := readSOCKSConnectResponse(conn); err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	ok = true
	return conn, nil
}

func socksConnectRequest(address string) ([]byte, error) {
	host, portValue, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split SOCKS target address %q: %w", address, err)
	}
	port, err := strconv.ParseUint(portValue, 10, 16)
	if err != nil || port == 0 {
		return nil, fmt.Errorf("invalid SOCKS target port %q", portValue)
	}

	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 0x01)
			request = append(request, ip4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return nil, fmt.Errorf("SOCKS target hostname is too long")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, []byte(host)...)
	}
	request = append(request, byte(port>>8), byte(port))
	return request, nil
}

func readSOCKSConnectResponse(conn net.Conn) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("SOCKS connect response: %w", err)
	}
	if header[0] != 0x05 {
		return fmt.Errorf("SOCKS connect response has unsupported version %d", header[0])
	}
	if header[1] != 0x00 {
		return fmt.Errorf("SOCKS connect failed with reply code %d", header[1])
	}

	toRead := 0
	switch header[3] {
	case 0x01:
		toRead = net.IPv4len + 2
	case 0x04:
		toRead = net.IPv6len + 2
	case 0x03:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return fmt.Errorf("SOCKS connect domain length: %w", err)
		}
		toRead = int(length[0]) + 2
	default:
		return fmt.Errorf("SOCKS connect response has unsupported address type %d", header[3])
	}
	if toRead > 0 {
		buf := make([]byte, toRead)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return fmt.Errorf("SOCKS connect bound address: %w", err)
		}
	}
	return nil
}

func normalizedTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return DefaultTimeout
	}
	return timeout
}

func latencyMillis(latency time.Duration) int64 {
	if latency <= 0 {
		return 0
	}
	ms := latency.Milliseconds()
	if ms == 0 {
		return 1
	}
	return ms
}
