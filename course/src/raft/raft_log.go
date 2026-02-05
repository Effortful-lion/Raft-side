package raft

import (
	"course/labgob"
	"fmt"
)

// 快照优化的 Raft 日志分层存储结构: 实现快照层+日志增量层/实时日志层
type RaftLog struct {
	// 快照相关：标记快照的最后一条日志的索引和任期
	// 巧妙理解：
	// 1. 将快照的最后一个日志条目和空日志条目重合
	// 2. 有快照时：全局索引 = snapLastIdx + index(tailLog[index]中的)
	// 3. 无快照时：全局索引 = 0 + index(有效索引还是从 1 开始)
	// 或者说，快照一直存在，只不过有空快照也占位
	snapLastIdx  int // 快照覆盖的最后一条日志的索引（快照的“结束位置”）
	snapLastTerm int // 快照覆盖的最后一条日志的任期

	// 快照二进制数据：对应全局日志索引 [1, snapLastIdx] 的所有历史日志的压缩结果
	// 包含该区间内所有日志执行后的状态机最终状态，恢复快照即可直接得到该状态
	// 全量快照
	snapshot []byte

	// 尾部日志：快照之后的最新日志，是未被压缩的“活日志”
	// 核心索引规则：
	// 1. 实际有效日志：对应索引 (snapLastIdx, snapLastIdx+len(tailLog)-1]
	// 2. 伪日志条目：tailLog[0] 是 哨兵日志，无实际命令
	tailLog []LogEntry // 元素是标准的LogEntry（含LogIndex、Term、Command等）
}

// 创建分层日志类
func NewRaftLog(snapLastIdx, snapLastTerm int, snapshot []byte, entries []LogEntry) *RaftLog {
	rl := &RaftLog{
		snapLastIdx:  snapLastIdx,
		snapLastTerm: snapLastTerm,
		snapshot:     snapshot,
	}

	// 添加 空日志条目,使得 tailLog 和 logEntry 的实现相同（有效日志都从 1 开始）
	rl.tailLog = append(rl.tailLog, LogEntry{
		Term: snapLastTerm,
	})
	// 添加 logEntries
	rl.tailLog = append(rl.tailLog, entries...)

	return rl
}

// 以下所有函数都应在rf.mutex的保护下调用

// 读取 RaftLog 数据
func (rl *RaftLog) readPersist(d *labgob.LabDecoder) error {
	var lastIdx int
	if err := d.Decode(&lastIdx); err != nil {
		return fmt.Errorf("decode last include index failed")
	}
	rl.snapLastIdx = lastIdx

	var lastTerm int
	if err := d.Decode(&lastTerm); err != nil {
		return fmt.Errorf("decode last include term failed")
	}
	rl.snapLastTerm = lastTerm

	var log []LogEntry
	if err := d.Decode(&log); err != nil {
		return fmt.Errorf("decode tail log failed")
	}
	rl.tailLog = log

	return nil
}

// 持久化 RaftLog 数据（快照数据在快照对象中）
func (rl *RaftLog) persist(e *labgob.LabEncoder) {
	e.Encode(rl.snapLastIdx)
	e.Encode(rl.snapLastTerm)
	e.Encode(rl.tailLog)
}

// access methods

// 返回当前节点的 log 长度（快照+实时log）
func (rl *RaftLog) size() int {
	return rl.snapLastIdx + len(rl.tailLog)
}

// 入参：全局索引
// 出参：返回相对索引（实时层的索引）
func (rl *RaftLog) idx(logicIdx int) int {
	// if the logicIdx fall beyond [snapLastIdx, size()-1]
	// 越界检测
	if logicIdx < rl.snapLastIdx || logicIdx >= rl.size() {
		panic(fmt.Sprintf("%d is out of [%d, %d]", logicIdx, rl.snapLastIdx, rl.size()-1))
	}
	// 返回逻辑索引
	return logicIdx - rl.snapLastIdx
}

