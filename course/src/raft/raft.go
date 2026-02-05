package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"

	"sync"
	"sync/atomic"
	"time"

	//	"course/labgob"
	"course/labrpc"
)

const (
	// 选举超时的边界
	electionTimeoutMin time.Duration = 250 * time.Millisecond
	electionTimeoutMax time.Duration = 400 * time.Millisecond

	// 日志复制的间隔时间
	replicateInterval time.Duration = 70 * time.Millisecond
)

const (
	// 空日志优化的边界 term ，真正的 term 从 1 开始
	InvalidTerm int = 0
	// 空日志优化的边界 index ，真正的 index 从 1 开始
	InvalidIndex int = 0
)

// Raft 角色
type Role string

const (
	Follower  Role = "Follower"
	Candidate Role = "Candidate"
	Leader    Role = "Leader"
)

// 当每个 Raft 节点意识到后续的日志条目已被提交时，该节点应通过传递给 Make () 函数的 applyCh，
// 向同一服务器上的服务（或测试器）发送一个 ApplyMsg。将 CommandValid 设置为 true，以表明该 ApplyMsg 包含一个新提交的日志条目。
// CommandValid = true：本次消息是Raft 已提交的日志条目，包含可执行的状态机命令（比如客户端的 Put/GET 操作），上层需要执行该命令并更新状态机；
// CommandValid = false：本次消息不是可执行命令（PartD 中固定为快照数据），上层需要根据快照恢复状态机，而非执行命令。
//
// 在 PartD 中，这个还会传送 snapshot，但是 CommandValid = false
type ApplyMsg struct {
	// 通用
	CommandValid bool        // 标识命令/日志消息(eg: kv 命令的日志消息)
	Command      interface{} // 命令内容
	CommandIndex int         // 日志/命令索引: CommandIndex = 日志条目.LogIndex，二者是同一个值的复用，只是命名上贴合上层 “命令处理” 的语义。
	// 做状态机执行的幂等校验

	// For PartD:
	SnapshotValid bool   // 标识 快照消息
	Snapshot      []byte // 快照数据
	SnapshotTerm  int    // 快照最后一条日志对应的任期
	SnapshotIndex int    // 快照最后一条日志对应的索引
}

// Raft 节点结构
type Raft struct {
	// 通用结构
	mu        sync.Mutex          // 用于保护 raft 节点的共享属性
	peers     []*labrpc.ClientEnd // 所有 raft 节点的 RPC 客户端
	persister *Persister          // 持久化类 —— 用于持久化 raft 节点的持久化状态
	me        int                 // 这是 raft 节点在 peers 数组中的索引 -> 全局唯一，因此也是 raft 节点的 id
	dead      int32               // set by Kill() —— 标识 raft 是否存活

	// 持久化状态
	currentTerm int      // 当前任期(当请求中的任期数 > 当前任期数，需要立刻更新并持久化)
	votedFor    int      // 每一个任期都只有一个投票，表示将票投给哪个 ID 的节点
	log         *RaftLog // raft 本地存储日志

	// 通用易失性状态
	role        Role // 角色（raft论文中没有，实现时使用逻辑清晰）—— 通常由 nextIndex 和 matchIndex 共同推出是 leader
	commitIndex int  // 节点日志提交进度
	lastApplied int  // 节点日志应用进度

	// leader 节点的易失性状态 —— 意味着对其他节点来说没用
	nextIndex  []int // 在日志匹配时进行日志探测 ，用于 leader 在日志复制前的一致性检查(表示下一个需要发送给跟随者的日志条目的索引地址。)
	matchIndex []int // 匹配点/共识点，表示 leader的匹配/共识进度，用于日志提交()

	// 工程需要的其他字段
	applyCh         chan ApplyMsg // 服务器 -> 服务器状态机发送应用消息的 channel
	snapPending     bool          // 当 Follower 收到 snapshot 时，就设置该标记 true
	applyCond       *sync.Cond    // 应用信号量 -> 通过信号量唤醒 apply 的工作流(就是将应用的消息传到 channel 进而传到状态机)
	electionStart   time.Time     // 记录选举开始的时间点(用于计算是否超时)
	electionTimeout time.Duration // 记录选举超时的时间(随机可控)
}

// 变 Follower
// Candidate 和 Leader 都是看到高任期请求才退回的，因此需要 term 参数
// 1. Leader and Candidate have lower term
// 2. Candidate receive the heartbeat from Leader(此时 Leader 的 Term >= Candidate)
func (rf *Raft) becomeFollowerLocked(term int) {
	LOG(rf.me, rf.currentTerm, DLog, "%s->Follower, For T%v->T%v", rf.role, rf.currentTerm, term)
	rf.role = Follower

	// 1 2
	if term > rf.currentTerm {
		rf.votedFor = -1
		rf.currentTerm = term
		rf.persist()
	}
}

// 变 Candidate
// Follower 选举超时
func (rf *Raft) becomeCandidateLocked() {
	// 对于Leader来说不需要变
	if rf.role != Follower && rf.role != Candidate {
		LOG(rf.me, rf.currentTerm, DError, "Only Follower and Candidate can become Candidate")
		return
	}

	// Follower 选举超时：任期提高，票置空，角色变 持久化存储
	LOG(rf.me, rf.currentTerm, DVote, "%s->Candidate, For T%d", rf.role, rf.currentTerm+1)
	rf.currentTerm++
	rf.role = Candidate
	rf.votedFor = rf.me
	rf.persist()
}

