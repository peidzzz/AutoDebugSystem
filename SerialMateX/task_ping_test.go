package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startPingTask 是本文件所有用例的公共前置：假电源口 + TX 恒成功 + 注入 pingRunHook。
// 注意 writeTaskFixtures 内部已经装了假电源口，不要在外面再调 installFakePwrPort。
func startPingTask(t *testing.T, cfg, cmds string, hook func(ctx context.Context, ip string) (int, error)) *App {
	t.Helper()
	writeTaskFixtures(t, cfg, cmds, "[Dev]\nName=Dev\nPort=COM9\nPWR-ON=0x01\nPWR-OFF=0x00\n")
	taskSendHook = func(string) string { return "Sent" }
	pingRunHook = hook
	t.Cleanup(func() {
		taskSendHook = nil
		pingRunHook = nil
	})

	app := &App{isConnected: true, connType: TypeSerial}
	if res := app.StartTask(TaskRunOptions{DeviceName: "Dev"}); res != "" {
		t.Fatalf("启动失败: %s", res)
	}
	return app
}

func TestTaskPingPassKeepsNormalCount(t *testing.T) {
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) { return 4, nil })

	rt := waitTaskDone(t, app, 8*time.Second)
	// 任务结束后 PingState 会被清空（否则界面描边会一直亮着），所以这里只断言计数与日志。
	if rt.NormalCount != 1 {
		t.Fatalf("PING 通过应计正常: %+v", rt)
	}
	if log := readTaskLog(t); !strings.Contains(log, "[PING ") || !strings.Contains(log, "→ 通过") {
		t.Fatalf("日志缺 PING 行:\n%s", log)
	}
}

func TestTaskPingFailPauses(t *testing.T) {
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=1\nWaitSeconds=1\nDelaySeconds=0\nRunCount=3\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) { return 1, nil })

	rt := waitTaskPaused(t, app, 8*time.Second)
	if rt.PausedReason != "ping" {
		t.Fatalf("PausedReason = %q, want ping", rt.PausedReason)
	}
	if !strings.Contains(rt.Message, "PING 192.168.1.10 未通过（回包 1/4）") {
		t.Fatalf("暂停文案不对: %q", rt.Message)
	}
	if res := app.EndTask(); res != "" {
		t.Fatalf("EndTask: %s", res)
	}
	waitTaskDone(t, app, 5*time.Second)
}

func TestTaskPingToolMissingDoesNotCrash(t *testing.T) {
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) { return 0, exec.ErrNotFound })

	rt := waitTaskDone(t, app, 8*time.Second)
	if rt.NormalCount != 0 {
		t.Fatalf("PING 工具不可用应按未通过: %+v", rt)
	}
	log := readTaskLog(t)
	if !strings.Contains(log, "无法执行 ping") {
		t.Fatalf("日志应写明 ping 无法执行（而不是伪装成丢包 100%%）:\n%s", log)
	}
}

func TestTaskPingRunsWithoutLogOrPause(t *testing.T) {
	// 用户明确要求：PING 是否执行只取决于 PingEnable，与 SaveLog / AbnormalPause 无关。
	// （没开日志、没开异常暂停时，界面上的正常次数就是 PING 的通过次数。）
	pings := 0
	app := startPingTask(t,
		"[Task]\nSaveLog=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=2\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) {
			pings++
			if pings == 1 {
				return 4, nil
			}
			return 0, nil
		})

	rt := waitTaskDone(t, app, 10*time.Second)
	if pings != 2 {
		t.Fatalf("两轮都应执行 PING，实际 %d 次", pings)
	}
	if rt.NormalCount != 1 {
		t.Fatalf("PING 失败的那一轮不应计正常: %+v", rt)
	}
}

