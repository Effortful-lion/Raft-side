package raft

//
// Raft 测试。
//
// 我们将使用原始的 test_test.go 来测试你的代码以进行评分。
// 因此，虽然你可以修改此代码以帮助调试，但请在提交前使用原始代码进行测试。
//

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 测试器慷慨地允许解决方案在一秒内完成选举
// （比论文中的超时范围大得多）。
const RaftElectionTimeout = 1000 * time.Millisecond

// 测试最初的选举部分A
func TestInitialElectionPartA(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartA): initial election")

	// 是否选举出领导者？
	cfg.checkOneLeader()

	// 稍微睡眠以避免与跟随者了解选举结果的竞争，然后检查所有节点是否对任期达成一致。
	time.Sleep(50 * time.Millisecond)
	term1 := cfg.checkTerms()
	if term1 < 1 {
		t.Fatalf("term is %v, but should be at least 1", term1)
	}

	// 如果没有网络故障，领导者和任期是否保持不变？
	time.Sleep(2 * RaftElectionTimeout)
	term2 := cfg.checkTerms()
	if term1 != term2 {
		fmt.Printf("warning: term changed even though there were no failures")
	}

	// 应该仍然有一个领导者。
	cfg.checkOneLeader()

	cfg.end()
}

// 测试连任部分A
func TestReElectionPartA(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartA): election after network failure")

	leader1 := cfg.checkOneLeader()

	// 如果领导者断开连接，应该选举新的领导者。
	cfg.disconnect(leader1)
	cfg.checkOneLeader()

	// 如果旧领导者重新加入，不应该
	// 干扰新领导者。并且旧领导者
	// 应该切换为跟随者。
	cfg.connect(leader1)
	leader2 := cfg.checkOneLeader()

	// 如果没有多数派，不应该选举新的领导者。
	cfg.disconnect(leader2)
	cfg.disconnect((leader2 + 1) % servers)
	time.Sleep(2 * RaftElectionTimeout)

	// 检查唯一连接的服务器
	// 不认为自己是领导者。
	cfg.checkNoLeader()

	// 如果出现多数派，应该选举领导者。
	cfg.connect((leader2 + 1) % servers)
	cfg.checkOneLeader()

	// 最后一个节点的重新加入不应该阻止领导者的存在。
	cfg.connect(leader2)
	cfg.checkOneLeader()

	cfg.end()
}

func TestManyElectionsPartA(t *testing.T) {
	servers := 7
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartA): multiple elections")

	cfg.checkOneLeader()

	iters := 10
	for ii := 1; ii < iters; ii++ {
		// 断开三个节点的连接
		i1 := rand.Int() % servers
		i2 := rand.Int() % servers
		i3 := rand.Int() % servers
		cfg.disconnect(i1)
		cfg.disconnect(i2)
		cfg.disconnect(i3)

		// 要么当前领导者仍然存活，
		// 要么剩余的四个节点应该选举新的领导者。
		cfg.checkOneLeader()

		cfg.connect(i1)
		cfg.connect(i2)
		cfg.connect(i3)
	}

	cfg.checkOneLeader()

	cfg.end()
}

func TestBasicAgreePartB(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): basic agreement")

	iters := 3
	for index := 1; index < iters+1; index++ {
		nd, _ := cfg.nCommitted(index)
		if nd > 0 {
			t.Fatalf("some have committed before Start()")
		}

		xindex := cfg.one(index*100, servers, false)
		if xindex != index {
			t.Fatalf("got index %v but expected %v", xindex, index)
		}
	}

	cfg.end()
}

// 基于 RPC 字节计数检查，确保每个命令只发送给每个节点一次。
func TestRPCBytesPartB(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): RPC byte count")

	cfg.one(99, servers, false)
	bytes0 := cfg.bytesTotal()

	iters := 10
	var sent int64 = 0
	for index := 2; index < iters+2; index++ {
		cmd := randstring(5000)
		xindex := cfg.one(cmd, servers, false)
		if xindex != index {
			t.Fatalf("got index %v but expected %v", xindex, index)
		}
		sent += int64(len(cmd))
	}

	bytes1 := cfg.bytesTotal()
	got := bytes1 - bytes0
	expected := int64(servers) * sent
	if got > expected+50000 {
		t.Fatalf("too many RPC bytes; got %v, expected %v", got, expected)
	}

	cfg.end()
}

