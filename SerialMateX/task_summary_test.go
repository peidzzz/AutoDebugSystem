package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFormatTaskSummary(t *testing.T) {
	s := &taskStats{pingEnabled: true}
	s.roundsTotal, s.roundsNormal = 5, 3
	s.cmdsTotal, s.cmdsNormal = 5, 5
	s.pingsTotal, s.pingsNormal = 5, 3
	s.addFailure(2, "cmd", `命令 3（RX "OK"）在运行时间内没收到匹配数据`)
	s.addFailure(4, "ping", "PING 192.168.1.10 回包 1/4，丢包 75% → 不通过")

	got := formatTaskSummary(s, "任务完成", time.Date(2026, 11, 20, 21, 30, 12, 0, time.Local))
	want := "\n" +
		"=============== 任务总结 ===============\n" +
		"* 总测试轮数：正常 3 / 异常 2 / 总数 5\n" +
		"* 命令执行：  正常 5 / 异常 0 / 总数 5\n" +
		"* PING 检测： 正常 3 / 异常 2 / 总数 5\n" +
		"----------------------------------------\n" +
		"* 失败轮次：2 轮\n" +
		"  - 第 2 轮：命令 3（RX \"OK\"）在运行时间内没收到匹配数据\n" +
		"  - 第 4 轮：PING 192.168.1.10 回包 1/4，丢包 75% → 不通过\n" +
		"* 结束原因：任务完成\n" +
		"* 结束时间：2026-11-20 21:30:12\n" +
		"========================================\n"
	if got != want {
		t.Fatalf("总结不符:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatTaskSummaryAllNormalAndPingOff(t *testing.T) {
	s := &taskStats{}
	s.roundsTotal, s.roundsNormal = 2, 2
	s.cmdsTotal, s.cmdsNormal = 2, 2
	got := formatTaskSummary(s, "任务完成", time.Date(2026, 11, 20, 21, 30, 12, 0, time.Local))
	if strings.Contains(got, "失败轮次") || strings.Contains(got, "-----") {
		t.Fatalf("全正常时不该有失败小节:\n%s", got)
	}
	if !strings.Contains(got, "* PING 检测： 未开启\n") {
		t.Fatalf("PING 未开启时应写「未开启」:\n%s", got)
	}

	// 空命令列表的 PING-only 任务：命令那行写「未配置命令」
	only := &taskStats{pingEnabled: true}
	only.roundsTotal, only.roundsNormal = 3, 3
	only.pingsTotal, only.pingsNormal = 3, 3
	got = formatTaskSummary(only, "任务完成", time.Date(2026, 11, 20, 21, 30, 12, 0, time.Local))
	if !strings.Contains(got, "* 命令执行：  未配置命令\n") {
		t.Fatalf("空命令列表应写「未配置命令」:\n%s", got)
	}
}

func TestTaskSummaryCapsFailureDetail(t *testing.T) {
	s := &taskStats{}
	for i := 1; i <= 205; i++ {
		s.addFailure(i, "cmd", "x")
	}
	if len(s.failures) != maxRecordedFailures || s.overflow != 5 {
		t.Fatalf("上限没生效: len=%d overflow=%d", len(s.failures), s.overflow)
	}
	got := formatTaskSummary(s, "任务完成", time.Now())
	if !strings.Contains(got, "* 失败轮次：205 轮") || !strings.Contains(got, "另有 5 轮失败未逐条列出") {
		t.Fatalf("溢出提示不对:\n%s", got)
	}
}

func TestTaskSummaryAppendedToLog(t *testing.T) {
	// 第一轮 PING 通过、第二轮失败：验证 PING 的正常/异常计数与失败明细。
	// pingRunHook 只在任务 goroutine 里被调用，计数器不需要加锁。
	calls := 0
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=2\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) {
			calls++
			if calls == 1 {
				return 4, nil
			}
			return 1, nil
		})

	waitTaskDone(t, app, 10*time.Second)

	log := readTaskLog(t)
	for _, want := range []string{
		"=============== 任务总结 ===============",
		"* 总测试轮数：正常 1 / 异常 1 / 总数 2",
		"* PING 检测： 正常 1 / 异常 1 / 总数 2",
		"* 失败轮次：1 轮",
		"第 2 轮：PING 192.168.1.10 回包 1/4，丢包 75% → 不通过",
		"* 结束原因：任务完成",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("日志缺少 %q，实际:\n%s", want, log)
		}
	}
}