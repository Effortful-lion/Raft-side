package raft

//
// 这是 Raft 必须向服务（或测试器）公开的 API 概述。
// 有关每个函数的更多详细信息，请参阅下面的注释。
//
// rf = Make(...)
//   创建一个新的 Raft 服务器。
// rf.Start(command interface{}) (index, term, isleader)
//   开始对新日志条目的共识
// rf.GetState() (term, isLeader)
//   询问 Raft 的当前任期，以及它是否认为自己是领导者
// ApplyMsg
//   每当新条目被提交到日志时，每个 Raft 节点
//   应该通过传递给 Make() 的 applyCh 向同一服务器上的服务（或测试器）发送 ApplyMsg。
//

import (
	//	"bytes"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	//	"course/labgob"
	"course/labrpc"
)

/*
上下文，在不同的地方有不同的指代。在我们的 Raft 的实现中，“上下文”就是指 Term 和 Role。即在一个任期内，只要你的角色没有变化，就能放心地推进状态机。
*/
// 这里面有个检查“上下文”是否丢失的关键函数：contextLostLocked 。
func (rf *Raft) contextLostLocked(role Role, term int) bool {
	return !(rf.currentTerm == term && rf.role == role)
}

// 当每个 Raft 节点意识到连续的日志条目已被提交时，
// 该节点应通过传递给 Make() 的 applyCh 向同一服务器上的服务（或测试器）发送 ApplyMsg。
// 将 CommandValid 设置为 true 以指示 ApplyMsg 包含新提交的日志条目。
//
// 在 PartD 部分，你会希望在 applyCh 上发送其他类型的消息（例如快照），
// 但对于这些其他用途，请将 CommandValid 设置为 false。
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// 用于 PartD：
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

// 实现单个 Raft 节点的 Go 对象。
/*
peers 创建：事先创建好的，所以只读
	ends := make([]*labrpc.ClientEnd, cfg.n)
	for j := 0; j < cfg.n; j++ {
		ends[j] = cfg.net.MakeEnd(cfg.endnames[i][j])
		cfg.net.Connect(cfg.endnames[i][j], j)
	}
*/
type Raft struct {
	mu        sync.Mutex          // 锁，用于保护对该节点状态的共享访问  - 互斥锁
	peers     []*labrpc.ClientEnd // 所有节点的 RPC 端点 - 相当于获得可以请求的客户端，数据固定，只读不加锁
	persister *Persister          // 用于保存该节点持久化状态的对象 - 快照对象
	me        int                 // 该节点在 peers[] 中的索引 - 相当于是id
	dead      int32               // 由 Kill() 设置	- 标识 raft dead

	// 你的数据在这里（PartA, PartB, PartC）。
	// 查看论文的图 2，了解 Raft 服务器必须维护的状态。
	role        Role // 节点角色
	currentTerm int  // 当前任期
	votedFor    int  // 投票给谁的节点ID

	// 用于选举循环
	electionStart   time.Time     // 开始计算超时
	electionTimeout time.Duration // 选举超时时间
}

// 将 Raft 的持久化状态保存到稳定存储中，
// 以便在崩溃和重启后可以检索。
// 查看论文的图 2，了解应该持久化的内容。
// 在实现快照之前，你应该将 nil 作为第二个参数传递给 persister.Save()。
// 在实现快照之后，传递当前快照（如果还没有快照，则传递 nil）。
func (rf *Raft) persist() {
	// 你的代码在这里（PartC）。
	// 示例：
	// w := new(bytes.Buffer)
	// e := labgob.NewEncoder(w)
	// e.Encode(rf.xxx)
	// e.Encode(rf.yyy)
	// raftstate := w.Bytes()
	// rf.persister.Save(raftstate, nil)
}

// 恢复之前持久化的状态。
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // 无状态启动？
		return
	}
	// 你的代码在这里（PartC）。
	// 示例：
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}

// 服务表示它已创建了一个快照，包含了
// 直到并包括 index 的所有信息。这意味着服务
// 不再需要通过（并包括）该索引的日志。
// Raft 现在应该尽可能地修剪其日志。
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// 你的代码在这里（PartD）。

}

// 使用 Raft 的服务（例如 k/v 服务器）想要开始
// 对下一个要附加到 Raft 日志的命令达成共识。
// 如果此服务器不是领导者，返回 false。否则开始共识并立即返回。
// 不能保证此命令会被提交到 Raft 日志，因为领导者可能会失败或输掉选举。
// 即使 Raft 实例已被杀死，此函数也应优雅地返回。
//
// 第一个返回值是命令在提交后将出现的索引。
// 第二个返回值是当前任期。
// 第三个返回值是 true，表示此服务器认为自己是领导者。
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	index := -1
	term := -1
	isLeader := true

	// Your code here (PartB).

	return index, term, isLeader
}

// 测试器不会在每次测试后停止由 Raft 创建的 goroutine，
// 但它会调用 Kill() 方法。你的代码可以使用 killed() 来
// 检查 Kill() 是否已被调用。使用 atomic 可以避免对锁的需求。
//
// 问题在于，长时间运行的 goroutine 会占用内存并可能消耗 CPU 时间，
// 这可能导致后续测试失败并生成令人困惑的调试输出。
// 任何带有长时间运行循环的 goroutine 都应该调用 killed() 来检查是否应该停止。
func (rf *Raft) Kill() {
	atomic.StoreInt32(&rf.dead, 1)
	// 你的代码在这里（如果需要）。
}

func (rf *Raft) killed() bool {
	z := atomic.LoadInt32(&rf.dead)
	return z == 1
}

func (rf *Raft) ticker() {
	for rf.killed() == false {

		// 你的代码在这里（PartA）
		// 检查是否应该开始领导者选举。

		// 暂停随机时间，介于 50 到 350 毫秒之间。
		ms := 50 + (rand.Int63() % 300)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// 服务或测试器想要创建一个 Raft 服务器。
// 所有 Raft 服务器（包括此服务器）的端口都在 peers[] 中。
// 此服务器的端口是 peers[me]。所有服务器的 peers[] 数组具有相同的顺序。
// persister 是此服务器保存其持久化状态的地方，并且最初也包含最近保存的状态（如果有）。
// applyCh 是一个通道，测试器或服务期望 Raft 通过此通道发送 ApplyMsg 消息。
// Make() 必须快速返回，因此它应该为任何长时间运行的工作启动 goroutine。
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{}
	rf.peers = peers
	rf.persister = persister
	rf.me = me

	// Your initialization code here (PartA, PartB, PartC).

	// initialize from state persisted before a crash
	rf.readPersist(persister.ReadRaftState())

	// start ticker goroutine to start elections
	// 开始选举 LOOP
	go rf.electionTicker()

	return rf
}