// 仅测试跟随者失败的情况。
func TestFollowerFailurePartB(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): test progressive failure of followers")

	cfg.one(101, servers, false)

	// 断开一个跟随者与网络的连接。
	leader1 := cfg.checkOneLeader()
	cfg.disconnect((leader1 + 1) % servers)

	// 即使有断开连接的跟随者，领导者和剩余的跟随者也应该能够达成一致。
	cfg.one(102, servers-1, false)
	time.Sleep(RaftElectionTimeout)
	cfg.one(103, servers-1, false)

	// 断开剩余的跟随者
	leader2 := cfg.checkOneLeader()
	cfg.disconnect((leader2 + 1) % servers)
	cfg.disconnect((leader2 + 2) % servers)

	// 提交一个命令。
	index, _, ok := cfg.rafts[leader2].Start(104)
	if ok != true {
		t.Fatalf("leader rejected Start()")
	}
	if index != 4 {
		t.Fatalf("expected index 4, got %v", index)
	}

	time.Sleep(2 * RaftElectionTimeout)

	// 检查命令 104 是否没有被提交。
	n, _ := cfg.nCommitted(index)
	if n > 0 {
		t.Fatalf("%v committed but no majority", n)
	}

	cfg.end()
}

// 仅测试领导者失败的情况。
func TestLeaderFailurePartB(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): test failure of leaders")

	cfg.one(101, servers, false)

	// 断开第一个领导者。
	leader1 := cfg.checkOneLeader()
	cfg.disconnect(leader1)

	// 剩余的跟随者应该选举
	// 新的领导者。
	cfg.one(102, servers-1, false)
	time.Sleep(RaftElectionTimeout)
	cfg.one(103, servers-1, false)

	// 断开新的领导者。
	leader2 := cfg.checkOneLeader()
	cfg.disconnect(leader2)

	// 向每个服务器提交一个命令。
	for i := 0; i < servers; i++ {
		cfg.rafts[i].Start(104)
	}

	time.Sleep(2 * RaftElectionTimeout)

	// 检查命令 104 是否没有被提交。
	n, _ := cfg.nCommitted(4)
	if n > 0 {
		t.Fatalf("%v committed but no majority", n)
	}

	cfg.end()
}

// 测试跟随者在断开连接后重新连接时是否能够参与共识。
func TestFailAgreePartB(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): agreement after follower reconnects")

	cfg.one(101, servers, false)

	// 断开一个跟随者与网络的连接。
	leader := cfg.checkOneLeader()
	cfg.disconnect((leader + 1) % servers)

	// 即使有断开连接的跟随者，领导者和剩余的跟随者也应该能够达成一致。
	cfg.one(102, servers-1, false)
	cfg.one(103, servers-1, false)
	time.Sleep(RaftElectionTimeout)
	cfg.one(104, servers-1, false)
	cfg.one(105, servers-1, false)

	// 重新连接
	cfg.connect((leader + 1) % servers)

	// 完整的服务器集合应该保留
	// 之前的共识，并且能够就新命令达成一致。
	cfg.one(106, servers, true)
	time.Sleep(RaftElectionTimeout)
	cfg.one(107, servers, true)

	cfg.end()
}

