package raft

import (
	"go.uber.org/zap"
	"github.com/effortful-lion/Raft-side/utils"
)

// sendRequestVote rpc调用
func (rf *Raft) sendRequestVote(server int, request *RequestVoteRequest, response *RequestVoteResponse) bool {

	zap.S().Info(utils.GetCurrentFunctionName())

	if rf.peers[server].Rpc == nil {
		tmp := utils.MakeEnd(rf.peers[server].Endname)
		if tmp == nil {
			return false
		}
		rf.peers[server].Rpc = tmp.Rpc
	}
	err := rf.peers[server].Rpc.Call("Raft.RequestVote", request, response)
	if err != nil {
		zap.S().Info(zap.Any("func", utils.GetCurrentFunctionName()), zap.Any("response", err))
		return false
	}
	zap.S().Info(zap.Any("func", utils.GetCurrentFunctionName()), zap.Any("response", response))
	return true
}

// sendAppendEntries rpc调用
func (rf *Raft) sendAppendEntries(server int, request *AppendEntriesRequest, response *AppendEntriesResponse) bool {

	zap.S().Info(utils.GetCurrentFunctionName())

	if rf.peers[server].Rpc == nil {
		tmp := utils.MakeEnd(rf.peers[server].Endname)
		if tmp == nil {
			return false
		}
		rf.peers[server].Rpc = tmp.Rpc
	}

	if rf.peers[server].Rpc == nil {
		return false
	}

	err := rf.peers[server].Rpc.Call("Raft.AppendEntries", request, response)

	if err != nil {
		return false
	}
	return true
}

// sendInstallSnapshot rpc调用
func (rf *Raft) sendInstallSnapshot(server int, request *InstallSnapshotRequest, response *InstallSnapshotResponse) bool {

	zap.S().Info(utils.GetCurrentFunctionName())

	if rf.peers[server].Rpc == nil {
		tmp := utils.MakeEnd(rf.peers[server].Endname)
		if tmp == nil {
			return false
		}
		rf.peers[server].Rpc = tmp.Rpc
	}

	if rf.peers[server].Rpc == nil {
		return false
	}

	err := rf.peers[server].Rpc.Call("Raft.InstallSnapshot", request, response)

	if err != nil {
		return false
	}
	return true
}