func TestTaskPingOnlyTaskWithoutCommands(t *testing.T) {
	// 命令列表为空 + PING 开启：应当能启动，整轮只有 PING 一步（正常次数 = PING 通过次数）
	writeTaskFixtures(t,
		"[Task]\nSaveLog=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=2\nPingEnable=1\nPingIP=192.168.1.10\n",
		"",
		"[Dev]\nName=Dev\nPort=COM9\nPWR-ON=0x01\nPWR-OFF=0x00\n")
	pingRunHook = func(ctx context.Context, ip string) (int, error) { return 4, nil }
	t.Cleanup(func() { pingRunHook = nil })

	app := &App{isConnected: true, connType: TypeSerial}
	if res := app.StartTask(TaskRunOptions{DeviceName: "Dev"}); res != "" {
		t.Fatalf("PING 开启时应允许空命令列表: %s", res)
	}
	rt := waitTaskDone(t, app, 10*time.Second)
	if rt.NormalCount != 2 || rt.RunCount != 2 {
		t.Fatalf("空命令列表应只靠 PING 计正常: %+v", rt)
	}
}

func TestTaskStopDuringPingCountsAbnormalRound(t *testing.T) {
	// 复审发现的严重问题：PING 期间点停止，原来既不给 PING 记异常、还把这轮算成「正常」，
	// 结果日志写「结束原因：任务已停止」，总结却是「正常 1 / 总数 1」「PING 0/0/0」。
	entered := make(chan struct{})
	app := startPingTask(t,
		"[Task]\nSaveLog=1\nAbnormalPause=1\nWaitSeconds=1\nDelaySeconds=0\nRunCount=5\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return 0, ctx.Err()
		})

	<-entered
	if res := app.StopTask(); res != "" {
		t.Fatalf("StopTask: %s", res)
	}
	rt := waitTaskDone(t, app, 3*time.Second)
	if rt.NormalCount != 0 {
		t.Fatalf("被停止打断的轮次不能算正常: %+v", rt)
	}
	if rt.PingState != "" {
		t.Fatalf("任务结束后 PingState 必须清空，否则界面描边会一直亮着: %q", rt.PingState)
	}
	log := readTaskLog(t)
	for _, want := range []string{
		"* 总测试轮数：正常 0 / 异常 1 / 总数 1",
		"* PING 检测： 正常 0 / 异常 1 / 总数 1",
		"被停止打断",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("日志缺少 %q，实际:\n%s", want, log)
		}
	}
}

func TestTaskPingFailStateClearedAtTaskEnd(t *testing.T) {
	// 最后一轮 PING 失败时，红框只该保持到任务结束，不能固化在界面上。
	app := startPingTask(t,
		"[Task]\nSaveLog=0\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) { return 1, nil })

	rt := waitTaskDone(t, app, 8*time.Second)
	if rt.PingState != "" {
		t.Fatalf("任务结束后 PingState 必须清空，实际 %q", rt.PingState)
	}
}

func TestPingFailDetailMentionsToolError(t *testing.T) {
	// ping.exe 缺失/被杀/超时不能都伪装成「丢包 100%」，否则用户查不出是工具问题。
	got := pingFailDetailErr("192.168.1.10", 0, exec.ErrNotFound)
	if !strings.Contains(got, "无法执行 ping") || !strings.Contains(got, exec.ErrNotFound.Error()) {
		t.Fatalf("明细没带上工具错误: %q", got)
	}
}

func TestPingProbePropagatesContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pingProbe(ctx, "192.168.1.10"); err == nil {
		t.Fatal("ctx 已取消时 pingProbe 必须返回错误，而不是 (0,nil)")
	}
}

func TestTaskStopDuringPingEndsQuickly(t *testing.T) {
	entered := make(chan struct{})
	app := startPingTask(t,
		"[Task]\nSaveLog=0\nAbnormalPause=1\nWaitSeconds=1\nDelaySeconds=0\nRunCount=5\nPingEnable=1\nPingIP=192.168.1.10\n",
		"TX AT 0\n",
		func(ctx context.Context, ip string) (int, error) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-ctx.Done() // 只有 ctx 被取消才返回，模拟一个挂住的 ping
			return 0, ctx.Err()
		})

	<-entered
	if res := app.StopTask(); res != "" {
		t.Fatalf("StopTask: %s", res)
	}
	waitTaskDone(t, app, 2*time.Second) // 必须远小于 pingTimeout(8s)
}