func TestFailNoAgreePartB(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): no agreement if too many followers disconnect")

	cfg.one(10, servers, false)

	// 5个跟随者中的3个断开连接
	leader := cfg.checkOneLeader()
	cfg.disconnect((leader + 1) % servers)
	cfg.disconnect((leader + 2) % servers)
	cfg.disconnect((leader + 3) % servers)

	index, _, ok := cfg.rafts[leader].Start(20)
	if ok != true {
		t.Fatalf("leader rejected Start()")
	}
	if index != 2 {
		t.Fatalf("expected index 2, got %v", index)
	}

	time.Sleep(2 * RaftElectionTimeout)

	n, _ := cfg.nCommitted(index)
	if n > 0 {
		t.Fatalf("%v committed but no majority", n)
	}

	// 修复
	cfg.connect((leader + 1) % servers)
	cfg.connect((leader + 2) % servers)
	cfg.connect((leader + 3) % servers)

	// 断开连接的多数派可能已经从
	// 他们自己的队伍中选择了一个领导者，忘记了索引 2。
	leader2 := cfg.checkOneLeader()
	index2, _, ok2 := cfg.rafts[leader2].Start(30)
	if ok2 == false {
		t.Fatalf("leader2 rejected Start()")
	}
	if index2 < 2 || index2 > 3 {
		t.Fatalf("unexpected index %v", index2)
	}

	cfg.one(1000, servers, true)

	cfg.end()
}

// 测试并发的 Start() 调用
func TestConcurrentStartsPartB(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): concurrent Start()s")

	var success bool
loop:
	for try := 0; try < 5; try++ {
		if try > 0 {
			// 给解决方案一些时间来稳定
			time.Sleep(3 * time.Second)
		}

		leader := cfg.checkOneLeader()
		_, term, ok := cfg.rafts[leader].Start(1)
		if !ok {
			// 领导者切换得非常快
			continue
		}

		iters := 5
		var wg sync.WaitGroup
		is := make(chan int, iters)
		for ii := 0; ii < iters; ii++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				i, term1, ok := cfg.rafts[leader].Start(100 + i)
				if term1 != term {
					return
				}
				if ok != true {
					return
				}
				is <- i
			}(ii)
		}

		wg.Wait()
		close(is)

		for j := 0; j < servers; j++ {
			if t, _ := cfg.rafts[j].GetState(); t != term {
				// 任期已更改 - 无法期望低 RPC 计数
				continue loop
			}
		}

		failed := false
		cmds := []int{}
		for index := range is {
			cmd := cfg.wait(index, servers, term)
			if ix, ok := cmd.(int); ok {
				if ix == -1 {
					// 节点已进入后续任期
					// 因此我们不能期望所有 Start() 调用都
					// 成功
					failed = true
					break
				}
				cmds = append(cmds, ix)
			} else {
				t.Fatalf("value %v is not an int", cmd)
			}
		}

		if failed {
			// 避免 goroutine 泄漏
			go func() {
				for range is {
				}
			}()
			continue
		}

		for ii := 0; ii < iters; ii++ {
			x := 100 + ii
			ok := false
			for j := 0; j < len(cmds); j++ {
				if cmds[j] == x {
					ok = true
				}
			}
			if ok == false {
				t.Fatalf("cmd %v missing in %v", x, cmds)
			}
		}

		success = true
		break
	}

	if !success {
		t.Fatalf("term changed too often")
	}

	cfg.end()
}

func TestRejoinPartB(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): rejoin of partitioned leader")

	cfg.one(101, servers, true)

	// 领导者网络故障
	leader1 := cfg.checkOneLeader()
	cfg.disconnect(leader1)

	// 让旧领导者尝试就一些条目达成一致
	cfg.rafts[leader1].Start(102)
	cfg.rafts[leader1].Start(103)
	cfg.rafts[leader1].Start(104)

	// 新领导者提交，也包括索引=2
	cfg.one(103, 2, true)

	// 新领导者网络故障
	leader2 := cfg.checkOneLeader()
	cfg.disconnect(leader2)

	// 旧领导者重新连接
	cfg.connect(leader1)

	cfg.one(104, 2, true)

	// 现在所有节点都在一起
	cfg.connect(leader2)

	cfg.one(105, servers, true)

	cfg.end()
}

