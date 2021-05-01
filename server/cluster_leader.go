package main

import (
	"math/rand"
	"net/rpc"
	"sync"
	"time"

	"github.com/tinode/chat/server/logs"
)

// Cluster methods related to leader node election. Based on ideas from Raft protocol.
// The leader node issues heartbeats to follower nodes. If the follower node fails enough
// times, the leader node annouces it dead and initiates rehashing: it regenerates ring hash with
// only live nodes and communicates the new list of nodes to followers. They in turn do their
// rehashing using the new list. When the dead node is revived, rehashing happens again.

// Failover config.
type clusterFailover struct {
	// Current leader
	leader string
	// Current election term
	term int
	// Hearbeat interval
	heartBeat time.Duration
	// Vote timeout: the number of missed heartbeats before a new election is initiated.
	voteTimeout int

	// The list of nodes the leader considers active
	activeNodes     []string
	activeNodesLock sync.RWMutex
	// The number of heartbeats a node can fail before being declared dead
	nodeFailCountLimit int

	// Channel for processing leader health checks.
	healthCheck chan *ClusterHealth
	// Channel for processing election votes.
	electionVote chan *ClusterVote
	// Channel for stopping the failover runner.
	done chan bool
}

type clusterFailoverConfig struct {
	// Failover is enabled
	Enabled bool `json:"enabled"`
	// Time in milliseconds between heartbeats
	Heartbeat int `json:"heartbeat"`
	// Number of failed heartbeats before a leader election is initiated.
	VoteAfter int `json:"vote_after"`
	// Number of failures before a node is considered dead
	NodeFailAfter int `json:"node_fail_after"`
}

// ClusterHealth is content of a leader's health check of a follower node.
type ClusterHealth struct {
	// Name of the leader node
	Leader string
	// Election term
	Term int
	// Ring hash signature that represents the cluster
	Signature string
	// Names of nodes currently active in the cluster
	Nodes []string
}

// ClusterVoteRequest is a request from a leader candidate to a node to vote for the candidate.
type ClusterVoteRequest struct {
	// Candidate node which issued this request
	Node string
	// Election term
	Term int
}

// ClusterVoteResponse is a vote from a node.
type ClusterVoteResponse struct {
	// Actual vote
	Result bool
	// Node's term after the vote
	Term int
}

// ClusterVote is a vote request and a response in leader election.
type ClusterVote struct {
	req  *ClusterVoteRequest
	resp chan ClusterVoteResponse
}

func (c *Cluster) failoverInit(config *clusterFailoverConfig) bool {
	if config == nil || !config.Enabled {
		return false
	}
	if len(c.nodes) < 2 {
		logs.Err.Printf("cluster: failover disabled; need at least 3 nodes, got %d", len(c.nodes)+1)
		return false
	}

	// Generate ring hash on the assumption that all nodes are alive and well.
	// This minimizes rehashing during normal operations.
	var activeNodes []string
	for _, node := range c.nodes {
		activeNodes = append(activeNodes, node.name)
	}
	activeNodes = append(activeNodes, c.thisNodeName)
	c.rehash(activeNodes)

	// Random heartbeat ticker: 0.75 * config.HeartBeat + random(0, 0.5 * config.HeartBeat)
	rand.Seed(time.Now().UnixNano())
	hb := time.Duration(config.Heartbeat) * time.Millisecond
	hb = (hb >> 1) + (hb >> 2) + time.Duration(rand.Intn(int(hb>>1)))

	c.fo = &clusterFailover{
		activeNodes:        activeNodes,
		heartBeat:          hb,
		voteTimeout:        config.VoteAfter,
		nodeFailCountLimit: config.NodeFailAfter,
		healthCheck:        make(chan *ClusterHealth, config.VoteAfter),
		electionVote:       make(chan *ClusterVote, len(c.nodes)),
		done:               make(chan bool, 1),
	}

	logs.Info.Println("cluster: failover mode enabled")

	return true
}

// Health is called by the leader node to assert leadership and check status
// of the followers.
func (c *Cluster) Health(health *ClusterHealth, unused *bool) error {
	select {
	case c.fo.healthCheck <- health:
	default:
	}
	return nil
}

// Vote processes request for a vote from a candidate.
func (c *Cluster) Vote(vreq *ClusterVoteRequest, response *ClusterVoteResponse) error {
	respChan := make(chan ClusterVoteResponse, 1)

	c.fo.electionVote <- &ClusterVote{
		req:  vreq,
		resp: respChan,
	}

	*response = <-respChan

	return nil
}

// Cluster leader checks health of follower nodes.
func (c *Cluster) sendHealthChecks() {
	rehash := false

	for _, node := range c.nodes {
		unused := false
		err := node.call("Cluster.Health",
			&ClusterHealth{
				Leader:    c.thisNodeName,
				Term:      c.fo.term,
				Signature: c.ring.Signature(),
				Nodes:     c.fo.activeNodes,
			}, &unused)

		if err != nil {
			node.failCount++
			if node.failCount == c.fo.nodeFailCountLimit {
				// Node failed too many times
				rehash = true
			}
		} else {
			if node.failCount >= c.fo.nodeFailCountLimit {
				// Node has recovered
				rehash = true
			}
			node.failCount = 0
		}
	}

	if rehash {
		activeNodes := []string{c.thisNodeName}
		for _, node := range c.nodes {
			if node.failCount < c.fo.nodeFailCountLimit {
				activeNodes = append(activeNodes, node.name)
			}
		}
		c.fo.activeNodesLock.Lock()
		c.fo.activeNodes = activeNodes
		c.fo.activeNodesLock.Unlock()
		c.rehash(activeNodes)
		c.invalidateProxySubs("")
		c.gcProxySessions(activeNodes)

		logs.Info.Println("cluster: initiating failover rehash for nodes", activeNodes)
		globals.hub.rehash <- true
	}
}

