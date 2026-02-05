目标
在 A 的领导人选举和日志复制（只有心跳检测）的基础上，实现：
1. 领导人选举的补充（任期相同，比较日志长度）
2. 心跳部分增加日志逻辑
3. 领导人维护集群全局所有节点的nextIndex和matchIndex，用于计算出commitIndex
4. 领导人提交更新了 commitIndex 之后，发送消息给所有节点，节点 apply 到自己的状态机
5. 异常处理
其中 nextIndex 用于进行日志同步时的匹配点试探，matchIndex 用于日志同步成功后的匹配点记录
那么，加入了日志操作，其实加入了：日志匹配机制、一致性检查机制、leader节点的集群管理、leader选举限制、安全性约束
日志匹配机制：
一致性检查机制：
安全性约束：
- 领导人不能提交旧任期的日志条目
- 只有当前任期的日志条目被提交后，之前的日志条目才能被间接提交
设计和实现
定义日志结构体
// 日志结构
type LogEntry struct {
    Term         int
    CommandValid bool
    Command      interface{}
}
补充结构体
Raft：
// 日志条目集合：每个日志条目包含了状态机要执行的命令以及领导人接收到该条目时的任期（这是解释日志结构：term、command + 新提交的日志条目标识（理清具体作用））
log []LogEntry
matchIndex []int    // 已经匹配的日志索引（当前已经有的日志条目索引集合）
nextIndex  []int    // 下一个要匹配的索引（初始值为领导人下一个日志条目索引，一致性检查时向前对齐）(为什么是一个数组？不是一个数？)
附加日志RPC请求结构体：
// 用于匹配日志前缀，由于一致性检查需要，满足：日志匹配特性
PrevLogIndex int
PrevLogTerm  int
Entries      []LogEntry // 携带日志
LeaderCommit int        // 领导人已知的已提交的最新的日志条目
Make补全
rf.log = append(rf.log, LogEntry{})

rf.matchIndex = make([]int, len(rf.peers))
rf.nextIndex = make([]int, len(rf.peers))
附加日志RPC请求补全
1. raft接收到新请求后，根据日志匹配原则，对当前raft的前一个日志的任期和索引值和请求的日志消息做比较。确保二者相等
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

    // 对齐任期后，访问状态
    // 结果返回日志不匹配，则向 nextIndex 向前一个继续试探 - 准备覆盖
    if !reply.Success {
       // 拿到前一个 nextIndex
       idx := rf.nextIndex[peer] - 1
       // 拿到对应的任期
       term := rf.log[idx].Term
       // 从 idx 开始，只要任期相同回退，直到任期不同
       // 这是一个优化，减少 RPC 次数：
       // 每个任期的最小索引处进行一致性校验，替代从第一个nextIndex开始每个索引处进行一致性校验
       for idx > 0 && rf.log[idx].Term == term {
          idx--
       }
       rf.nextIndex[peer] = idx + 1
       // 任期不同后，idx+1 其实对应的就是和这个raft节点对齐的最新的日志
       LOG(rf.me, rf.currentTerm, DLog, "Log not matched in %d, Update next=%d", args.PrevLogIndex, rf.nextIndex[peer])
       return
    }

    // update the match/next index if log appended successfully
    rf.matchIndex[peer] = args.PrevLogIndex + len(args.Entries)
    rf.nextIndex[peer] = rf.matchIndex[peer] + 1

    // TODO: need compute the new commitIndex here,
    // but we leave it to the other chapter
}


for peer := 0; peer < len(rf.peers); peer++ {
    if peer == rf.me {
       // Don't forget to update Leader's matchIndex
       rf.matchIndex[peer] = len(rf.log) - 1
       rf.nextIndex[peer] = len(rf.log)
       continue
    }

    // 发送RPC日志附加请求构造参数：领导人的最后一个日志位置、任期
    prevIdx := rf.nextIndex[peer] - 1
    prevTerm := rf.log[prevIdx].Term
    args := &AppendEntriesArgs{
       Term:         rf.currentTerm,
       LeaderId:     rf.me,
       PrevLogIndex: prevIdx,
       PrevLogTerm:  prevTerm,
       Entries:      rf.log[prevIdx+1:],
       LeaderCommit: rf.commitIndex,
    }
    LOG(rf.me, rf.currentTerm, DDebug, "-> S%d, Send log, Prev=[%d]T%d, Len()=%d", peer, args.PrevLogIndex, args.PrevLogTerm, len(args.Entries))
    go replicateToPeer(peer, args)
}
附加日志RPC响应补全
// return failure if the previous log not matched
if args.PrevLogIndex >= len(rf.log) {
    LOG(rf.me, rf.currentTerm, DLog2, "<- S%d, Reject Log, Follower log too short, Len:%d <= Prev:%d", args.LeaderId, len(rf.log), args.PrevLogIndex)
    return
}
if rf.log[args.PrevLogIndex].Term != args.PrevLogTerm {
    LOG(rf.me, rf.currentTerm, DLog2, "<- S%d, Reject Log, Prev log not match, [%d]: T%d != T%d", args.LeaderId, args.PrevLogIndex, rf.log[args.PrevLogIndex].Term, args.PrevLogTerm)
    return
}

