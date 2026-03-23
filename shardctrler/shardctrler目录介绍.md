# shardctrler 目录介绍

## 目录概述

`shardctrler` 目录实现了一个分片控制器，负责管理分片到复制组的分配。它是一个基于 Raft 共识算法的服务，用于维护分片分配的配置信息，并在复制组加入、离开或需要手动移动分片时更新配置。

## 文件结构

```
shardctrler/
├── client.go       # 客户端实现，提供与分片控制器交互的接口
├── common.go       # 公共数据结构和常量定义
├── configStateMachine.go  # 配置状态机实现，处理配置变更
├── rpc.go          # RPC 服务初始化
└── server.go       # 服务器实现，处理客户端请求和 Raft 协议
```

## 文件功能详解

### 1. client.go

**功能**：实现分片控制器的客户端，提供与服务器交互的接口。

**主要组件**：
- `Clerk` 结构体：客户端核心结构，维护服务器列表和领导者 ID
- `MakeClerk` 函数：创建新的客户端实例
- `Query` 方法：查询指定配置号的配置信息
- `Join` 方法：添加新的复制组
- `Leave` 方法：删除指定的复制组
- `Move` 方法：手动移动分片到指定复制组
- `Command` 方法：发送命令到服务器并处理响应

**核心流程**：
1. 客户端初始化时设置服务器列表和初始领导者 ID
2. 发送命令时，尝试与当前领导者通信
3. 如果领导者错误或超时，切换到下一个服务器继续尝试
4. 成功收到响应后，更新命令 ID 并返回结果

### 2. common.go

**功能**：定义公共数据结构和常量。

**主要组件**：
- `NShards` 常量：分片数量（10）
- `Config` 结构体：配置信息，包含配置编号、分片分配和复制组信息
- `OperationOp` 枚举：操作类型（Join、Leave、Move、Query）
- `Err` 枚举：错误类型（OK、ErrWrongLeader、ErrTimeout）
- `CommandRequest` 结构体：命令请求，包含不同操作所需的参数
- `CommandResponse` 结构体：命令响应，包含错误信息和配置信息

**核心数据结构**：
- `Config`：描述一组复制组和每个分片的负责复制组
- `CommandRequest`：封装不同类型的操作请求
- `CommandResponse`：封装操作响应结果

### 3. configStateMachine.go

**功能**：实现配置状态机，处理配置变更操作。

**主要组件**：
- `ConfigStateMachine` 接口：定义配置状态机的方法
- `MemoryConfigStateMachine` 结构体：内存中的配置状态机实现
- `Join` 方法：处理添加复制组的操作
- `Leave` 方法：处理删除复制组的操作
- `Move` 方法：处理手动移动分片的操作
- `Query` 方法：查询指定配置号的配置信息
- 辅助函数：`Group2Shards`、`GetGIDWithMinimumShards`、`GetGIDWithMaximumShards`、`deepCopy`

**核心流程**：
1. 对于 Join 操作：添加新复制组并重新平衡分片分配
2. 对于 Leave 操作：删除指定复制组并重新分配其分片
3. 对于 Move 操作：手动移动指定分片到目标复制组
4. 对于 Query 操作：返回指定配置号的配置信息

### 4. rpc.go

**功能**：初始化 RPC 服务，处理网络通信。

**主要组件**：
- `rpcInit` 函数：初始化 RPC 服务，注册 Raft 和 ShardCtrler 服务
- 监听 TCP 连接并处理 RPC 请求

**核心流程**：
1. 注册 Raft 和 ShardCtrler 服务
2. 监听指定端口
3. 接受连接并处理 RPC 请求

### 5. server.go

**功能**：实现分片控制器服务器，处理客户端请求和 Raft 协议。

**主要组件**：
- `ShardCtrler` 结构体：服务器核心结构，包含 Raft 实例、状态机、操作记录和通知通道
- `StartServer` 函数：启动分片控制器服务器
- `applier` 方法：处理 Raft 应用消息，更新状态机
- `applyLogToStateMachine` 方法：将日志应用到状态机
- `Command` 方法：处理客户端命令请求
- 辅助方法：`getNotifyChan`、`removeOutdatedNotifyChan`、`isDuplicateRequest`

**核心流程**：
1. 服务器启动时初始化 Raft 实例和状态机
2. 客户端发送命令时，服务器检查是否为重复请求
3. 如果不是重复请求，将命令提交到 Raft
4. Raft 提交后，applier 协程将命令应用到状态机
5. 服务器通过通知通道将结果返回给客户端

## 核心数据结构

### 1. Config

```go
type Config struct {
    Num    int              // 配置编号
    Shards [NShards]int     // 分片 -> 复制组 ID
    Groups map[int][]string // 复制组 ID -> 服务器列表
}
```

**功能**：描述分片分配的配置信息，包含配置编号、每个分片的负责复制组以及所有复制组的服务器列表。

### 2. CommandRequest

```go
type CommandRequest struct {
    Servers   map[int][]string // Join 操作使用
    GIDs      []int            // Leave 操作使用
    Shard     int              // Move 操作使用
    GID       int              // Move 操作使用
    Num       int              // Query 操作使用
    Op        OperationOp      // 操作类型
    ClientId  int64            // 客户端 ID
    CommandId int64            // 命令 ID
}
```

**功能**：封装客户端请求，根据操作类型包含不同的参数。

### 3. ShardCtrler

```go
type ShardCtrler struct {
    mu              sync.RWMutex
    dead            int32
    rf              *raft.Raft
    applyCh         chan raft.ApplyMsg
    stateMachine     ConfigStateMachine            // 配置状态机
    lastOperations   map[int64]OperationContext    // 记录客户端最后操作
    notifyChans      map[int]chan *CommandResponse // 通知通道
    Lis             net.Listener
}
```

**功能**：分片控制器服务器核心结构，管理 Raft 实例、状态机、操作记录和通知通道。

## 主要功能和流程

### 1. 配置管理

- **Join**：添加新的复制组并重新平衡分片分配
- **Leave**：删除指定复制组并重新分配其分片
- **Move**：手动移动指定分片到目标复制组
- **Query**：查询指定配置号的配置信息

### 2. 分片平衡

当添加或删除复制组时，分片控制器会自动重新平衡分片分配，确保每个复制组的分片数量尽可能均衡。

### 3. 容错处理

- 使用 Raft 共识算法确保配置变更的一致性
- 客户端自动重试机制，当遇到领导者错误或超时时切换到其他服务器
- 重复请求检测，避免重复执行操作

## 与其他模块的交互

### 1. 与 Raft 模块的交互

- 使用 Raft 共识算法确保配置变更的一致性
- 通过 applyCh 接收 Raft 应用消息
- 调用 Raft.Start() 提交新的配置变更

### 2. 与 ShardKV 模块的交互

- ShardKV 模块通过 Query 操作获取最新的分片分配配置
- 当配置变更时，ShardKV 模块根据新配置调整分片分配

## 总结

`shardctrler` 模块是一个基于 Raft 共识算法的分片控制器，负责管理分片到复制组的分配。它提供了 Join、Leave、Move 和 Query 四个核心操作，确保分片分配的一致性和平衡性。通过与 Raft 模块的集成，它能够在分布式环境中提供可靠的配置管理服务，为 ShardKV 模块的分片操作提供基础支持。