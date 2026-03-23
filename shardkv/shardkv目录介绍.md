# shardkv 目录介绍

## 目录概述

`shardkv` 目录实现了一个分片键值存储服务，基于 Raft 共识算法。它由多个复制组组成，每个复制组运行 op-at-a-time paxos。分片控制器（ShardCtrler）决定哪个组服务于哪个分片，并且可能会不时更改分片分配。

## 文件结构

```
shardkv/
├── README.md       # RPC 接口说明
├── client.go       # 客户端实现，提供与分片键值存储交互的接口
├── common.go       # 公共数据结构和常量定义
├── rpc.go          # RPC 服务初始化
├── server.go       # 服务器实现，处理客户端请求和 Raft 协议
├── shard.go        # 分片管理实现
└── test_test.go    # 测试文件
```

## 文件功能详解

### 1. README.md

**功能**：简要说明 RPC 接口。

**主要内容**：
- ShardKV.GetShardsData：获取分片数据
- ShardKV.DeleteShardsData：删除分片数据

### 2. client.go

**功能**：实现分片键值存储的客户端，提供与服务器交互的接口。

**主要组件**：
- `key2shard` 函数：将键转换为分片
- `nrand` 函数：生成随机的 int64 数字
- `Clerk` 结构体：客户端核心结构，维护分片控制器客户端、配置信息、服务器端点创建函数和领导者 ID 映射
- `MakeClerk` 函数：创建新的客户端实例
- `Get` 方法：获取键值
- `Put` 方法：设置键值
- `Append` 方法：追加值到键
- `Command` 方法：发送命令到服务器并处理响应

**核心流程**：
1. 客户端初始化时获取最新的配置信息
2. 发送命令时，根据键计算分片，找到对应的复制组
3. 尝试与复制组的领导者通信
4. 如果领导者错误或超时，切换到复制组内的其他服务器
5. 如果是错误的组，获取最新配置并重试

### 3. common.go

**功能**：定义公共数据结构和常量。

**主要组件**：
- 超时常量：`ExecuteTimeout`、`ConfigureMonitorTimeout`、`MigrationMonitorTimeout`、`GCMonitorTimeout`、`EmptyEntryDetectorTimeout`
- `Err` 枚举：错误类型（OK、ErrNoKey、ErrWrongGroup、ErrWrongLeader、ErrOutDated、ErrTimeout、ErrNotReady）
- `ShardStatus` 枚举：分片状态（Serving、Pulling、BePulling、GCing）
- `OperationContext` 结构体：操作上下文，记录客户端的最大命令 ID 和最后响应
- `Command` 结构体：命令，包含操作类型和数据
- `CommandType` 枚举：命令类型（Operation、Configuration、InsertShards、DeleteShards、EmptyEntry）
- `OperationOp` 枚举：操作类型（OpPut、OpAppend、OpGet）
- `CommandRequest` 结构体：命令请求，包含键、值、操作类型、客户端 ID 和命令 ID
- `CommandResponse` 结构体：命令响应，包含错误信息和值
- `ShardOperationRequest` 结构体：分片操作请求，包含配置编号和分片 ID 列表
- `ShardOperationResponse` 结构体：分片操作响应，包含错误信息、配置编号、分片数据和最后操作

### 4. rpc.go

**功能**：初始化 RPC 服务，处理网络通信。

**主要组件**：
- `rpcInit` 函数：初始化 RPC 服务，注册 Raft 和 ShardKV 服务
- 监听 TCP 连接并处理 RPC 请求

**核心流程**：
1. 注册 Raft 和 ShardKV 服务
2. 监听指定端口
3. 接受连接并处理 RPC 请求

### 5. server.go

**功能**：实现分片键值存储服务器，处理客户端请求和 Raft 协议。

**主要组件**：
- `ShardKV` 结构体：服务器核心结构，包含 Raft 实例、状态机、操作记录和通知通道
- `StartServer` 函数：启动分片键值存储服务器
- `applier` 方法：处理 Raft 应用消息，更新状态机
- `applyOperation` 方法：应用数据操作
- `applyConfiguration` 方法：应用配置操作
- `applyInsertShards` 方法：应用分片插入操作
- `applyDeleteShards` 方法：应用分片删除操作
- `applyEmptyEntry` 方法：应用空日志操作
- `Command` 方法：处理客户端命令请求
- `Execute` 方法：与 Raft 层交互
- `GetShardsData` 方法：获取分片数据（用于分片迁移）
- `DeleteShardsData` 方法：删除分片数据（用于垃圾回收）
- `monitor` 方法：监控并执行各种操作
- `configureAction` 方法：配置更新操作
- `migrationAction` 方法：分片迁移操作
- `gcAction` 方法：垃圾回收操作
- `checkEntryInCurrentTermAction` 方法：检查当前任期的日志条目
- 快照相关方法：`needSnapshot`、`takeSnapshot`、`restoreSnapshot`
- 辅助方法：`canServe`、`isDuplicateRequest`、`getNotifyChan`、`removeOutdatedNotifyChan`

