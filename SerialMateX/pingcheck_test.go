package main

import (
	"context"
	"strings"
	"testing"
)

func TestValidIPv4(t *testing.T) {
	cases := map[string]bool{
		"192.168.1.10": true, " 10.0.0.5 ": true, "255.255.255.255": true, "8.8.8.8": true,
		"": false, "0.0.0.0": false, "192.168.1": false, "192.168.1.256": false,
		"1.2.3.4.5": false, "192.168.01.1": false, "abc": false, "::1": false,
	}
	for in, want := range cases {
		if got := validIPv4(in); got != want {
			t.Errorf("validIPv4(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPingLossRateAndPassed(t *testing.T) {
	// recv → 丢包百分比 / 是否通过（通过 = 丢包 ≤ 50%，即至少回 2 包）
	cases := []struct {
		recv, loss int
		pass       bool
	}{
		{4, 0, true}, {3, 25, true}, {2, 50, true}, {1, 75, false}, {0, 100, false},
	}
	for _, c := range cases {
		if got := pingLossRate(c.recv); got != c.loss {
			t.Errorf("pingLossRate(%d) = %d, want %d", c.recv, got, c.loss)
		}
		if got := pingPassed(c.recv); got != c.pass {
			t.Errorf("pingPassed(%d) = %v, want %v", c.recv, got, c.pass)
		}
	}
}

func TestPingCountTTL(t *testing.T) {
	en := "Reply from 192.168.1.1: bytes=32 time<1ms TTL=64\r\n" +
		"Reply from 192.168.1.1: bytes=32 time<1ms TTL=64\r\n" +
		"Request timed out.\r\n"
	zh := "来自 192.168.1.1 的回复: 字节=32 时间<1ms TTL=64\r\n" +
		"来自 192.168.1.1 的回复: 字节=32 时间=2ms ttl=64\r\n"
	cases := map[string]struct {
		out  string
		want int
	}{
		"en":   {en, 2},
		"zh":   {zh, 2},
		"全部丢失": {"无法访问目标主机。\r\n", 0},
		"空输出":  {"", 0},
	}
	for name, c := range cases {
		if got := pingCountTTL(c.out); got != c.want {
			t.Errorf("pingCountTTL(%s) = %d, want %d", name, got, c.want)
		}
	}
}

func TestPingLogLineAndFailDetail(t *testing.T) {
	line := pingLogLine("21:10:32", "192.168.1.10", 1, false)
	want := "[PING 21:10:32] 192.168.1.10 回包 1/4（丢包 75%）→ 不通过\n"
	if line != want {
		t.Fatalf("pingLogLine = %q, want %q", line, want)
	}
	ok := pingLogLine("21:10:32", "192.168.1.10", 4, true)
	if !strings.HasSuffix(ok, "回包 4/4（丢包 0%）→ 通过\n") {
		t.Fatalf("通过时日志行不对: %q", ok)
	}
	if detail := pingFailDetail("192.168.1.10", 1); detail != "PING 192.168.1.10 回包 1/4，丢包 75% → 不通过" {
		t.Fatalf("pingFailDetail = %q", detail)
	}
}

func TestPingProbeUsesHook(t *testing.T) {
	pingRunHook = func(ctx context.Context, ip string) (int, error) {
		if ip != "10.0.0.1" {
			t.Errorf("hook 收到的 ip = %q", ip)
		}
		return 3, nil
	}
	t.Cleanup(func() { pingRunHook = nil })

	recv, err := pingProbe(context.Background(), "10.0.0.1")
	if err != nil || recv != 3 {
		t.Fatalf("pingProbe = (%d, %v), want (3, nil)", recv, err)
	}
}
