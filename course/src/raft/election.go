package raft

import (
	"math/rand"
	"time"
)

// 角色定义
type Role string

const (
	Leader    Role = "Leader"
	Follower  Role = "Follower"
	Candidate Role = "Candidate"
)

// 返回当前任期和该服务器是否认为自己是领导者。
func (rf *Raft) GetState() (int, bool) {
	var term int
	var isleader bool
	// 你的代码在这里（PartA）。
	rf.mu.Lock()
	term = rf.currentTerm
	isleader = rf.role == Leader
	rf.mu.Unlock()
	return term, isleader
}

// 角色转换(所有转换操作原子性，外部有锁才能用，内部不用锁) —— 单纯的角色转换
func (r *Raft) becomeLeaderLocked() {
	if r.role != Candidate {
		LOG(r.me, r.currentTerm, DLeader,
			"%s, Only candidate can become Leader", r.role)
		return
	}
	// candidate -> leader
	LOG(r.me, r.currentTerm, DLeader, "%s -> Leader, For T%d",
		r.role, r.currentTerm)
	r.role = Leader
}

func (r *Raft) becomeFollowerLocked(term int) {
	// term 日志消息的任期
	// term 任期小不用变
	if term < r.currentTerm {
		LOG(r.me, r.currentTerm, DError, "Can't become Follower, lower term")
		return
	}

	LOG(r.me, r.currentTerm, DLog, "%s -> Follower, For T%d->T%d",
		r.role, r.currentTerm, term)

	// term 任期大，那么不论 leader candidate 都要变
	if term > r.currentTerm {
		// 任期变大，重置投票（一个任期开始，投票重置）
		r.currentTerm = term
		r.role = Follower
		r.votedFor = -1
	}

	// candidate 发现存在领导人则 变: (触发原因不写)
	// 这里其实是如果有leader当选，那么leader会发送心跳，当candidate发现来自leader的心跳携带的任期比自己大，还是会降低
}

func (r *Raft) becomeCandidateLocked() {
	// leader 不能
	if r.role == Leader {
		LOG(r.me, r.currentTerm, DError, "Leader can't become Candidate")
		return
	}

	// candidate 和 follower 都要变
	// follower: 超时选举
	// candidate: 选举失败，重置随机超时时间再超时选举
	// 所以其实二者是一样的动作
	LOG(r.me, r.currentTerm, DVote, "%s -> Candidate, For T%d->T%d",
		r.role, r.currentTerm, r.currentTerm+1)
	r.role = Candidate
	r.currentTerm++
	// 重置超时选举（candidate） —— 主要是针对选举失败后重置随机时间
	r.resetElectionTimerLocked()
	r.votedFor = r.me
}

// 超时时间可控范围
const (
	electionTimeoutMin time.Duration = 250 * time.Millisecond
	electionTimeoutMax time.Duration = 400 * time.Millisecond
)

// 设置/重置超时时间 + 记录开始计算时间
func (rf *Raft) resetElectionTimerLocked() {
	rf.electionStart = time.Now()
	randRange := int64(electionTimeoutMax - electionTimeoutMin)
	rf.electionTimeout = electionTimeoutMin + time.Duration(rand.Int63()%randRange)
}

// 判断是否超时 / 是否开始选举
func (rf *Raft) isElectionTimeoutLocked() bool {
	return time.Since(rf.electionStart) > rf.electionTimeout
}

