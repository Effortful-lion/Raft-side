package labrpc

//
// 基于通道的 RPC，用于 6.5840 实验。
//
// 模拟一个可以丢失请求、丢失回复、延迟消息以及完全断开特定主机连接的网络。
//
// 我们将使用原始的 labrpc.go 来测试你的代码以进行评分。
// 因此，虽然你可以修改此代码以帮助调试，但请在提交前使用原始代码进行测试。
//
// 改编自 Go 的 net/rpc/server.go。
//
// 发送经过 labgob 编码的值，以确保 RPC 不包含对程序对象的引用。
//
// net := MakeNetwork() -- 持有网络、客户端和服务器。
// end := net.MakeEnd(endname) -- 创建客户端端点，用于与一个服务器通信。
// net.AddServer(servername, server) -- 向网络添加命名服务器。
// net.DeleteServer(servername) -- 移除命名服务器。
// net.Connect(endname, servername) -- 将客户端连接到服务器。
// net.Enable(endname, enabled) -- 启用/禁用客户端。
// net.Reliable(bool) -- false 表示丢弃/延迟消息
//
// end.Call("Raft.AppendEntries", &args, &reply) -- 发送 RPC，等待回复。
// "Raft" 是要调用的服务器结构体的名称。
// "AppendEntries" 是要调用的方法的名称。
// Call() 返回 true 表示服务器执行了请求且回复有效。
// Call() 返回 false 如果网络丢失了请求或回复，或者服务器已关闭。
// 在同一个 ClientEnd 上可以同时有多个 Call() 调用在进行中。
// 并发的 Call() 调用可能会以乱序传递给服务器，因为网络可能会重新排序消息。
// Call() 保证会返回（可能会有延迟），除非服务器端的处理函数不返回。
// 服务器 RPC 处理函数必须将其 args 和 reply 参数声明为指针，以便它们的类型与 Call() 的参数类型完全匹配。
//
// srv := MakeServer()
// srv.AddService(svc) -- 服务器可以有多个服务，例如 Raft 和 k/v
//   将 srv 传递给 net.AddServer()
//
// svc := MakeService(receiverObject) -- 对象的方法将处理 RPC
//   类似于 Go 的 rpcs.Register()
//   将 svc 传递给 srv.AddService()
//

import (
	"bytes"
	"course/labgob"
	"log"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type reqMsg struct {
	endname  interface{} // name of sending ClientEnd
	svcMeth  string      // e.g. "Raft.AppendEntries"
	argsType reflect.Type
	args     []byte
	replyCh  chan replyMsg
}

type replyMsg struct {
	ok    bool
	reply []byte
}

type ClientEnd struct {
	endname interface{}   // this end-point's name
	ch      chan reqMsg   // copy of Network.endCh
	done    chan struct{} // closed when Network is cleaned up
}

// 发送 RPC，等待回复。
// 返回值表示成功；false 表示未从服务器收到回复。
func (e *ClientEnd) Call(svcMeth string, args interface{}, reply interface{}) bool {
	req := reqMsg{}
	req.endname = e.endname
	req.svcMeth = svcMeth
	req.argsType = reflect.TypeOf(args)
	req.replyCh = make(chan replyMsg)

	qb := new(bytes.Buffer)
	qe := labgob.NewEncoder(qb)
	if err := qe.Encode(args); err != nil {
		panic(err)
	}
	req.args = qb.Bytes()

	//
	// send the request.
	//
	select {
	case e.ch <- req:
		// the request has been sent.
	case <-e.done:
		// entire Network has been destroyed.
		return false
	}

	//
	// wait for the reply.
	//
	rep := <-req.replyCh
	if rep.ok {
		rb := bytes.NewBuffer(rep.reply)
		rd := labgob.NewDecoder(rb)
		if err := rd.Decode(reply); err != nil {
			log.Fatalf("ClientEnd.Call(): decode reply: %v\n", err)
		}
		return true
	} else {
		return false
	}
}

type Network struct {
	mu             sync.Mutex
	reliable       bool
	longDelays     bool                        // pause a long time on send on disabled connection
	longReordering bool                        // sometimes delay replies a long time
	ends           map[interface{}]*ClientEnd  // ends, by name
	enabled        map[interface{}]bool        // by end name
	servers        map[interface{}]*Server     // servers, by name
	connections    map[interface{}]interface{} // endname -> servername
	endCh          chan reqMsg
	done           chan struct{} // closed when Network is cleaned up
	count          int32         // total RPC count, for statistics
	bytes          int64         // total bytes send, for statistics
}

func MakeNetwork() *Network {
	rn := &Network{}
	rn.reliable = true
	rn.ends = map[interface{}]*ClientEnd{}
	rn.enabled = map[interface{}]bool{}
	rn.servers = map[interface{}]*Server{}
	rn.connections = map[interface{}](interface{}){}
	rn.endCh = make(chan reqMsg)
	rn.done = make(chan struct{})

	// 单个 goroutine 处理所有 ClientEnd.Call()
	go func() {
		for {
			select {
			case xreq := <-rn.endCh:
				atomic.AddInt32(&rn.count, 1)
				atomic.AddInt64(&rn.bytes, int64(len(xreq.args)))
				go rn.processReq(xreq)
			case <-rn.done:
				return
			}
		}
	}()

	return rn
}

func (rn *Network) Cleanup() {
	close(rn.done)
}

// 控制丢包
func (rn *Network) Reliable(yes bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	rn.reliable = yes
}

// 控制乱序
func (rn *Network) LongReordering(yes bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	rn.longReordering = yes
}

// 控制延迟
func (rn *Network) LongDelays(yes bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	rn.longDelays = yes
}

func (rn *Network) readEndnameInfo(endname interface{}) (enabled bool,
	servername interface{}, server *Server, reliable bool, longreordering bool,
) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	enabled = rn.enabled[endname]
	servername = rn.connections[endname]
	if servername != nil {
		server = rn.servers[servername]
	}
	reliable = rn.reliable
	longreordering = rn.longReordering
	return
}