func TestBackupPartB(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): leader backs up quickly over incorrect follower logs")

	cfg.one(rand.Int(), servers, true)

	// 将领导者和一个跟随者放在一个分区中
	leader1 := cfg.checkOneLeader()
	cfg.disconnect((leader1 + 2) % servers)
	cfg.disconnect((leader1 + 3) % servers)
	cfg.disconnect((leader1 + 4) % servers)

	// 提交许多不会提交的命令
	for i := 0; i < 50; i++ {
		cfg.rafts[leader1].Start(rand.Int())
	}

	time.Sleep(RaftElectionTimeout / 2)

	cfg.disconnect((leader1 + 0) % servers)
	cfg.disconnect((leader1 + 1) % servers)

	// 允许其他分区恢复
	cfg.connect((leader1 + 2) % servers)
	cfg.connect((leader1 + 3) % servers)
	cfg.connect((leader1 + 4) % servers)

	// 向新组提交许多成功的命令。
	for i := 0; i < 50; i++ {
		cfg.one(rand.Int(), 3, true)
	}

	// 现在另一个分区的领导者和一个跟随者
	leader2 := cfg.checkOneLeader()
	other := (leader1 + 2) % servers
	if leader2 == other {
		other = (leader2 + 1) % servers
	}
	cfg.disconnect(other)

	// 更多不会提交的命令
	for i := 0; i < 50; i++ {
		cfg.rafts[leader2].Start(rand.Int())
	}

	time.Sleep(RaftElectionTimeout / 2)

	// 使原始领导者恢复活力，
	for i := 0; i < servers; i++ {
		cfg.disconnect(i)
	}
	cfg.connect((leader1 + 0) % servers)
	cfg.connect((leader1 + 1) % servers)
	cfg.connect(other)

	// 向新组提交许多成功的命令。
	for i := 0; i < 50; i++ {
		cfg.one(rand.Int(), 3, true)
	}

	// 现在所有人都在一起
	for i := 0; i < servers; i++ {
		cfg.connect(i)
	}
	cfg.one(rand.Int(), servers, true)

	cfg.end()
}

func TestCountPartB(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartB): RPC counts aren't too high")

	rpcs := func() (n int) {
		for j := 0; j < servers; j++ {
			n += cfg.rpcCount(j)
		}
		return
	}

	leader := cfg.checkOneLeader()

	total1 := rpcs()

	if total1 > 30 || total1 < 1 {
		t.Fatalf("too many or few RPCs (%v) to elect initial leader\n", total1)
	}

	var total2 int
	var success bool
loop:
	for try := 0; try < 5; try++ {
		if try > 0 {
			// 给解决方案一些时间来稳定
			time.Sleep(3 * time.Second)
		}

		leader = cfg.checkOneLeader()
		total1 = rpcs()

		iters := 10
		starti, term, ok := cfg.rafts[leader].Start(1)
		if !ok {
			// 领导者切换得非常快
			continue
		}
		cmds := []int{}
		for i := 1; i < iters+2; i++ {
			x := int(rand.Int31())
			cmds = append(cmds, x)
			index1, term1, ok := cfg.rafts[leader].Start(x)
			if term1 != term {
				// 启动时任期已更改
				continue loop
			}
			if !ok {
				// 不再是领导者，因此任期已更改
				continue loop
			}
			if starti+i != index1 {
				t.Fatalf("Start() failed")
			}
		}

		for i := 1; i < iters+1; i++ {
			cmd := cfg.wait(starti+i, servers, term)
			if ix, ok := cmd.(int); ok == false || ix != cmds[i-1] {
				if ix == -1 {
					// 任期已更改 - 重试
					continue loop
				}
				t.Fatalf("wrong value %v committed for index %v; expected %v\n", cmd, starti+i, cmds)
			}
		}

		failed := false
		total2 = 0
		for j := 0; j < servers; j++ {
			if t, _ := cfg.rafts[j].GetState(); t != term {
				// 任期已更改 - 无法期望低 RPC 计数
				// 需要继续更新 total2
				failed = true
			}
			total2 += cfg.rpcCount(j)
		}

		if failed {
			continue loop
		}

		if total2-total1 > (iters+1+3)*3 {
			t.Fatalf("too many RPCs (%v) for %v entries\n", total2-total1, iters)
		}

		success = true
		break
	}

	if !success {
		t.Fatalf("term changed too often")
	}

	time.Sleep(RaftElectionTimeout)

	total3 := 0
	for j := 0; j < servers; j++ {
		total3 += cfg.rpcCount(j)
	}

	if total3-total2 > 3*20 {
		t.Fatalf("too many RPCs (%v) for 1 second of idleness\n", total3-total2)
	}

	cfg.end()
}