**核心流程**：
1. 服务器启动时初始化 Raft 实例和状态机
2. 启动多个监控协程，处理配置更新、分片迁移、垃圾回收和空日志检测
3. 客户端发送命令时，服务器检查是否为重复请求
4. 如果不是重复请求，将命令提交到 Raft
5. Raft 提交后，applier 协程将命令应用到状态机
6. 服务器通过通知通道将结果返回给客户端
7. 当 Raft 状态超过阈值时，创建快照

### 6. shard.go

**功能**：实现分片管理，包括分片的创建、获取、设置和追加操作。

**主要组件**：
- `Shard` 结构体：分片核心结构，包含键值存储和状态
- `NewShard` 函数：创建新的分片
- `Get` 方法：获取键值
- `Put` 方法：设置键值
- `Append` 方法：追加值到键
- `deepCopy` 方法：深度复制分片数据

## 核心数据结构

### 1. Shard

```go
type Shard struct {
    KV     map[string]string // 键值存储
    Status ShardStatus       // 分片状态
}
```

**功能**：表示一个分片，包含键值存储和状态信息。

### 2. ShardKV

```go
type ShardKV struct {
    mu            sync.RWMutex
    dead          int32
    Rf            *raft.Raft
    applyCh       chan raft.ApplyMsg
    makeEnd       func(string) *labrpc.ClientEnd
    gid           int
    sc            *shardctrler.Clerk
    maxRaftState  int // 快照阈值
    lastApplied   int // 记录最后应用的日志索引
    lastConfig    shardctrler.Config
    currentConfig shardctrler.Config
    stateMachines  map[int]*Shard                // KV 状态机
    lastOperations map[int64]OperationContext    // 记录客户端最后操作
    notifyChans    map[int]chan *CommandResponse // 通知通道
    Lis            net.Listener
}
```

**功能**：分片键值存储服务器核心结构，管理 Raft 实例、状态机、操作记录和通知通道。

### 3. Command

```go
type Command struct {
    Op   CommandType
    Data interface{}
}
```

**功能**：封装不同类型的命令，包含命令类型和数据。

### 4. CommandRequest

```go
type CommandRequest struct {
    Key       string
    Value     string
    Op        OperationOp
    ClientId  int64
    CommandId int64
}
```

**功能**：封装客户端请求，包含键、值、操作类型、客户端 ID 和命令 ID。

## 主要功能和流程

### 1. 数据操作

- **Get**：获取键值
- **Put**：设置键值
- **Append**：追加值到键

### 2. 配置管理

- 监控配置变更
- 应用新配置
- 更新分片状态

### 3. 分片迁移

- 检测需要迁移的分片
- 从源复制组拉取分片数据
- 应用分片数据到本地状态机
- 通知源复制组删除分片数据

### 4. 垃圾回收

- 检测需要删除的分片
- 通知目标复制组删除分片数据
- 清理本地分片状态

### 5. 快照管理

- 当 Raft 状态超过阈值时创建快照
- 恢复快照数据

### 6. 容错处理

- 使用 Raft 共识算法确保数据一致性
- 客户端自动重试机制，当遇到领导者错误或超时时切换到其他服务器
- 重复请求检测，避免重复执行操作

## 与其他模块的交互

### 1. 与 Raft 模块的交互

- 使用 Raft 共识算法确保数据一致性
- 通过 applyCh 接收 Raft 应用消息
- 调用 Raft.Start() 提交新的命令
- 使用 Raft.Snapshot() 创建快照
- 使用 Raft.CondInstallSnapshot() 安装快照

### 2. 与 ShardCtrler 模块的交互

- 通过 ShardCtrler.Query() 获取最新的分片分配配置
- 根据配置变更调整分片分配

## 总结

`shardkv` 模块是一个基于 Raft 共识算法的分片键值存储服务，由多个复制组组成。它提供了 Get、Put 和 Append 三个核心操作，支持分片迁移和垃圾回收。通过与 Raft 模块和 ShardCtrler 模块的集成，它能够在分布式环境中提供可靠的键值存储服务，具有良好的扩展性和容错能力。