package main

import (
	"log"
	"math/rand"
	"net/rpc"
	"time"
)

// Cluster methods related to leader node election. Based on ideas from Raft protocol.
// The leader node issues heartbeats to follower nodes. If the follower node fails enough
// times, the leader node annouces it dead and initiates rehashing: it regenerates ring hash with
// only live nodes and communicates the new list of nodes to followers. They in turn do their
// rehashing using the new list. When the dead node is revived, rehashing happens again.

// Failover config
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

// ClusterPing is content of a leader node ping to a follower node.
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
		log.Printf("cluster: failover disabled; need at least 3 nodes, got %d", len(c.nodes)+1)
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
		leaderPing:         make(chan *ClusterPing, config.VoteAfter),
		electionVote:       make(chan *ClusterVote, len(c.nodes)),
		done:               make(chan bool, 1)}

	log.Println("cluster: failover mode enabled")

	return true
}

// Ping is called by the leader node to assert leadership and check status
// of the followers.
func (c *Cluster) Ping(ping *ClusterPing, unused *bool) error {
	select {
	case c.fo.leaderPing <- ping:
	default:
	}
	return nil
}

// Vote processes request for a vote from a candidate.
func (c *Cluster) Vote(vreq *ClusterVoteRequest, response *ClusterVoteResponse) error {
	respChan := make(chan ClusterVoteResponse, 1)

	c.fo.electionVote <- &ClusterVote{
		req:  vreq,
		resp: respChan}

	*response = <-respChan

	return nil
}

func (c *Cluster) sendPings() {
	rehash := false

	for _, node := range c.nodes {
		unused := false
		err := node.call("Cluster.Ping", &ClusterPing{
			Leader:    c.thisNodeName,
			Term:      c.fo.term,
			Signature: c.ring.Signature(),
			Nodes:     c.fo.activeNodes}, &unused)

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
		var activeNodes []string
		for _, node := range c.nodes {
			if node.failCount < c.fo.nodeFailCountLimit {
				activeNodes = append(activeNodes, node.name)
			}
		}
		activeNodes = append(activeNodes, c.thisNodeName)

		c.fo.activeNodes = activeNodes
		c.rehash(activeNodes)
		c.invalidateRemoteSubs()

		log.Println("cluster: initiating failover rehash for nodes", activeNodes)
		globals.hub.rehash <- true
	}
}

func (c *Cluster) electLeader() {
	// Increment the term (voting for myself in this term) and clear the leader
	c.fo.term++
	c.fo.leader = ""

	// Make sure the current node does not report itself as a leader.
	statsSet("ClusterLeader", 0)

	log.Println("cluster: leading new election for term", c.fo.term)

	nodeCount := len(c.nodes)
	// Number of votes needed to elect the leader
	expectVotes := (nodeCount+1)>>1 + 1
	done := make(chan *rpc.Call, nodeCount)

	// Send async requests for votes to other nodes
	for _, node := range c.nodes {
		response := ClusterVoteResponse{}
		node.callAsync("Cluster.Vote", &ClusterVoteRequest{
			Node: c.thisNodeName,
			Term: c.fo.term}, &response, done)
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
		log.Printf("'%s' elected self as a new leader", c.thisNodeName)
	}
}

// Go routine that processes calls related to leader election and maintenance.
func (c *Cluster) run() {

	ticker := time.NewTicker(c.fo.heartBeat)

	// Count of missed pings from the leader.
	missed := 0
	// Don't rehash immediately on the first ping. If this node just came online, leader will
	// account it on the next ping. Otherwise it will be rehashing twice.
	rehashSkipped := false

	for {
		select {
		case <-ticker.C:
			if c.fo.leader == c.thisNodeName {
				// I'm the leader, send pings
				c.sendPings()
			} else {
				// Increment the number of missed pings from the leader.
				// The counter will be reset to zero when the ping is received.
				missed++
				if missed >= c.fo.voteTimeout {
					// Leader is gone, initiate election of a new leader.
					missed = 0
					c.electLeader()
				}
			}
		case ping := <-c.fo.leaderPing:
			// Ping from a leader.

			if ping.Term < c.fo.term {
				// This is a ping from a stale leader. Ignore.
				log.Println("cluster: ping from a stale leader", ping.Term, c.fo.term, ping.Leader, c.fo.leader)
				continue
			}

			if ping.Term > c.fo.term {
				c.fo.term = ping.Term
				c.fo.leader = ping.Leader
				log.Printf("cluster: leader '%s' elected", c.fo.leader)
			} else if ping.Leader != c.fo.leader {
				if c.fo.leader != "" {
					// Wrong leader. It's a bug, should never happen!
					log.Printf("cluster: wrong leader '%s' while expecting '%s'; term %d",
						ping.Leader, c.fo.leader, ping.Term)
				} else {
					log.Printf("cluster: leader set to '%s'", ping.Leader)
				}
				c.fo.leader = ping.Leader
			}

			// This ping is from a leader, consequently this node is not the leader.
			statsSet("ClusterLeader", 0)

			missed = 0
			if ping.Signature != c.ring.Signature() {
				if rehashSkipped {
					log.Println("cluster: rehashing at a request of",
						ping.Leader, ping.Nodes, ping.Signature, c.ring.Signature())
					c.rehash(ping.Nodes)
					c.invalidateRemoteSubs()
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
				log.Printf("Voting YES for %s, my term %d, vote term %d", vreq.req.Node, c.fo.term, vreq.req.Term)
				c.fo.term = vreq.req.Term
				c.fo.leader = ""
				// Election means these is no leader yet.
				statsSet("ClusterLeader", 0)
				vreq.resp <- ClusterVoteResponse{Result: true, Term: c.fo.term}
			} else {
				// This node has voted already or stale election, reject.
				log.Printf("Voting NO for %s, my term %d, vote term %d", vreq.req.Node, c.fo.term, vreq.req.Term)
				vreq.resp <- ClusterVoteResponse{Result: false, Term: c.fo.term}
			}
		case <-c.fo.done:
			return
		}
	}
}
