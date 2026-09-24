package outbound

// 并行拨号转发器：代理商标配多线轮询解析，同一域名可能返回多个出口 IP，
// 其中部分 IP 可能不可达。标准 net.Dialer 对多个 IPv4 地址按顺序串行尝试，
// 先撞上死 IP 就会白等超时（表现为整体 context deadline exceeded）。
// ParallelDialer 解析主机后并发拨号，任一连接成功立即返回并取消其余尝试。

import (
	"context"
	"errors"
	"net"
	"time"
)

// ParallelDialer 实现 proxy.Dialer 与 proxy.ContextDialer，可直接作为
// proxy.SOCKS5 的 forward 拨号器或 http.Transport.DialContext 使用。
type ParallelDialer struct {
	// Base 底层拨号器；为空时使用与 proxy.Direct 等价的默认值。
	Base *net.Dialer
}

func (d ParallelDialer) base() *net.Dialer {
	if d.Base != nil {
		return d.Base
	}
	return &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
}

// Dial 实现 proxy.Dialer。
func (d ParallelDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

// DialContext 解析 addr 主机名，多 IP 时并发拨号，单 IP / IP 字面量走标准拨号。
func (d ParallelDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := d.base()
	if network != "tcp" {
		return dialer.DialContext(ctx, network, addr)
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host == "" || net.ParseIP(host) != nil {
		return dialer.DialContext(ctx, network, addr)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) <= 1 {
		// 解析失败交给标准拨号器（保留其错误语义与 Happy Eyeballs 行为）。
		return dialer.DialContext(ctx, network, addr)
	}
	addrs := make([]string, 0, len(ips))
	for _, ip := range ips {
		addrs = append(addrs, net.JoinHostPort(ip.IP.String(), port))
	}
	return raceDial(ctx, dialer, network, addrs)
}

// raceDial 并发向全部地址发起连接，返回最先成功的一个；
// 其余尝试在首个成功后被取消，缓冲 channel 保证协程不泄漏。
func raceDial(ctx context.Context, dialer *net.Dialer, network string, addrs []string) (net.Conn, error) {
	if len(addrs) <= 1 {
		if len(addrs) == 0 {
			return nil, errors.New("并行拨号：无可拨地址")
		}
		return dialer.DialContext(ctx, network, addrs[0])
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type attempt struct {
		conn net.Conn
		err  error
	}
	results := make(chan attempt, len(addrs))
	for _, addr := range addrs {
		go func(addr string) {
			conn, err := dialer.DialContext(ctx, network, addr)
			results <- attempt{conn: conn, err: err}
		}(addr)
	}
	var lastErr error
	for range addrs {
		r := <-results
		if r.err == nil {
			return r.conn, nil
		}
		// 首个成功后其余拨号会被取消，优先保留真实失败原因。
		if !errors.Is(r.err, context.Canceled) && lastErr == nil {
			lastErr = r.err
		}
	}
	return nil, lastErr
}
