package raft

import (
	"go.uber.org/zap"
	"github.com/effortful-lion/Raft-side/utils"
)

// RequestVote 处理candidate的拉票请求
func (rf *Raft) RequestVote(request *RequestVoteRequest, response *RequestVoteResponse) (err error) {
	zap.S().Info(utils.GetCurrentFunctionName())
	rf.mu.Lock()
	defer rf.mu.Unlock()
	defer rf.persist()
	// term不合法情况
	if request.Term < rf.currentTerm || (request.Term == rf.currentTerm && rf.votedFor != -1 && rf.votedFor != request.CandidateId) {
		response.Term, response.VoteGranted = rf.currentTerm, false
		return
	}

	if request.Term > rf.currentTerm {
		rf.ChangeState(StateFollower)
		rf.currentTerm, rf.votedFor = request.Term, -1
	}
	// 检查日志是否最新
	if !rf.isLogUpToDate(request.LastLogTerm, request.LastLogIndex) {
		response.Term, response.VoteGranted = rf.currentTerm, false
		return
	}
	rf.votedFor = request.CandidateId
	rf.electionTimer.Reset(RandomizedElectionTimeout())
	response.Term, response.VoteGranted = rf.currentTerm, true

	return err
}