func (c *Cluster) electLeader() {
	// Increment the term (voting for myself in this term) and clear the leader
	c.fo.term++
	c.fo.leader = ""

	// Make sure the current node does not report itself as a leader.
	statsSet("ClusterLeader", 0)

	logs.Info.Println("cluster: leading new election for term", c.fo.term)

	nodeCount := len(c.nodes)
	// Number of votes needed to elect the leader
	expectVotes := (nodeCount+1)>>1 + 1
	done := make(chan *rpc.Call, nodeCount)

	// Send async requests for votes to other nodes
	for _, node := range c.nodes {
		response := ClusterVoteResponse{}
		node.callAsync("Cluster.Vote",
			&ClusterVoteRequest{
				Node: c.thisNodeName,
				Term: c.fo.term,
			}, &response, done)
	}

	// Number of votes received (1 vote for self)
	voteCount := 1
	timeout := time.NewTimer(c.fo.heartBeat>>1 + c.fo.heartBeat)
	// Wait for one of the following
	// 1. More than half of the nodes voting in favor
	// 2. All nodes responded.
	// 3. Timeout.
	for i := 0; i < nodeCount && voteCount < expectVotes; {
		select {
		case call := <-done:
			if call.Error == nil {
				if call.Reply.(*ClusterVoteResponse).Result {
					// Vote in my favor
					voteCount++
				} else if c.fo.term < call.Reply.(*ClusterVoteResponse).Term {
					// Vote against me. Abandon vote: this node's term is behind the cluster
					i = nodeCount
					voteCount = 0
				}
			}

			i++
		case <-timeout.C:
			// break the loop
			i = nodeCount
		}
	}

	if voteCount >= expectVotes {
		// Current node elected as the leader.
		c.fo.leader = c.thisNodeName
		statsSet("ClusterLeader", 1)
		logs.Info.Printf("'%s' elected self as a new leader", c.thisNodeName)
	}
}

// Go routine that processes calls related to leader election and maintenance.
func (c *Cluster) run() {
	ticker := time.NewTicker(c.fo.heartBeat)

	// Count of missed health checks from the leader.
	missed := 0
	// Don't rehash immediately on the first missed health check. If this node just came online, leader will
	// account it on the next check. Otherwise it will be rehashing twice.
	rehashSkipped := false

	for {
		select {
		case <-ticker.C:
			if c.fo.leader == c.thisNodeName {
				// I'm the leader, send the health checks to followers.
				c.sendHealthChecks()
			} else {
				// Increment the number of missed health checks from the leader.
				// The counter will be reset to zero when a health check is received.
				missed++
				if missed >= c.fo.voteTimeout {
					// Leader is gone, initiate election of a new leader.
					missed = 0
					c.electLeader()
				}
			}
		case health := <-c.fo.healthCheck:
			// Health check from the leader.

			if health.Term < c.fo.term {
				// This is a health check from a stale leader. Ignore.
				logs.Warn.Println("cluster: health check from a stale leader", health.Term, c.fo.term, health.Leader, c.fo.leader)
				continue
			}

			if health.Term > c.fo.term {
				c.fo.term = health.Term
				c.fo.leader = health.Leader
				logs.Info.Printf("cluster: leader '%s' elected", c.fo.leader)
			} else if health.Leader != c.fo.leader {
				if c.fo.leader != "" {
					// Wrong leader. It's a bug, should never happen!
					logs.Err.Printf("cluster: wrong leader '%s' while expecting '%s'; term %d",
						health.Leader, c.fo.leader, health.Term)
				} else {
					logs.Info.Printf("cluster: leader set to '%s'", health.Leader)
				}
				c.fo.leader = health.Leader
			}

			// This is a health check from a leader, consequently this node is not the leader.
			statsSet("ClusterLeader", 0)

			missed = 0
			if health.Signature != c.ring.Signature() {
				if rehashSkipped {
					logs.Info.Println("cluster: rehashing at a request of",
						health.Leader, health.Nodes, health.Signature, c.ring.Signature())
					c.rehash(health.Nodes)
					c.invalidateProxySubs("")
					c.gcProxySessions(health.Nodes)
					rehashSkipped = false

					globals.hub.rehash <- true
				} else {
					rehashSkipped = true
				}
			}

		case vreq := <-c.fo.electionVote:
			if c.fo.term < vreq.req.Term {
				// This is a new election. This node has not voted yet. Vote for the requestor and
				// clear the current leader.
				logs.Info.Printf("Voting YES for %s, my term %d, vote term %d", vreq.req.Node, c.fo.term, vreq.req.Term)
				c.fo.term = vreq.req.Term
				c.fo.leader = ""
				// Election means these is no leader yet.
				statsSet("ClusterLeader", 0)
				vreq.resp <- ClusterVoteResponse{Result: true, Term: c.fo.term}
			} else {
				// This node has voted already or stale election, reject.
				logs.Info.Printf("Voting NO for %s, my term %d, vote term %d", vreq.req.Node, c.fo.term, vreq.req.Term)
				vreq.resp <- ClusterVoteResponse{Result: false, Term: c.fo.term}
			}
		case <-c.fo.done:
			return
		}
	}
}
