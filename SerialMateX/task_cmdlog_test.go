package main

import (
	"strings"
	"testing"
	"time"
)

func TestTaskLogCmdLineOnIncompleteRound(t *testing.T) {
	// 一条永不匹配的 RX：这一轮命令没跑完，必须留下 [CMD] 行，写清第几轮、哪条命令。
	// 本用例没有 TX 命令，所以不需要 taskSendHook。
	writeTaskFixtures(t,
		"[Task]\nSaveLog=1\nAbnormalPause=0\nWaitSeconds=1\nDelaySeconds=0\nRunCount=1\n",
		"RX NEVER_MATCH 0\n",
		"[Dev]\nName=Dev\nPort=COM9\nPWR-ON=0x01\nPWR-OFF=0x00\n")

	app := &App{isConnected: true, connType: TypeSerial}
	if res := app.StartTask(TaskRunOptions{DeviceName: "Dev"}); res != "" {
		t.Fatalf("启动失败: %s", res)
	}
	waitTaskDone(t, app, 8*time.Second)

	log := readTaskLog(t)
	if !strings.Contains(log, "[CMD ") {
		t.Fatalf("日志里没有 [CMD] 行:\n%s", log)
	}
	if !strings.Contains(log, "第 1 次测试未完成") || !strings.Contains(log, `命令 1（RX "NEVER_MATCH"）`) {
		t.Fatalf("[CMD] 行缺少轮次或命令描述:\n%s", log)
	}
}