// 变 Leader
// Candidate 选举成功
func (rf *Raft) becomeLeaderLocked() {
	// 只有候选者可以成为 Leader
	if rf.role != Candidate {
		LOG(rf.me, rf.currentTerm, DError, "Only Candidate can become Leader")
		return
	}
	// 变角色、转变后立刻初始化 leader 的 易失性 状态
	LOG(rf.me, rf.currentTerm, DLeader, "Become Leader in T%d", rf.currentTerm)
	rf.role = Leader
	for peer := 0; peer < len(rf.peers); peer++ {
		rf.nextIndex[peer] = rf.log.size()
		rf.matchIndex[peer] = 0
	}
	// 新任leader启动 日志复制LOOP
	go rf.replicationTicker(rf.currentTerm)
}

// 返回当前节点的任期和 isLeader?
func (rf *Raft) GetState() (int, bool) {
	// Your code here (PartA).
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.currentTerm, rf.role == Leader
}

// 获得 raft 的状态占用大小
func (rf *Raft) GetRaftStateSize() int {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	return rf.persister.RaftStateSize()
}

// 上层基于 Raft 的业务服务（例如键值对服务器）想要发起共识，
// 确认待追加到 Raft 日志中的下一条命令。如果当前节点并非领导者，直接返回 false；
// 若是领导者，则启动共识流程并立即返回（非阻塞）。
// 该命令不保证一定会被提交到 Raft 日志中 —— 因为领导者可能故障，或在选举中落败。
// 即使当前 Raft 实例已被终止，此函数也应优雅返回，不抛出异常。
//
// 第一个返回值：若该命令最终被提交，它将出现在 Raft 日志中的索引位置；
// 第二个返回值：当前 Raft 节点的任期号；
// 第三个返回值：布尔值，表示当前节点是否认为自己是领导者。
//
// 简单说：
// Start是上层服务向 Raft 集群提交命令的唯一入口，仅由 Leader 处理，完成「本地日志追加 + 持久化」后立即返回
// 剩下的共识工作（附加日志）就自动交给 Raft 底层的 Loop 去处理了

func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.role != Leader {
		return 0, 0, false
	}
	rf.log.append(LogEntry{
		CommandValid: true,
		Command:      command,
		Term:         rf.currentTerm,
	})
	LOG(rf.me, rf.currentTerm, DLeader, "Leader accept log [%d]T%d", rf.log.size()-1, rf.currentTerm)
	rf.persist()

	return rf.log.size() - 1, rf.currentTerm, true
}

// Go 中无法安全地强制终止一个协程（强行终止会导致锁未释放、资源未回收、数据竞争等问题）
// 测试器为了保证测试环境的安全性，选择「温和通知」的方式：
// 通过Kill()打标记，让协程自己检测标记并主动退出，这是 Go 并发编程中终止协程的标准优雅做法。
// 代码主动调用，控制结束
func (rf *Raft) Kill() {
	INFO("calling the raft kill and make a flag")
	atomic.StoreInt32(&rf.dead, 1)
	// Your code here, if desired.
}

// 查看是否存活
func (rf *Raft) killed() bool {
	// INFO("checking whether the raft is killed")
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

// 查看当前上下文是否和之前一致(角色、任期不变)——一般在临界区的执行代码前进行检查
func (rf *Raft) contextLostLocked(role Role, term int) bool {
	return !(rf.currentTerm == term && rf.role == role)
}

// Make 方法：创建一个 Raft 节点实例，由上层业务服务(比如 kv服务器)或测试器调用
// 1. 入参说明
// 1.1 peers []：集群中所有 Raft 节点的通信端口集合，所有节点的 peers [] 数组顺序完全一致
// 1.2 me：当前 Raft 节点在 peers [] 中的索引，当前节点的端口为 peers [me]
// 1.3 persister：持久化存储实例，用于节点保存持久化状态；若节点此前崩溃过，该实例初始时会持有最新的持久化状态（如有）
// 1.4 applyCh：消息通知通道，Raft 节点需通过该通道向测试器 / 上层服务发送 ApplyMsg 消息（已提交日志的应用通知）
// 2. 核心要求
// 2.1 执行效率：Make () 必须快速返回，禁止阻塞
// 2.2 协程管理：所有需要长期运行的逻辑，都应在 Make () 中启动独立协程处理
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (PartA, PartB, PartC).
	// 所有重启/新建的节点都是 Follower
	rf.role = Follower
	rf.currentTerm = 1
	rf.votedFor = -1

	// 一个占位条目，用于避免大量的边界检查
	rf.log = NewRaftLog(InvalidIndex, InvalidTerm, nil, nil)

	// initialize the leader's view slice
	rf.nextIndex = make([]int, len(rf.peers))
	rf.matchIndex = make([]int, len(rf.peers))

	// initialize the fields used for apply
	rf.applyCh = applyCh
	rf.applyCond = sync.NewCond(&rf.mu)
	rf.commitIndex = 0
	rf.lastApplied = 0
	rf.snapPending = false

	// initialize from state persisted before a crash
	// 读取持久化存储
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	// 异步启动: 选举LOOP + 执行LOOP
	go rf.electionTicker()
	go rf.applicationTicker()

	return rf
}