// 主 loop 定时检查（定时时间 << 选举超时时间）
func (rf *Raft) electionTicker() {
	// 如果节点存活
	for !rf.killed() {
		// Your code here (PartA)
		// Check if a leader election should be started.
		// 上锁检查状态
		rf.mu.Lock()
		if rf.role != Leader && rf.isElectionTimeoutLocked() {
			rf.becomeCandidateLocked()
			// 异步开始一次选举，选举过程不影响整体定时检查
			go rf.startElection(rf.currentTerm)
		}
		rf.mu.Unlock()

		// 暂停一段随机时长，介于50到350毫秒之间。
		ms := 50 + (rand.Int63() % 300)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// 参与选举
func (rf *Raft) startElection(term int) bool {
	votes := 0
	// 要票逻辑定义
	askVoteFromPeer := func(peer int, args *RequestVoteArgs) {
		// send RPC
		reply := &RequestVoteReply{}
		ok := rf.sendRequestVote(peer, args, reply)

		// handle the response
		rf.mu.Lock()
		defer rf.mu.Unlock()
		if !ok {
			LOG(rf.me, rf.currentTerm, DDebug, "Ask vote from %d, Lost or error", peer)
			return
		}

		// align the term
		if reply.Term > rf.currentTerm {
			rf.becomeFollowerLocked(reply.Term)
			return
		}

		// check the context
		if rf.contextLostLocked(Candidate, term) {
			LOG(rf.me, rf.currentTerm, DVote, "Lost context, abort RequestVoteReply in T%d", rf.currentTerm)
			return
		}

		// count votes
		// 要票逻辑统计
		if reply.VoteGranted {
			votes++
		}
		if votes > len(rf.peers)/2 {
			rf.becomeLeaderLocked()
			// 当选 leader，立刻启动心跳检测机制
			go rf.replicationTicker(term)
		}
	}

	rf.mu.Lock()
	defer rf.mu.Unlock()

	// every time locked
	// 任期发生变化 || 角色发送变化 取消选举
	if rf.contextLostLocked(Candidate, term) {
		return false
	}

	// 投票统计(自己 + 要票逻辑)
	for peer := 0; peer < len(rf.peers); peer++ {
		if peer == rf.me {
			votes++
			continue
		}

		args := &RequestVoteArgs{
			Term:        term,
			CandidateId: rf.me,
		}
		// 要票逻辑
		go askVoteFromPeer(peer, args)
	}

	return true
}

// 示例 RequestVote RPC 参数结构。
// 字段名必须以大写字母开头！
type RequestVoteArgs struct {
	// 你的数据在这里（PartA, PartB）。
	Term        int // 任期
	CandidateId int // 投给谁
}

// 示例 RequestVote RPC 回复结构。
// 字段名必须以大写字母开头！
type RequestVoteReply struct {
	// 你的数据在这里（PartA）。
	Term        int  // 任期
	VoteGranted bool // 投成功了没
}

// RPC 处理函数：投票逻辑
func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	// Your code here (PartA, PartB).
	rf.mu.Lock()
	defer rf.mu.Unlock()

	// align the term
	reply.Term = rf.currentTerm
	// 当前大，拒绝投票
	if rf.currentTerm > args.Term {
		LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Reject vote, higher term, T%d>T%d", args.CandidateId, rf.currentTerm, args.Term)
		reply.VoteGranted = false
		return
	}
	// 当前小，转为追随者 并 投票（投票逻辑在下面）
	if rf.currentTerm < args.Term {
		rf.becomeFollowerLocked(args.Term)
	}

	// 是否有票
	if rf.votedFor != -1 {
		LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Reject, Already voted S%d", args.CandidateId, rf.votedFor)
		reply.VoteGranted = false
		return
	}

	// 投票成功
	reply.VoteGranted = true
	rf.votedFor = args.CandidateId
	// 只有投票成功，才重置超时选举时间
	rf.resetElectionTimerLocked()
	LOG(rf.me, rf.currentTerm, DVote, "-> S%d", args.CandidateId)
}

// 向服务器发送 RequestVote RPC 的示例代码。
// server 是目标服务器在 rf.peers[] 中的索引。
// 期望在 args 中提供 RPC 参数。
// 用 RPC 回复填充 *reply，因此调用者应该传递 &reply。
// 传递给 Call() 的 args 和 reply 的类型必须与
// 处理函数中声明的参数类型相同（包括它们是否为指针）。
//
// labrpc 包模拟一个不可靠的网络，其中服务器可能无法访问，
// 并且请求和回复可能会丢失。
// Call() 发送请求并等待回复。如果在超时时间内收到回复，Call() 返回 true；
// 否则，Call() 返回 false。因此，Call() 可能需要一段时间才会返回。
// 返回 false 可能是由以下原因导致的：服务器死亡、服务器无法访问、
// 请求丢失或回复丢失。
//
// Call() 保证会返回（可能会延迟），除非服务器端的处理函数不返回。
// 因此，不需要在 Call() 周围实现自己的超时机制。
//
// 有关更多详细信息，请查看 ../labrpc/labrpc.go 中的注释。
//
// 如果你在使用 RPC 时遇到问题，请检查你是否已将
// 通过 RPC 传递的结构体中的所有字段名大写，并且
// 调用者是否使用 & 传递回复结构体的地址，而不是结构体本身。
func (rf *Raft) sendRequestVote(server int, args *RequestVoteArgs, reply *RequestVoteReply) bool {
	ok := rf.peers[server].Call("Raft.RequestVote", args, reply)
	return ok
}