func TestPersist1PartC(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartC): basic persistence")

	cfg.one(11, servers, true)

	// 崩溃并重启所有服务器
	for i := 0; i < servers; i++ {
		cfg.start1(i, cfg.applier)
	}
	for i := 0; i < servers; i++ {
		cfg.disconnect(i)
		cfg.connect(i)
	}

	cfg.one(12, servers, true)

	leader1 := cfg.checkOneLeader()
	cfg.disconnect(leader1)
	cfg.start1(leader1, cfg.applier)
	cfg.connect(leader1)

	cfg.one(13, servers, true)

	leader2 := cfg.checkOneLeader()
	cfg.disconnect(leader2)
	cfg.one(14, servers-1, true)
	cfg.start1(leader2, cfg.applier)
	cfg.connect(leader2)

	cfg.wait(4, servers, -1) // 等待 leader2 加入，然后再杀死 i3

	i3 := (cfg.checkOneLeader() + 1) % servers
	cfg.disconnect(i3)
	cfg.one(15, servers-1, true)
	cfg.start1(i3, cfg.applier)
	cfg.connect(i3)

	cfg.one(16, servers, true)

	cfg.end()
}

func TestPersist2PartC(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartC): more persistence")

	index := 1
	for iters := 0; iters < 5; iters++ {
		cfg.one(10+index, servers, true)
		index++

		leader1 := cfg.checkOneLeader()

		cfg.disconnect((leader1 + 1) % servers)
		cfg.disconnect((leader1 + 2) % servers)

		cfg.one(10+index, servers-2, true)
		index++

		cfg.disconnect((leader1 + 0) % servers)
		cfg.disconnect((leader1 + 3) % servers)
		cfg.disconnect((leader1 + 4) % servers)

		cfg.start1((leader1+1)%servers, cfg.applier)
		cfg.start1((leader1+2)%servers, cfg.applier)
		cfg.connect((leader1 + 1) % servers)
		cfg.connect((leader1 + 2) % servers)

		time.Sleep(RaftElectionTimeout)

		cfg.start1((leader1+3)%servers, cfg.applier)
		cfg.connect((leader1 + 3) % servers)

		cfg.one(10+index, servers-2, true)
		index++

		cfg.connect((leader1 + 4) % servers)
		cfg.connect((leader1 + 0) % servers)
	}

	cfg.one(1000, servers, true)

	cfg.end()
}

func TestPersist3PartC(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartC): partitioned leader and one follower crash, leader restarts")

	cfg.one(101, 3, true)

	leader := cfg.checkOneLeader()
	cfg.disconnect((leader + 2) % servers)

	cfg.one(102, 2, true)

	cfg.crash1((leader + 0) % servers)
	cfg.crash1((leader + 1) % servers)
	cfg.connect((leader + 2) % servers)
	cfg.start1((leader+0)%servers, cfg.applier)
	cfg.connect((leader + 0) % servers)

	cfg.one(103, 2, true)

	cfg.start1((leader+1)%servers, cfg.applier)
	cfg.connect((leader + 1) % servers)

	cfg.one(104, servers, true)

	cfg.end()
}