func (rn *Network) isServerDead(endname interface{}, servername interface{}, server *Server) bool {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	if rn.enabled[endname] == false || rn.servers[servername] != server {
		return true
	}
	return false
}

func (rn *Network) processReq(req reqMsg) {
	enabled, servername, server, reliable, longreordering := rn.readEndnameInfo(req.endname)

	if enabled && servername != nil && server != nil {
		if reliable == false {
			// 短暂延迟
			ms := (rand.Int() % 27)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}

		if reliable == false && (rand.Int()%1000) < 100 {
			// 丢弃请求，返回超时
			req.replyCh <- replyMsg{false, nil}
			return
		}

		// 执行请求（调用 RPC 处理程序）。
		// 在单独的线程中执行，以便我们可以定期检查
		// 服务器是否已被终止，RPC 是否应获得失败回复。
		ech := make(chan replyMsg)
		go func() {
			r := server.dispatch(req)
			ech <- r
		}()

		// 等待处理程序返回，
		// 但如果调用了 DeleteServer()，则停止等待，
		// 并返回错误。
		var reply replyMsg
		replyOK := false
		serverDead := false
		for replyOK == false && serverDead == false {
			select {
			case reply = <-ech:
				replyOK = true
			case <-time.After(100 * time.Millisecond):
				serverDead = rn.isServerDead(req.endname, servername, server)
				if serverDead {
					go func() {
						<-ech // 排空通道，让之前创建的 goroutine 终止
					}()
				}
			}
		}

		// 如果调用了 DeleteServer()，即
		// 服务器已被终止，则不回复。这是为了避免
		// 客户端获得对 Append 的肯定回复，
		// 但服务器将更新持久化到旧的 Persister 中的情况。
		// config.go 会在替换 Persister 之前小心地调用 DeleteServer()。
		serverDead = rn.isServerDead(req.endname, servername, server)

		if replyOK == false || serverDead == true {
			// 服务器在我们等待时被终止；返回错误。
			req.replyCh <- replyMsg{false, nil}
		} else if reliable == false && (rand.Int()%1000) < 100 {
			// 丢弃回复，返回超时
			req.replyCh <- replyMsg{false, nil}
		} else if longreordering == true && rand.Intn(900) < 600 {
			// 延迟响应一段时间
			ms := 200 + rand.Intn(1+rand.Intn(2000))
			// Russ 指出，这种定时器安排将减少
			// goroutine 的数量，从而减少竞争检测器
			// 出错的可能性。
			time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
				atomic.AddInt64(&rn.bytes, int64(len(reply.reply)))
				req.replyCh <- reply
			})
		} else {
			atomic.AddInt64(&rn.bytes, int64(len(reply.reply)))
			req.replyCh <- reply
		}
	} else {
		// 模拟无回复和最终超时。
		ms := 0
		if rn.longDelays {
			// 让 Raft 测试检查领导者是否不同步发送 RPC。
			ms = (rand.Int() % 7000)
		} else {
			// 许多 kv 测试要求客户端相当快速地尝试每个服务器。
			ms = (rand.Int() % 100)
		}
		time.AfterFunc(time.Duration(ms)*time.Millisecond, func() {
			req.replyCh <- replyMsg{false, nil}
		})
	}

}

// 创建客户端端点。
// 启动监听和传递的线程。
func (rn *Network) MakeEnd(endname interface{}) *ClientEnd {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	if _, ok := rn.ends[endname]; ok {
		log.Fatalf("MakeEnd: %v already exists\n", endname)
	}

	e := &ClientEnd{}
	e.endname = endname
	e.ch = rn.endCh
	e.done = rn.done
	rn.ends[endname] = e
	rn.enabled[endname] = false
	rn.connections[endname] = nil

	return e
}

func (rn *Network) AddServer(servername interface{}, rs *Server) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	rn.servers[servername] = rs
}

