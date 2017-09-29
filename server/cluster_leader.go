package main

import (
	"log"
	"math/rand"
	"net/rpc"
	"time"
)

// Cluster methods related to leader node election. Based on ideas from Raft protocol.
// The leader node manages failover rehashing.

// Failover config
type ClusterFailover struct {
	// Current leader
	leader string
	// Current election term
	term int
	// Hearbeat interval
	heartBeat time.Duration
	// Vote timeout: the number of missed heartbeats before a new election is initiated.
	voteTimeout int

	// The list of nodes the leader considers active
	activeNodes []string
	// The number of heartbeats a node can fail before being declared dead
	nodeFailCountLimit int

	// Channel for processing leader pings
	leaderPing chan *ClusterPing
	// Channel for processing election votes
	electionVote chan *ClusterVote
	// Channel for stopping the failover runner
	done chan bool
}

type ClusterFailoverConfig struct {
	// Failover is enabled
	Enabled bool `json:"enabled"`
	// Time in milliseconds between heartbeats
	Heartbeat int `json:"heartbeat"`
	// Number of failed heartbeats before a leader election is initiated.
	VoteAfter int `json:"vote_after"`
	// Number of failures before a node is considered dead
	NodeFailAfter int `json:"node_fail_after"`
}

// Content of a leader node ping to a follower node
type ClusterPing struct {
	// Name of the leader node
	Leader string
	// Election term
	Term int
	// Ring hash signature that represents the cluster
	Signature string
	// Names of nodes currently active in the cluster
	Nodes []string
}

type ClusterVoteRequest struct {
	// Candidate node which issued this request
	Node string
	// Election term
	Term int
}

type ClusterVote struct {
	req  *ClusterVoteRequest
	resp chan bool
}

func (c *Cluster) failoverInit(config *ClusterFailoverConfig) {
	if config == nil || !config.Enabled {
		return
	}
	c.fo = &ClusterFailover{
		heartBeat:          time.Duration(config.Heartbeat) * time.Millisecond,
		voteTimeout:        config.VoteAfter,
		nodeFailCountLimit: config.NodeFailAfter,
		leaderPing:         make(chan *ClusterPing, config.VoteAfter),
		electionVote:       make(chan *ClusterVote, len(c.nodes)),
		done:               make(chan bool, 1)}

	go c.run()
}

// Leader node calls this method to assert leadership and check status
// of the followers.
func (c *Cluster) Ping(ping *ClusterPing, unused *bool) error {
	select {
	case c.fo.leaderPing <- ping:
	default:
	}
	return nil
}

// Process request for a vote from a candidate.
func (c *Cluster) Vote(vreq *ClusterVoteRequest, response *bool) error {
	respChan := make(chan bool, 1)

	c.fo.electionVote <- &ClusterVote{
		req:  vreq,
		resp: respChan}

	*response = <-respChan

	return nil
}

func (c *Cluster) sendPings() {
	var response bool
	var rehash bool

	for _, node := range c.nodes {
		err := node.endpoint.Call("Cluster.Ping", &ClusterPing{
			Leader:    c.thisNodeName,
			Term:      c.fo.term,
			Signature: c.ring.Signature(),
			Nodes:     c.fo.activeNodes}, &response)

		if err != nil {
			node.failCount++
			if node.failCount >= c.fo.nodeFailCountLimit {
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
		// TODO rehash
	}
}

func (c *Cluster) electLeader() {
	// Increment the term and clear the leader
	c.fo.term++
	c.fo.leader = ""

	nodeCount := len(c.nodes)
	// Number of votes needed to elect the leader
	expectVotes := (nodeCount+1)>>1 + 1
	done := make(chan *rpc.Call, nodeCount)

	// Send async requests for votes to other nodes
	for _, node := range c.nodes {
		response := false
		node.endpoint.Go("Cluster.Vote", &ClusterVoteRequest{
			Node: c.thisNodeName,
			Term: c.fo.term}, &response, done)
	}

	// Number of votes received (1 vote for self)
	voteCount := 1
	timeout := time.NewTimer(c.fo.heartBeat>>1 + c.fo.heartBeat)
	// Wait for one of the following
	// 1. More than half of the nodes voting in favor
	// 2. Timeout.
	for i := 0; i < nodeCount && voteCount < expectVotes; {
		select {
		case call := <-done:
			if call.Error == nil && *call.Reply.(*bool) {
				voteCount++
			}
			i++
		case <-timeout.C:
			// break the loop
			i = nodeCount
		}
	}

	if voteCount >= expectVotes {
		// Current node elected as the leader
		c.fo.leader = c.thisNodeName
	}
}

// Go routine that processes calls related to leader election and maintenance.
func (c *Cluster) run() {
	// Heartbeat ticker
	rand.Seed(time.Now().UnixNano())

	// Random ticker 0.75 * heartBeat + random(0, 0.5 * heartBeat)
	ticker := time.NewTicker((c.fo.heartBeat >> 1) + (c.fo.heartBeat >> 2) +
		time.Duration(rand.Intn(int(c.fo.heartBeat>>1))))
	missed := 0
	for {
		select {
		case <-ticker.C:
			if c.fo.leader == c.thisNodeName {
				// I'm the leader, send pings
				c.sendPings()
			} else {
				missed++
				if missed >= c.fo.voteTimeout {
					// Elect the leader
					c.electLeader()
				}
			}
		case ping := <-c.fo.leaderPing:
			// Ping from a leader.

			if ping.Term < c.fo.term {
				// This is a ping from a stale leader. Ignore.
				continue
			}

			if ping.Term > c.fo.term {
				c.fo.term = ping.Term
				c.fo.leader = ping.Leader
			} else if ping.Leader != c.fo.leader {
				// Wrong leader. It's a bug, should never happen!
				log.Printf("cluster: wrong leader '%s' while expecting '%s'; term %d",
					ping.Leader, c.fo.leader, ping.Term)
				c.fo.leader = ping.Leader
			}

			missed = 0
			if ping.Signature != c.ring.Signature() {
				log.Println("cluster: leader requested rehashing")
			}
		case vreq := <-c.fo.electionVote:
			if c.fo.term < vreq.req.Term {
				// This is a new election. This node has not voted yet. Vote for the requestor.
				c.fo.term = vreq.req.Term
				vreq.resp <- true
			} else {
				// This node has voted already, reject.
				vreq.resp <- false
			}
		case <-c.fo.done:
			return
		}
	}
}