// 匹配成功: 紧接着 preLogIndex向后追加
rf.log = append(rf.log[:args.PrevLogIndex+1], args.Entries...)
LOG(rf.me, rf.currentTerm, DLog2, "Follower append logs: (%d, %d]", args.PrevLogIndex, args.PrevLogIndex+len(args.Entries))
reply.Success = true

// TODO: handle the args.LeaderCommit
补全 leader 初始化
1. 维护一个nextIndex
2. 维护一个matchIndex
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
    // 初始化
    for peer := 0; peer < len(r.peers); peer++ {
       r.nextIndex[peer] = len(r.log) // 下一个
       r.matchIndex[peer] = 0
    }
}
选举补全
1. 任期相同，大的有效
// 当前一样大，选举限制
// 如果当前的日志更新，那么拒绝更新
if rf.isMoreUpToDateLocked(args.LastLogIndex, args.LastLogTerm) {
    LOG(rf.me, rf.currentTerm, DVote, "-> S%d, Reject Vote, S%d's log less up-to-date", args.CandidateId)
    return
}

// 判断日志长度(当前 相比于 请求的消息来说，谁更新？)
func (rf *Raft) isMoreUpToDateLocked(candidateIndex, candidateTerm int) bool {
    //l := len(rf.log)
    //lastTerm, lastIndex := rf.log[l-1].Term, l-1
    //LOG(rf.me, rf.currentTerm, DVote, "Compare last log, Me: [%d]T%d, Candidate: [%d]T%d", lastIndex, lastTerm, candidateIndex, candidateTerm)
    //
    //// 如果任期不等，返回是否大
    //if lastTerm != candidateTerm {
    // return lastTerm > candidateTerm
    //}
    //// 当前的 preindex > 接收到的 index ?
    //return lastIndex > candidateIndex
    moreUp := true
    l := len(rf.log)
    preIndex := l - 1
    preTerm := rf.log[preIndex].Term
    // 任期大，当前更新，不需要更新
    if preTerm > candidateTerm {
       return moreUp
    }
    // 索引大，当前更新，不需要更新
    if preIndex > candidateIndex {
       return moreUp
    }
    moreUp = false
    return moreUp
}
日志应用
补充raft结构体
commitIndex int           // 当前节点的日志提交进度
lastApplied int           // 上次应用的日志索引: 记录当前节点的日志应用进度
applyCond   *sync.Cond    // 信号量控制 应用时机 / 不应用的时候等待
applyCh     chan ApplyMsg // 传输 applyMsg
注意：
1. 这里的 CommandIndex 的含义和计算方式不懂
/*
Apply 工作流在实现的时候，最重要的就是在给 applyCh 发送 ApplyMsg 时，不要在加锁的情况下进行。因为我们并不知道这个操作会耗时多久（即应用层多久会取走数据），因此不能让其在 apply 的时候持有锁。

于是，我们把 apply 分为三个阶段：

阶段一：构造所有待 apply 的 ApplyMsg
阶段二：遍历这些 msgs，进行 apply
阶段三：更新 lastApplied
*/

func (rf *Raft) applyTicker() {
    for !rf.killed() {
       rf.mu.Lock()
       rf.applyCond.Wait()

       entries := make([]LogEntry, 0)
       // should start from rf.lastApplied+1 instead of rf.lastApplied
       // 从上一次应用进度到提交进度
       for i := rf.lastApplied + 1; i <= rf.commitIndex; i++ {
          entries = append(entries, rf.log[i])
       }
       rf.mu.Unlock()

       // 将需要追加的日志
       for i, entry := range entries {
          rf.applyCh <- ApplyMsg{
             CommandValid: entry.CommandValid,
             Command:      entry.Command,
             CommandIndex: rf.lastApplied + 1 + i,
          }
       }

       rf.mu.Lock()
       LOG(rf.me, rf.currentTerm, DApply, "Apply log for [%d, %d]", rf.lastApplied+1, rf.lastApplied+len(entries))
       rf.lastApplied += len(entries)
       rf.mu.Unlock()
    }
}
只要我们保证全局就只有这一个 apply 的地方，那我们这样分成三个部分问题就不大。尤其是需要注意，当后面增加 snapshot apply 的逻辑时，也要放到该函数里。
注意：
同步 commitIndex ，通过集群多数节点的提交情况，算出集群的commitIndex：(比 leader 大，那么需要附加新的日志了，比leader小 可能嘛？)
majorityMatched := rf.getMajorityIndexLocked()
if majorityMatched > rf.commitIndex {
    LOG(rf.me, rf.currentTerm, DApply, "Leader update the commit index %d->%d", rf.commitIndex, majorityMatched)
    rf.commitIndex = majorityMatched
    // 有新的日志提交，提交
    rf.applyCond.Signal()
}
测试过程


测试结果