// 入参：全局索引
// 返回实时层的日志
func (rl *RaftLog) at(logicIdx int) LogEntry {
	return rl.tailLog[rl.idx(logicIdx)]
}

// 返回当前节点最后一个日志的全局索引和对应的任期
func (rl *RaftLog) last() (index, term int) {
	i := len(rl.tailLog) - 1
	return rl.snapLastIdx + i, rl.tailLog[i].Term
}

// 如果实时层中有 term，那么返回该 term 中的第一个日志的全局索引
func (rl *RaftLog) firstFor(term int) int {
	for idx, entry := range rl.tailLog {
		if entry.Term == term {
			return idx + rl.snapLastIdx
		} else if entry.Term > term {
			break
		}
	}
	return InvalidIndex
}

// 返回从全局索引 startIdx 开始的所有日志
func (rl *RaftLog) tail(startIdx int) []LogEntry {
	if startIdx >= rl.size() {
		return nil
	}

	return rl.tailLog[rl.idx(startIdx):]
}

// mutate methods

// 将 LogEntry 追加到当前节点的 log
func (rl *RaftLog) append(e LogEntry) {
	rl.tailLog = append(rl.tailLog, e)
}

// 入参：全局索引，待追加的日志条目
// 追加到本地 log
func (rl *RaftLog) appendFrom(logicPrevIndex int, entries []LogEntry) {
	rl.tailLog = append(rl.tailLog[:rl.idx(logicPrevIndex)+1], entries...)
}

// string methods for debug
func (rl *RaftLog) String() string {
	var terms string
	prevTerm := rl.snapLastTerm
	prevStart := rl.snapLastIdx
	for i := 0; i < len(rl.tailLog); i++ {
		if rl.tailLog[i].Term != prevTerm {
			terms += fmt.Sprintf(" [%d, %d]T%d", prevStart, rl.snapLastIdx+i-1, prevTerm)
			prevTerm = rl.tailLog[i].Term
			prevStart = i
		}
	}
	terms += fmt.Sprintf("[%d, %d]T%d", prevStart, rl.snapLastIdx+len(rl.tailLog)-1, prevTerm)
	return terms
}

//	index 也在快照里 (<= index)
//
// 将应用层的快照数据应用到 raft 节点中
// 本地做快照 + 保留增量
func (rl *RaftLog) doSnapshot(index int, snapshot []byte) {
	// 如果 index(将要生成的快照的边界) <= 当前节点.snapLastIdx，已经存在了没必要
	// TODO 前面调用前应该判断过了，我先删掉
	//if index <= rl.snapLastIdx {
	//	return
	//}

	// 获得相对索引
	idx := rl.idx(index)

	// 应用快照数据
	rl.snapLastIdx = index
	rl.snapLastTerm = rl.tailLog[idx].Term
	rl.snapshot = snapshot

	// make a new log array
	NewLogEntry := make([]LogEntry, 0, rl.size()-rl.snapLastIdx)
	NewLogEntry = append(NewLogEntry, LogEntry{
		Term: rl.snapLastTerm,
	})
	// 将 剩余数据 = log - 快照数据 放到新logEntries中，存储到新的实时层中
	NewLogEntry = append(NewLogEntry, rl.tailLog[idx+1:]...)
	rl.tailLog = NewLogEntry
}

// 从 raft 层安装快照
// 安装快照 + 清空增量
func (rl *RaftLog) installSnapshot(index, term int, snapshot []byte) {
	// 将数据应用到 raft 节点
	rl.snapLastIdx = index
	rl.snapLastTerm = term
	rl.snapshot = snapshot

	// 创建一个新的 实时层log
	NewLogEntry := make([]LogEntry, 0, 1)
	NewLogEntry = append(NewLogEntry, LogEntry{
		Term: rl.snapLastTerm,
	})
	rl.tailLog = NewLogEntry
}
