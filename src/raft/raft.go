package raft

import (
	//	"bytes"

	"sync"
	"time"

	"raft/labrpc"
)

type Raft struct {
	mu        sync.RWMutex        // Lock to protect shared access to this peer's state
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      int32               // set by Kill()

	applyCh       chan ApplyMsg
	applyCond     *sync.Cond   // used to wakeup applier goroutine after committing new entries
	tryAppendCond []*sync.Cond // used to signal replicator goroutine to batch replicating entries
	state         int

	currentTerm int
	votedFor    int
	raftLog     *raftLog // the first entry is a dummy entry which contains LastSnapshotTerm, LastSnapshotIndex and nil Command

	commitIndex int
	lastApplied int
	nextIndex   []int
	matchIndex  []int

	electionTimer  *time.Timer
	heartbeatTimer *time.Timer
}

func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	rf := &Raft{
		peers:          peers,
		persister:      persister,
		me:             me,
		dead:           0,
		applyCh:        applyCh,
		tryAppendCond:  make([]*sync.Cond, len(peers)),
		state:          StateFollower,
		currentTerm:    0,
		votedFor:       -1,
		raftLog:        newLogs(),
		nextIndex:      make([]int, len(peers)),
		matchIndex:     make([]int, len(peers)),
		heartbeatTimer: time.NewTimer(StableHeartbeatTimeout()),
		electionTimer:  time.NewTimer(RandomizedElectionTimeout()),
	}
	rf.readPersist(persister.ReadRaftState())
	rf.applyCond = sync.NewCond(&rf.mu)

	for i := 0; i < len(peers); i++ {
		if i != rf.me {
			rf.tryAppendCond[i] = sync.NewCond(&sync.Mutex{})
			// start a peer's replicator goroutine to replicate entries in the background
			go rf.appendThread(i)
		}
	}
	// start ticker goroutine to start elections
	go rf.ticker()
	// start applier goroutine to push committed logs into applyCh exactly once
	go rf.applier()
	return rf
}

//receive appending command from upper KV layer
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.state != StateLeader {
		return -1, -1, false
	}
	newLog := Entry{}
	newLog.Command = command
	newLog.Index = rf.raftLog.lastIndex() + 1
	newLog.Term = rf.currentTerm
	rf.raftLog.append(newLog)
	rf.persist()
	DPrintf("{Node %v} receives a new command[%v] to replicate in term %v", rf.me, newLog, rf.currentTerm)
	rf.BroadcastAppend(Append)
	return newLog.Index, newLog.Term, true
}

func (rf *Raft) ticker() {
	for !rf.killed() {
		select {
		case <-rf.electionTimer.C:
			rf.electionTimer.Reset(RandomizedElectionTimeout())
			rf.mu.Lock()
			if rf.state != StateLeader {
				rf.StartElection()
			}
			rf.mu.Unlock()
		case <-rf.heartbeatTimer.C:
			rf.heartbeatTimer.Reset(StableHeartbeatTimeout())
			rf.mu.Lock()
			if rf.state == StateLeader {
				go rf.BroadcastAppend(HeartBeat)
			}
			rf.mu.Unlock()
		}
	}
}

func (rf *Raft) needAppend(peer int) bool {
	rf.mu.RLock()
	defer rf.mu.RUnlock()
	ret := rf.state == StateLeader && rf.matchIndex[peer] < rf.raftLog.lastIndex()
	return ret
}

func (rf *Raft) appendThread(peer int) {
	rf.tryAppendCond[peer].L.Lock()
	defer rf.tryAppendCond[peer].L.Unlock()
	for !rf.killed() {
		// we might recevied N Appending request, but we don't need
		// to do len(peers)*N RPC, because first few RPCs might push
		// all the new entry from logs to other replica, then needReplicating
		// will be false
		for !rf.needAppend(peer) {
			rf.tryAppendCond[peer].Wait()
			if rf.killed() {
				return
			}
		}
		rf.appendOneRound(peer)
	}
}

// a dedicated applier goroutine to guarantee that each log will be push into applyCh exactly once, ensuring that service's applying entries and raft's committing entries can be parallel
func (rf *Raft) applier() {
	for !rf.killed() {
		rf.mu.Lock()
		// if there is no need to apply entries, just release CPU and wait other goroutine's signal if they commit new entries
		for rf.lastApplied >= rf.commitIndex {
			rf.applyCond.Wait()
			if rf.killed() {
				rf.mu.Unlock()
				return
			}
		}
		commitIndex, lastApplied := rf.commitIndex, rf.lastApplied
		entries := make([]Entry, commitIndex-lastApplied)
		copy(entries, rf.raftLog.slice(lastApplied+1, commitIndex+1))
		rf.mu.Unlock()
		for _, entry := range entries {
			rf.applyCh <- ApplyMsg{
				CommandValid: true,
				Command:      entry.Command,
				CommandTerm:  entry.Term,
				CommandIndex: entry.Index,
			}
		}
		rf.mu.Lock()
		DPrintf("{Node %v} applies entries %v-%v in term %v", rf.me, rf.lastApplied, commitIndex, rf.currentTerm)
		// use commitIndex rather than rf.commitIndex because rf.commitIndex may change during the Unlock() and Lock()
		// use Max(rf.lastApplied, commitIndex) rather than commitIndex directly to avoid concurrently InstallSnapshot rpc causing lastApplied to rollback
		rf.lastApplied = Max(rf.lastApplied, commitIndex)
		rf.mu.Unlock()
	}
}

//
// A service wants to switch to snapshot.  Only do so if Raft hasn't
// have more recent info since it communicate the snapshot on applyCh.
//
func (rf *Raft) CondInstallSnapshot(lastIncludedTerm int, lastIncludedIndex int, snapshot []byte) bool {

	// Your code here (2D).

	return true
}

// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	// Your code here (2D).

}