// 模拟服务器节点断开
func (rn *Network) DeleteServer(servername interface{}) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	rn.servers[servername] = nil
}

// 将 ClientEnd 连接到服务器。
// 一个 ClientEnd 在其生命周期中只能连接一次。
func (rn *Network) Connect(endname interface{}, servername interface{}) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	rn.connections[endname] = servername
}

// 启用/禁用 ClientEnd。
func (rn *Network) Enable(endname interface{}, enabled bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	rn.enabled[endname] = enabled
}

// 获取服务器的传入 RPC 计数。
func (rn *Network) GetCount(servername interface{}) int {
	rn.mu.Lock()
	defer rn.mu.Unlock()

	svr := rn.servers[servername]
	return svr.GetCount()
}

func (rn *Network) GetTotalCount() int {
	x := atomic.LoadInt32(&rn.count)
	return int(x)
}

func (rn *Network) GetTotalBytes() int64 {
	x := atomic.LoadInt64(&rn.bytes)
	return x
}

// 服务器是服务的集合，所有服务共享同一个 RPC 分发器。
// 这样，例如 Raft 和 k/v 服务器都可以监听同一个 RPC 端点。
type Server struct {
	mu       sync.Mutex
	services map[string]*Service
	count    int // 传入的 RPCs
}

func MakeServer() *Server {
	rs := &Server{}
	rs.services = map[string]*Service{}
	return rs
}

func (rs *Server) AddService(svc *Service) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.services[svc.name] = svc
}

func (rs *Server) dispatch(req reqMsg) replyMsg {
	rs.mu.Lock()

	rs.count += 1

	// 将 Raft.AppendEntries 拆分为服务和方法
	dot := strings.LastIndex(req.svcMeth, ".")
	serviceName := req.svcMeth[:dot]
	methodName := req.svcMeth[dot+1:]

	service, ok := rs.services[serviceName]

	rs.mu.Unlock()

	if ok {
		return service.dispatch(methodName, req)
	} else {
		choices := []string{}
		for k, _ := range rs.services {
			choices = append(choices, k)
		}
		log.Fatalf("labrpc.Server.dispatch(): unknown service %v in %v.%v; expecting one of %v\n",
			serviceName, serviceName, methodName, choices)
		return replyMsg{false, nil}
	}
}

func (rs *Server) GetCount() int {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.count
}

// 一个带有可通过 RPC 调用的方法的对象。
// 单个服务器可以有多个 Service。
type Service struct {
	name    string
	rcvr    reflect.Value
	typ     reflect.Type
	methods map[string]reflect.Method
}

func MakeService(rcvr interface{}) *Service {
	svc := &Service{}
	svc.typ = reflect.TypeOf(rcvr)
	svc.rcvr = reflect.ValueOf(rcvr)
	svc.name = reflect.Indirect(svc.rcvr).Type().Name()
	svc.methods = map[string]reflect.Method{}

	for m := 0; m < svc.typ.NumMethod(); m++ {
		method := svc.typ.Method(m)
		mtype := method.Type
		mname := method.Name

		//fmt.Printf("%v pp %v ni %v 1k %v 2k %v no %v\n",
		//	mname, method.PkgPath, mtype.NumIn(), mtype.In(1).Kind(), mtype.In(2).Kind(), mtype.NumOut())

		if method.PkgPath != "" || // 首字母大写？
			mtype.NumIn() != 3 ||
			//mtype.In(1).Kind() != reflect.Ptr ||
			mtype.In(2).Kind() != reflect.Ptr ||
			mtype.NumOut() != 0 {
			// 该方法不适合作为处理程序
			//fmt.Printf("bad method: %v\n", mname)
		} else {
			// 该方法看起来像处理程序
			svc.methods[mname] = method
		}
	}

	return svc
}

func (svc *Service) dispatch(methname string, req reqMsg) replyMsg {
	if method, ok := svc.methods[methname]; ok {
		// 准备读取参数的空间。
		// Value 的类型将是 req.argsType 的指针。
		args := reflect.New(req.argsType)

		// 解码参数。
		ab := bytes.NewBuffer(req.args)
		ad := labgob.NewDecoder(ab)
		ad.Decode(args.Interface())

		// 为回复分配空间。
		replyType := method.Type.In(2)
		replyType = replyType.Elem()
		replyv := reflect.New(replyType)

		// 调用方法。
		function := method.Func
		function.Call([]reflect.Value{svc.rcvr, args.Elem(), replyv})

		// 编码回复。
		rb := new(bytes.Buffer)
		re := labgob.NewEncoder(rb)
		re.EncodeValue(replyv)

		return replyMsg{true, rb.Bytes()}
	} else {
		choices := []string{}
		for k, _ := range svc.methods {
			choices = append(choices, k)
		}
		log.Fatalf("labrpc.Service.dispatch(): unknown method %v in %v; expecting one of %v\n",
			methname, req.svcMeth, choices)
		return replyMsg{false, nil}
	}
}
