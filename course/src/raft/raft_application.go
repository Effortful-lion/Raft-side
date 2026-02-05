package raft

// 作用：
// 把 Raft 集群中已提交的日志条目、或需要同步的快照数据，安全地通过applyCh通道推送给上层应用层
// 比如 k/v 服务根据日志执行 Put/Delete，根据快照恢复全量数据
// 优先处理快照而非日志
func (rf *Raft) applicationTicker() {
	for !rf.killed() {
		rf.mu.Lock()
		rf.applyCond.Wait()
		// 日志应用
		entries := make([]LogEntry, 0)
		// Follower 是否追加了快照数据(相当于已经提交了日志) —— true表示当前有快照需要推送给应用层
		// 快照数据 <= 已提交的数据
		snapPendingApply := rf.snapPending

		// 1. 无快照，收集待推送的已提交日志
		if !snapPendingApply {
			if rf.lastApplied < rf.log.snapLastIdx {
				rf.lastApplied = rf.log.snapLastIdx
			}

			// make sure that the rf.log have all the entries
			start := rf.lastApplied + 1
			end := rf.commitIndex
			if end >= rf.log.size() {
				end = rf.log.size() - 1
			}
			for i := start; i <= end; i++ {
				entries = append(entries, rf.log.at(i))
			}
		}
		rf.mu.Unlock()

		// 2. 无快照，将 已提交的新日志 全都通过 applyCh 传送到应用层
		// 3. 有快照，把快照数据传送到应用层
		if !snapPendingApply {
			for i, entry := range entries {
				rf.applyCh <- ApplyMsg{
					CommandValid: entry.CommandValid,
					Command:      entry.Command,
					CommandIndex: rf.lastApplied + 1 + i, // must be cautious
				}
			}
		} else {
			rf.applyCh <- ApplyMsg{
				SnapshotValid: true,
				Snapshot:      rf.log.snapshot,
				SnapshotIndex: rf.log.snapLastIdx,
				SnapshotTerm:  rf.log.snapLastTerm,
			}
		}

		rf.mu.Lock()
		// 1. 无快照，更新 lastApplied = lastApplied + len(entries)
		// 2. 有快照，更新 lastApplied = snapLastIdx(因为是全量快照)，更新提交
		// 注意：非 Leader 节点（Follower）同步快照后，本地 commitIndex 滞后于 snapLastIdx。因此需要修正为：commitIndex = lastApplied
		if !snapPendingApply {
			LOG(rf.me, rf.currentTerm, DApply, "Apply log for [%d, %d]", rf.lastApplied+1, rf.lastApplied+len(entries))
			rf.lastApplied += len(entries)
		} else {
			LOG(rf.me, rf.currentTerm, DApply, "Apply snapshot for [0, %d]", 0, rf.log.snapLastIdx)
			rf.lastApplied = rf.log.snapLastIdx
			if rf.commitIndex < rf.lastApplied {
				rf.commitIndex = rf.lastApplied
			}
			// 重置快照待应用标记，后续可处理普通日志(快照优先处理)
			rf.snapPending = false
		}
		rf.mu.Unlock()
	}
}