// 测试扩展 Raft 论文图 8 中描述的场景。每次迭代都会要求领导者（如果有）在 Raft 日志中插入一个命令。
// 如果有领导者，该领导者会以高概率快速失败（可能未提交命令），或
// 以低概率过一段时间后崩溃（很可能已提交命令）。
// 如果存活的服务器数量不足以形成多数派，可能会启动一个新服务器。
// 新任期的领导者可能会尝试完成复制尚未提交的日志条目。
func TestFigure8PartC(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartC): Figure 8")

	cfg.one(rand.Int(), 1, true)

	nup := servers
	for iters := 0; iters < 1000; iters++ {
		leader := -1
		for i := 0; i < servers; i++ {
			if cfg.rafts[i] != nil {
				_, _, ok := cfg.rafts[i].Start(rand.Int())
				if ok {
					leader = i
				}
			}
		}

		if (rand.Int() % 1000) < 100 {
			ms := rand.Int63() % (int64(RaftElectionTimeout/time.Millisecond) / 2)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		} else {
			ms := (rand.Int63() % 13)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}

		if leader != -1 {
			cfg.crash1(leader)
			nup -= 1
		}

		if nup < 3 {
			s := rand.Int() % servers
			if cfg.rafts[s] == nil {
				cfg.start1(s, cfg.applier)
				cfg.connect(s)
				nup += 1
			}
		}
	}

	for i := 0; i < servers; i++ {
		if cfg.rafts[i] == nil {
			cfg.start1(i, cfg.applier)
			cfg.connect(i)
		}
	}

	cfg.one(rand.Int(), servers, true)

	cfg.end()
}

func TestUnreliableAgreePartC(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, true, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartC): unreliable agreement")

	var wg sync.WaitGroup

	for iters := 1; iters < 50; iters++ {
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func(iters, j int) {
				defer wg.Done()
				cfg.one((100*iters)+j, 1, true)
			}(iters, j)
		}
		cfg.one(iters, 1, true)
	}

	cfg.setunreliable(false)

	wg.Wait()

	cfg.one(100, servers, true)

	cfg.end()
}

func TestFigure8UnreliablePartC(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, true, false)
	defer cfg.cleanup()

	cfg.begin("Test (PartC): Figure 8 (unreliable)")

	cfg.one(rand.Int()%10000, 1, true)

	nup := servers
	for iters := 0; iters < 1000; iters++ {
		if iters == 200 {
			cfg.setlongreordering(true)
		}
		leader := -1
		for i := 0; i < servers; i++ {
			_, _, ok := cfg.rafts[i].Start(rand.Int() % 10000)
			if ok && cfg.connected[i] {
				leader = i
			}
		}

		if (rand.Int() % 1000) < 100 {
			ms := rand.Int63() % (int64(RaftElectionTimeout/time.Millisecond) / 2)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		} else {
			ms := (rand.Int63() % 13)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}

		if leader != -1 && (rand.Int()%1000) < int(RaftElectionTimeout/time.Millisecond)/2 {
			cfg.disconnect(leader)
			nup -= 1
		}

		if nup < 3 {
			s := rand.Int() % servers
			if cfg.connected[s] == false {
				cfg.connect(s)
				nup += 1
			}
		}
	}

	for i := 0; i < servers; i++ {
		if cfg.connected[i] == false {
			cfg.connect(i)
		}
	}

	cfg.one(rand.Int()%10000, servers, true)

	cfg.end()
}

