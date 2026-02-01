package raft

import "time"

// 附加日志 + 心跳检测

// 心跳间隔 << 超时选举时间（250 - 400） => 50
var replicateInterval = 50 * time.Millisecond

// 心跳请求参数
type AppendEntriesArgs struct {
	Term     int
	LeaderId int
}

// 心跳响应参数
type AppendEntriesReply struct {
	Term    int
	Success bool
}

// 心跳机制
func (rf *Raft) replicationTicker(term int) {
	for !rf.killed() {
		ok := rf.startReplication(term)
		if !ok {
			return
		}

		time.Sleep(replicateInterval)
	}
}

// 返回是否成功发起一轮心跳
// 开始一轮心跳
func (rf *Raft) startReplication(term int) bool {
	replicateToPeer := func(peer int, args *AppendEntriesArgs) {
		reply := &AppendEntriesReply{}
		ok := rf.sendAppendEntries(peer, args, reply)

		rf.mu.Lock()
		defer rf.mu.Unlock()
		// 收获到结果后，参与处理
		if !ok {
			LOG(rf.me, rf.currentTerm, DLog, "-> S%d, Lost or crashed", peer)
			return
		}
		// align the term
		if reply.Term > rf.currentTerm {
			rf.becomeFollowerLocked(reply.Term)
			return
		}
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.contextLostLocked(Leader, term) {
		LOG(rf.me, rf.currentTerm, DLeader, "Leader[T%d] -> %s[T%d]", term, rf.role, rf.currentTerm)
		return false
	}

	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			continue
		}

		args := &AppendEntriesArgs{
			Term:     term,
			LeaderId: rf.me,
		}

		go replicateToPeer(peer, args)
	}

	return true
}

func (rf *Raft) sendAppendEntries(peer int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	// 没有写操作，不会有竞争 不需要锁
	client := rf.peers[peer]
	if client == nil {
		return false
	}

	ok := client.Call("Raft.AppendEntries", args, reply)

	if !ok {
		rf.mu.Lock()
		LOG(rf.me, rf.currentTerm, DLog, "S%d sendAppendEntries, S%d AppendEntries failed", rf.me, peer)
		rf.mu.Unlock()
	}
	return ok
}

//func (rf *Raft) sendAppendEntries(server int, args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
//	ok := rf.peers[server].Call("Raft.AppendEntries", args, reply)
//	return ok
//}

// 心跳处理
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	reply.Term = rf.currentTerm
	reply.Success = false
	// align the term
	if args.Term < rf.currentTerm {
		LOG(rf.me, rf.currentTerm, DLog2, "<- S%d, Reject log", args.LeaderId)
		return
	}
	if args.Term >= rf.currentTerm {
		rf.becomeFollowerLocked(args.Term)
	}

	// reset the timer
	rf.resetElectionTimerLocked()
	reply.Success = true
}
