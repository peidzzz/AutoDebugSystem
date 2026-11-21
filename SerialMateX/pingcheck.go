package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// pingPacketCount 是每轮检测发送的包数。
	pingPacketCount = 4
	// pingTimeout 是单次检测的整体上限，超时即放弃本次检测。
	pingTimeout = 8 * time.Second
	// pingPassLossRate 是判定通过的丢包率上限（百分比）：丢包 ≤ 50% 即至少回 2 包算通过。
	pingPassLossRate = 50
)

// pingRunHook 仅供测试替换真实的 ping.exe 调用。
var pingRunHook func(ctx context.Context, ip string) (int, error)

// validIPv4 判断 s 是否是一个可用的 IPv4 地址（允许首尾空格，不接受 0.0.0.0 与 IPv6）。
func validIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return false
	}
	to4 := ip.To4()
	if to4 == nil {
		return false
	}
	return !to4.Equal(net.IPv4zero)
}

// pingLossRate 把回包数换算成丢包百分比。
func pingLossRate(recv int) int {
	if recv < 0 {
		recv = 0
	}
	if recv > pingPacketCount {
		recv = pingPacketCount
	}
	return (pingPacketCount - recv) * 100 / pingPacketCount
}

// pingPassed 表示本次检测是否通过（丢包 ≤ 50%）。
func pingPassed(recv int) bool {
	return pingLossRate(recv) <= pingPassLossRate
}

// pingCountTTL 数 ping 输出里带 TTL= 的行，即成功回包数（大小写不敏感，中英文输出通用）。
func pingCountTTL(out string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(strings.ToUpper(line), "TTL=") {
			n++
		}
	}
	return n
}

func pingVerdict(passed bool) string {
	if passed {
		return "通过"
	}
	return "不通过"
}

// pingLogLine 是写进任务日志的单行结果，以换行结尾。
func pingLogLine(at, ip string, recv int, passed bool) string {
	return fmt.Sprintf("[PING %s] %s 回包 %d/%d（丢包 %d%%）→ %s\n",
		at, ip, recv, pingPacketCount, pingLossRate(recv), pingVerdict(passed))
}

// pingFailDetail 是写进任务总结失败明细的描述，不含时间戳与换行。
func pingFailDetail(ip string, recv int) string {
	return fmt.Sprintf("PING %s 回包 %d/%d，丢包 %d%% → %s",
		ip, recv, pingPacketCount, pingLossRate(recv), pingVerdict(false))
}

// pingFailDetailErr 用于 ping 根本没能执行的情况（找不到 ping.exe、被安全软件杀掉、超时）。
// 这种失败和「丢包 100%」是两回事，不提工具错误用户查不出来。
func pingFailDetailErr(ip string, recv int, err error) string {
	if err == nil {
		return pingFailDetail(ip, recv)
	}
	return fmt.Sprintf("PING %s 无法执行 ping：%v", ip, err)
}

// pingCancelDetail 是 PING 被用户点停止打断时的失败明细。
func pingCancelDetail(ip string) string {
	return "PING " + ip + " 被停止打断"
}

// pingCancelLine 是 PING 被打断时写进日志的单行。
func pingCancelLine(at, ip string) string {
	return fmt.Sprintf("[PING %s] %s 检测被停止打断\n", at, ip)
}

// pingLogLineErr 是 ping 根本没能执行时写进日志的单行。
func pingLogLineErr(at, ip string, err error) string {
	return fmt.Sprintf("[PING %s] %s 无法执行 ping：%v\n", at, ip, err)
}

// pingProbe 调用系统 ping.exe 发 4 个包，返回成功回包数（0..4）。
// ping.exe 在有丢包时会返回退出码 1，这里按正常结果处理，不当作错误；
// 但 ctx 被取消（用户点停止 / 超时）必须如实返回错误，不能伪装成丢包 100%。
func pingProbe(ctx context.Context, ip string) (int, error) {
	if pingRunHook != nil {
		return pingRunHook(ctx, ip)
	}
	cmd := exec.CommandContext(ctx, "ping", "-4", "-n", strconv.Itoa(pingPacketCount), "-w", "1000", ip)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		if _, ok := err.(*exec.ExitError); !ok {
			return 0, err
		}
	}
	return pingCountTTL(out.String()), nil
}