func internalChurn(t *testing.T, unreliable bool) {

	servers := 5
	cfg := make_config(t, servers, unreliable, false)
	defer cfg.cleanup()

	if unreliable {
		cfg.begin("Test (PartC): unreliable churn")
	} else {
		cfg.begin("Test (PartC): churn")
	}

	stop := int32(0)

	// 创建并发客户端
	cfn := func(me int, ch chan []int) {
		var ret []int
		ret = nil
		defer func() { ch <- ret }()
		values := []int{}
		for atomic.LoadInt32(&stop) == 0 {
			x := rand.Int()
			index := -1
			ok := false
			for i := 0; i < servers; i++ {
				// 尝试所有服务器，可能其中一个是领导者
				cfg.mu.Lock()
				rf := cfg.rafts[i]
				cfg.mu.Unlock()
				if rf != nil {
					index1, _, ok1 := rf.Start(x)
					if ok1 {
						ok = ok1
						index = index1
					}
				}
			}
			if ok {
				// 领导者可能会提交我们的值，也可能不会。
				// 但不要永远等待。
				for _, to := range []int{10, 20, 50, 100, 200} {
					nd, cmd := cfg.nCommitted(index)
					if nd > 0 {
						if xx, ok := cmd.(int); ok {
							if xx == x {
								values = append(values, x)
							}
						} else {
							cfg.t.Fatalf("wrong command type")
						}
						break
					}
					time.Sleep(time.Duration(to) * time.Millisecond)
				}
			} else {
				time.Sleep(time.Duration(79+me*17) * time.Millisecond)
			}
		}
		ret = values
	}

	ncli := 3
	cha := []chan []int{}
	for i := 0; i < ncli; i++ {
		cha = append(cha, make(chan []int))
		go cfn(i, cha[i])
	}

	for iters := 0; iters < 20; iters++ {
		if (rand.Int() % 1000) < 200 {
			i := rand.Int() % servers
			cfg.disconnect(i)
		}

		if (rand.Int() % 1000) < 500 {
			i := rand.Int() % servers
			if cfg.rafts[i] == nil {
				cfg.start1(i, cfg.applier)
			}
			cfg.connect(i)
		}

		if (rand.Int() % 1000) < 200 {
			i := rand.Int() % servers
			if cfg.rafts[i] != nil {
				cfg.crash1(i)
			}
		}

		// 使崩溃/重启的频率足够低，以便节点通常能够
		// 跟上，但又不至于低到一切都已经从一个变化
		// 稳定下来。选择一个比选举超时小的值，但不要小太多。
		time.Sleep((RaftElectionTimeout * 7) / 10)
	}

	time.Sleep(RaftElectionTimeout)
	cfg.setunreliable(false)
	for i := 0; i < servers; i++ {
		if cfg.rafts[i] == nil {
			cfg.start1(i, cfg.applier)
		}
		cfg.connect(i)
	}

	atomic.StoreInt32(&stop, 1)

	values := []int{}
	for i := 0; i < ncli; i++ {
		vv := <-cha[i]
		if vv == nil {
			t.Fatal("client failed")
		}
		values = append(values, vv...)
	}

	time.Sleep(RaftElectionTimeout)

	lastIndex := cfg.one(rand.Int(), servers, true)

	really := make([]int, lastIndex+1)
	for index := 1; index <= lastIndex; index++ {
		v := cfg.wait(index, servers, -1)
		if vi, ok := v.(int); ok {
			really = append(really, vi)
		} else {
			t.Fatalf("not an int")
		}
	}

	for _, v1 := range values {
		ok := false
		for _, v2 := range really {
			if v1 == v2 {
				ok = true
			}
		}
		if ok == false {
			cfg.t.Fatalf("didn't find a value")
		}
	}

	cfg.end()
}

func TestReliableChurnPartC(t *testing.T) {
	internalChurn(t, false)
}

func TestUnreliableChurnPartC(t *testing.T) {
	internalChurn(t, true)
}

const MAXLOGSIZE = 2000

func snapcommon(t *testing.T, name string, disconnect bool, reliable bool, crash bool) {
	iters := 30
	servers := 3
	cfg := make_config(t, servers, !reliable, true)
	defer cfg.cleanup()

	cfg.begin(name)

	cfg.one(rand.Int(), servers, true)
	leader1 := cfg.checkOneLeader()

	for i := 0; i < iters; i++ {
		victim := (leader1 + 1) % servers
		sender := leader1
		if i%3 == 1 {
			sender = (leader1 + 1) % servers
			victim = leader1
		}

		if disconnect {
			cfg.disconnect(victim)
			cfg.one(rand.Int(), servers-1, true)
		}
		if crash {
			cfg.crash1(victim)
			cfg.one(rand.Int(), servers-1, true)
		}

		// 可能发送足够的命令以获取快照
		nn := (SnapShotInterval / 2) + (rand.Int() % SnapShotInterval)
		for i := 0; i < nn; i++ {
			cfg.rafts[sender].Start(rand.Int())
		}

		// 让 applier 线程赶上 Start() 调用
		if disconnect == false && crash == false {
			// 确保所有跟随者都已赶上，以便
			// TestSnapshotBasicPartD() 不需要 InstallSnapshot RPC。
			cfg.one(rand.Int(), servers, true)
		} else {
			cfg.one(rand.Int(), servers-1, true)
		}

		if cfg.LogSize() >= MAXLOGSIZE {
			cfg.t.Fatalf("Log size too large")
		}
		if disconnect {
			// 重新连接一个跟随者，该跟随者可能落后并
			// 需要接收快照来赶上。
			cfg.connect(victim)
			cfg.one(rand.Int(), servers, true)
			leader1 = cfg.checkOneLeader()
		}
		if crash {
			cfg.start1(victim, cfg.applierSnap)
			cfg.connect(victim)
			cfg.one(rand.Int(), servers, true)
			leader1 = cfg.checkOneLeader()
		}
	}
	cfg.end()
}

