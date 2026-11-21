package main

import (
	"strconv"
	"strings"
	"time"
)

// maxRecordedFailures 是总结里最多逐条列出的失败轮次数；超出部分只报个数。
const maxRecordedFailures = 200

// roundFailure 是某一轮的失败记录，写进任务总结的「失败轮次」小节。
type roundFailure struct {
	Round  int    // 第几轮（从 1 开始）
	Kind   string // cmd / ping / power
	Detail string // 具体原因（PING 失败必须带丢包率）
}

// taskStats 是一次任务的统计。只有任务 goroutine 读写，所以不需要加锁。
type taskStats struct {
	roundsTotal  int
	roundsNormal int
	cmdsTotal    int // 命令轮数：每轮记一次（空命令列表的任务不记）
	cmdsNormal   int
	pingsTotal   int
	pingsNormal  int
	pingEnabled  bool
	failures     []roundFailure
	// 失败「轮次」数按轮去重：同一轮里命令未完成 + 掉电失败会记两条明细，
	// 但总结里的「失败轮次：N 轮」必须与「异常 N」对得上。
	failureRounds     int
	lastFailureRound  int
	overflow          int // 没能逐条列出、且已被计入 failureRounds 的轮数
	lastOverflowRound int
}

// addFailure 记一轮失败；超过上限只累加 overflow（同样按轮计）。
func (s *taskStats) addFailure(round int, kind, detail string) {
	if round != s.lastFailureRound {
		s.lastFailureRound = round
		s.failureRounds++
	}
	if len(s.failures) >= maxRecordedFailures {
		if round != s.lastOverflowRound {
			s.lastOverflowRound = round
			s.overflow++
		}
		return
	}
	s.failures = append(s.failures, roundFailure{Round: round, Kind: kind, Detail: detail})
}

// formatTaskSummary 生成任务结束时的统计块。首尾各带一个换行，
// 因为 appendTaskLog 追加的是原文（日志里紧接在最后一条 RX 数据后面）。
func formatTaskSummary(s *taskStats, endReason string, endedAt time.Time) string {
	var b strings.Builder

	b.WriteString("\n=============== 任务总结 ===============\n")
	b.WriteString("* 总测试轮数：正常 " + strconv.Itoa(s.roundsNormal) +
		" / 异常 " + strconv.Itoa(s.roundsTotal-s.roundsNormal) +
		" / 总数 " + strconv.Itoa(s.roundsTotal) + "\n")
	if s.cmdsTotal == 0 {
		b.WriteString("* 命令执行：  未配置命令\n")
	} else {
		b.WriteString("* 命令执行：  正常 " + strconv.Itoa(s.cmdsNormal) +
			" / 异常 " + strconv.Itoa(s.cmdsTotal-s.cmdsNormal) +
			" / 总数 " + strconv.Itoa(s.cmdsTotal) + "\n")
	}
	if !s.pingEnabled {
		b.WriteString("* PING 检测： 未开启\n")
	} else {
		b.WriteString("* PING 检测： 正常 " + strconv.Itoa(s.pingsNormal) +
			" / 异常 " + strconv.Itoa(s.pingsTotal-s.pingsNormal) +
			" / 总数 " + strconv.Itoa(s.pingsTotal) + "\n")
	}

	if s.failureRounds > 0 {
		b.WriteString("----------------------------------------\n")
		b.WriteString("* 失败轮次：" + strconv.Itoa(s.failureRounds) + " 轮\n")
		for _, f := range s.failures {
			b.WriteString("  - 第 " + strconv.Itoa(f.Round) + " 轮：" + f.Detail + "\n")
		}
		if s.overflow > 0 {
			b.WriteString("  …（另有 " + strconv.Itoa(s.overflow) + " 轮失败未逐条列出）\n")
		}
	}

	b.WriteString("* 结束原因：" + endReason + "\n")
	b.WriteString("* 结束时间：" + endedAt.Format("2006-01-02 15:04:05") + "\n")
	b.WriteString("========================================\n")
	return b.String()
}