func TestSnapshotBasicPartD(t *testing.T) {
	snapcommon(t, "Test (PartD): snapshots basic", false, true, false)
}

func TestSnapshotInstallPartD(t *testing.T) {
	snapcommon(t, "Test (PartD): install snapshots (disconnect)", true, true, false)
}

func TestSnapshotInstallUnreliablePartD(t *testing.T) {
	snapcommon(t, "Test (PartD): install snapshots (disconnect+unreliable)",
		true, false, false)
}

func TestSnapshotInstallCrashPartD(t *testing.T) {
	snapcommon(t, "Test (PartD): install snapshots (crash)", false, true, true)
}

func TestSnapshotInstallUnCrashPartD(t *testing.T) {
	snapcommon(t, "Test (PartD): install snapshots (unreliable+crash)", false, false, true)
}

// 服务器是否持久化快照，并在重启时使用快照和日志尾部？
func TestSnapshotAllCrashPartD(t *testing.T) {
	servers := 3
	iters := 5
	cfg := make_config(t, servers, false, true)
	defer cfg.cleanup()

	cfg.begin("Test (PartD): crash and restart all servers")

	cfg.one(rand.Int(), servers, true)

	for i := 0; i < iters; i++ {
		// 可能足够获取快照
		nn := (SnapShotInterval / 2) + (rand.Int() % SnapShotInterval)
		for i := 0; i < nn; i++ {
			cfg.one(rand.Int(), servers, true)
		}

		index1 := cfg.one(rand.Int(), servers, true)

		// 崩溃所有服务器
		for i := 0; i < servers; i++ {
			cfg.crash1(i)
		}

		// 恢复所有服务器
		for i := 0; i < servers; i++ {
			cfg.start1(i, cfg.applierSnap)
			cfg.connect(i)
		}

		index2 := cfg.one(rand.Int(), servers, true)
		if index2 < index1+1 {
			t.Fatalf("index decreased from %v to %v", index1, index2)
		}
	}
	cfg.end()
}

// 服务器是否正确初始化其内存中的快照副本，确保未来对持久化状态的写入不会丢失状态？
func TestSnapshotInitPartD(t *testing.T) {
	servers := 3
	cfg := make_config(t, servers, false, true)
	defer cfg.cleanup()

	cfg.begin("Test (PartD): snapshot initialization after crash")
	cfg.one(rand.Int(), servers, true)

	// 足够的操作以生成快照
	nn := SnapShotInterval + 1
	for i := 0; i < nn; i++ {
		cfg.one(rand.Int(), servers, true)
	}

	// 崩溃所有服务器
	for i := 0; i < servers; i++ {
		cfg.crash1(i)
	}

	// 恢复所有服务器
	for i := 0; i < servers; i++ {
		cfg.start1(i, cfg.applierSnap)
		cfg.connect(i)
	}

	// 单个操作，以便将某些内容写回持久化存储。
	cfg.one(rand.Int(), servers, true)

	// 崩溃所有服务器
	for i := 0; i < servers; i++ {
		cfg.crash1(i)
	}

	// 恢复所有服务器
	for i := 0; i < servers; i++ {
		cfg.start1(i, cfg.applierSnap)
		cfg.connect(i)
	}

	// 执行另一个操作以触发潜在的错误
	cfg.one(rand.Int(), servers, true)
	cfg.end()
}
