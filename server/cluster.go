package main

import (
	"encoding/gob"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/rpc"
	"sort"
	"sync"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/push"
	rh "github.com/tinode/chat/server/ringhash"
	"github.com/tinode/chat/server/store/types"
)

const (
	// Default timeout before attempting to reconnect to a node
	defaultClusterReconnect = 200 * time.Millisecond
	// Number of replicas in ringhash
	clusterHashReplicas = 20
)

type clusterNodeConfig struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
}

type clusterConfig struct {
	// List of all members of the cluster, including this member
	Nodes []clusterNodeConfig `json:"nodes"`
	// Name of this cluster node
	ThisName string `json:"self"`
	// Failover configuration
	Failover *clusterFailoverConfig
}

// ClusterNode is a client's connection to another node.
type ClusterNode struct {
	lock sync.Mutex

	// RPC endpoint
	endpoint *rpc.Client
	// True if the endpoint is believed to be connected
	connected bool
	// True if a go routine is trying to reconnect the node
	reconnecting bool
	// TCP address in the form host:port
	address string
	// Name of the node
	name string
	// Fingerprint of the node: unique value which changes when the node restarts.
	fingerprint int64

	// A number of times this node has failed in a row
	failCount int

	// Channel for shutting down the runner; buffered, 1
	done chan bool
}

// ClusterSess is a basic info on a remote session where the message was created.
type ClusterSess struct {
	// IP address of the client. For long polling this is the IP of the last poll
	RemoteAddr string

	// User agent, a string provived by an authenticated client in {login} packet
	UserAgent string

	// ID of the current user or 0
	Uid types.Uid

	// User's authentication level
	AuthLvl auth.Level

	// Protocol version of the client: ((major & 0xff) << 8) | (minor & 0xff)
	Ver int

	// Human language of the client
	Lang string

	// Device ID
	DeviceID string

	// Device platform: "web", "ios", "android"
	Platform string

	// Session ID
	Sid string
}

// ClusterReq is either a Proxy to Master or intra-cluster routing request message.
type ClusterReq struct {
	// Name of the node sending this request
	Node string

	// Ring hash signature of the node sending this request
	// Signature must match the signature of the receiver, otherwise the
	// Cluster is desynchronized.
	Signature string

	// Fingerprint of the node sending this request.
	// Fingerprint changes when the node is restarted.
	Fingerprint int64

	// Client message. Set for C2S requests.
	CliMsg *ClientComMessage
	// Message to be routed. Set for route requests.
	SrvMsg *ServerComMessage

	// Root user may send messages on behalf of other users.
	OnBehalfOf string
	// AuthLevel of the user specified by root.
	AuthLvl int

	// Expanded (routable) topic name
	RcptTo string
	// Originating session
	Sess *ClusterSess
	// True if the original session has disconnected
	SessGone bool
	// For {pres} messages, indicates whether to break reply loop.
	PresWantReply bool
}

// ClusterResp is a Master to Proxy response message.
type ClusterResp struct {
	Msg []byte
	// Session ID to forward message to, if any.
	FromSID string
}

// Handle outbound node communication: read messages from the channel, forward to remote nodes.
// FIXME(gene): this will drain the outbound queue in case of a failure: all unprocessed messages will be dropped.
// Maybe it's a good thing, maybe not.
func (n *ClusterNode) reconnect() {
	var reconnTicker *time.Ticker

	// Avoid parallel reconnection threads
	n.lock.Lock()
	if n.reconnecting {
		n.lock.Unlock()
		return
	}
	n.reconnecting = true
	n.lock.Unlock()

	var count = 0
	var err error
	for {
		// Attempt to reconnect right away
		if n.endpoint, err = rpc.Dial("tcp", n.address); err == nil {
			if reconnTicker != nil {
				reconnTicker.Stop()
			}
			n.lock.Lock()
			n.connected = true
			n.reconnecting = false
			n.lock.Unlock()
			statsInc("LiveClusterNodes", 1)
			log.Printf("cluster: connection to '%s' established", n.name)
			return
		} else if count == 0 {
			reconnTicker = time.NewTicker(defaultClusterReconnect)
		}

		count++

		select {
		case <-reconnTicker.C:
			// Wait for timer to try to reconnect again. Do nothing if the timer is inactive.
		case <-n.done:
			// Shutting down
			log.Printf("cluster: node '%s' shutdown started", n.name)
			reconnTicker.Stop()
			if n.endpoint != nil {
				n.endpoint.Close()
			}
			n.lock.Lock()
			n.connected = false
			n.reconnecting = false
			n.lock.Unlock()
			log.Printf("cluster: node '%s' shut down completed", n.name)
			return
		}
	}
}

func (n *ClusterNode) call(proc string, msg, resp interface{}) error {
	if !n.connected {
		return errors.New("cluster: node '" + n.name + "' not connected")
	}

	if err := n.endpoint.Call(proc, msg, resp); err != nil {
		log.Printf("cluster: call failed to '%s' [%s]", n.name, err)

		n.lock.Lock()
		if n.connected {
			n.endpoint.Close()
			n.connected = false
			statsInc("LiveClusterNodes", -1)
			go n.reconnect()
		}
		n.lock.Unlock()
		return err
	}

	return nil
}

func (n *ClusterNode) callAsync(proc string, msg, resp interface{}, done chan *rpc.Call) *rpc.Call {
	if done != nil && cap(done) == 0 {
		log.Panic("cluster: RPC done channel is unbuffered")
	}

	if !n.connected {
		call := &rpc.Call{
			ServiceMethod: proc,
			Args:          msg,
			Reply:         resp,
			Error:         errors.New("cluster: node '" + n.name + "' not connected"),
			Done:          done,
		}
		if done != nil {
			done <- call
		}
		return call
	}

	myDone := make(chan *rpc.Call, 1)
	go func() {
		call := <-myDone
		if call.Error != nil {
			n.lock.Lock()
			if n.connected {
				n.endpoint.Close()
				n.connected = false
				statsInc("LiveClusterNodes", -1)
				go n.reconnect()
			}
			n.lock.Unlock()
		}

		if done != nil {
			done <- call
		}
	}()

	call := n.endpoint.Go(proc, msg, resp, myDone)
	call.Done = done

	return call
}

// Proxy forwards message to master
func (n *ClusterNode) forward(msg *ClusterReq) error {
	log.Printf("cluster: forwarding request to node '%s'", n.name)
	msg.Node = globals.cluster.thisNodeName
	var rejected bool
	err := n.call("Cluster.Master", msg, &rejected)
	if err == nil && rejected {
		err = errors.New("cluster: master node out of sync")
	}
	return err
}

// Master responds to proxy
func (n *ClusterNode) respond(msg *ClusterResp) error {
	log.Printf("cluster: replying to node '%s'", n.name)
	var unused bool
	return n.call("Cluster.Proxy", msg, &unused)
}

// Routes the message within the cluster.
func (n *ClusterNode) route(msg *ClusterReq) error {
	log.Printf("cluster: routing message for topic '%s' to node '%s'", msg.RcptTo, n.name)
	var unused bool
	return n.call("Cluster.Route", msg, &unused)
}

// Cluster is the representation of the cluster.
type Cluster struct {
	// Cluster nodes with RPC endpoints (excluding current node).
	nodes map[string]*ClusterNode
	// Name of the local node
	thisNodeName string
	// Fingerprint of the local node
	fingerprint int64

	// Resolved address to listed on
	listenOn string

	// Socket for inbound connections
	inbound *net.TCPListener
	// Ring hash for mapping topic names to nodes
	ring *rh.Ring

	// Failover parameters. Could be nil if failover is not enabled
	fo *clusterFailover
}

// Master at topic's master node receives C2S messages from topic's proxy nodes.
// The message is treated like it came from a session: find or create a session locally,
// dispatch the message to it like it came from a normal ws/lp connection.
// Called by a remote node.
func (c *Cluster) Master(msg *ClusterReq, rejected *bool) error {
	log.Printf("cluster: Master request received from node '%s'", msg.Node)

	// Find the local session associated with the given remote session.
	sess := globals.sessionStore.Get(msg.Sess.Sid)

	if msg.SessGone {
		// Original session has disconnected. Tear down the local proxied session.
		if sess != nil {
			sess.stop <- nil
		}
	} else if msg.Signature == c.ring.Signature() {
		// This cluster member received a request for a topic it owns.
		node := globals.cluster.nodes[msg.Node]

		if node == nil {
			log.Println("cluster: request from an unknown node", msg.Node)
			return nil
		}

		// Check if the remote node has been restarted and if so cleanup stale sessions
		// which originated at that node.
		if node.fingerprint == 0 {
			node.fingerprint = msg.Fingerprint
		} else if node.fingerprint != msg.Fingerprint {
			globals.sessionStore.NodeRestarted(node.name, msg.Fingerprint)
			node.fingerprint = msg.Fingerprint
		}

		if sess == nil {
			// If the session is not found, create it.
			sess, _ = globals.sessionStore.NewSession(node, msg.Sess.Sid)
			go sess.rpcWriteLoop()
		}

		// Update session params which may have changed since the last call.
		sess.uid = msg.Sess.Uid
		sess.authLvl = msg.Sess.AuthLvl
		sess.ver = msg.Sess.Ver
		sess.userAgent = msg.Sess.UserAgent
		sess.remoteAddr = msg.Sess.RemoteAddr
		sess.lang = msg.Sess.Lang
		sess.deviceID = msg.Sess.DeviceID
		sess.platf = msg.Sess.Platform

		// Dispatch remote message to a local session.
		msg.CliMsg.from = msg.OnBehalfOf
		msg.CliMsg.authLvl = msg.AuthLvl
		sess.dispatch(msg.CliMsg)
	} else {
		// Reject the request: wrong signature, cluster is out of sync.
		*rejected = true
	}

	return nil
}

// Proxy receives messages from the master node addressed to a specific local session.
// Called by Session.writeRPC
func (Cluster) Proxy(msg *ClusterResp, unused *bool) error {
	log.Println("cluster: response from Master for session", msg.FromSID)

	// This cluster member received a response from topic owner to be forwarded to a session
	// Find appropriate session, send the message to it
	if sess := globals.sessionStore.Get(msg.FromSID); sess != nil {
		if !sess.queueOutBytes(msg.Msg) {
			log.Println("cluster.Proxy: timeout")
		}
	} else {
		log.Println("cluster: master response for unknown session", msg.FromSID)
	}

	return nil
}

// Route endpoint receives intra-cluster messages (e.g. pres) destined for the nodes hosting topic.
// Called by Hub.route channel consumer.
func (c *Cluster) Route(msg *ClusterReq, rejected *bool) error {
	log.Printf("cluster: node '%s' received route request for topic '%s' from node '%s'", c.thisNodeName, msg.RcptTo, msg.Node)

	*rejected = false
	if msg.Signature != c.ring.Signature() || msg.SrvMsg == nil {
		*rejected = true
		return nil
	}
	msg.SrvMsg.rcptto = msg.RcptTo
	if msg.SrvMsg.Pres != nil && msg.PresWantReply {
		msg.SrvMsg.Pres.wantReply = true
	}
	globals.hub.route <- msg.SrvMsg
	return nil
}

// User cache & push notifications management. These are calls received by the Master from Proxy.
// The Proxy expects no payload to be returned by the master.

// UserCacheUpdate endpoint receives updates to user's cached values as well as sends push notifications.
func (c *Cluster) UserCacheUpdate(msg *UserCacheReq, rejected *bool) error {
	log.Printf("cluster: node '%s' received user cache update from node '%s'", c.thisNodeName, msg.Node)
	usersRequestFromCluster(msg)
	return nil
}

// Sends user cache update to user's Master node where the cache actually resides.
// The request is extected to contain users who reside at remote nodes only.
func (c *Cluster) routeUserReq(req *UserCacheReq) error {
	// Index requests by cluster node.
	reqByNode := make(map[string]*UserCacheReq)

	if req.PushRcpt != nil {
		// Request to send push notifications. Create separate packets for each affected cluster node.
		for uid, recepient := range req.PushRcpt.To {
			n := c.nodeForTopic(uid.UserId())
			if n == nil {
				return errors.New("attempt to update user at a non-existent node (1)")
			}
			r := reqByNode[n.name]
			if r == nil {
				r = &UserCacheReq{
					PushRcpt: &push.Receipt{
						Payload: req.PushRcpt.Payload,
						To:      make(map[types.Uid]push.Recipient)},
					Node: c.thisNodeName}
			}
			r.PushRcpt.To[uid] = recepient
			reqByNode[n.name] = r
		}
	} else if len(req.UserIdList) > 0 {
		// Request to add/remove user from cache.
		for _, uid := range req.UserIdList {
			n := c.nodeForTopic(uid.UserId())
			if n == nil {
				return errors.New("attempt to update user at a non-existent node (2)")
			}
			r := reqByNode[n.name]
			if r == nil {
				r = &UserCacheReq{Node: c.thisNodeName, Inc: req.Inc}
			}
			r.UserIdList = append(r.UserIdList, uid)
			reqByNode[n.name] = r
		}
	}

	if len(reqByNode) > 0 {
		for nodeName, r := range reqByNode {
			n := globals.cluster.nodes[nodeName]
			var rejected bool
			err := n.call("Cluster.UserCacheUpdate", r, &rejected)
			if rejected {
				err = errors.New("cluster: master node out of sync")
			}
			if err != nil {
				return err
			}
		}
		return nil
	}

	// Update to cached values.
	n := c.nodeForTopic(req.UserId.UserId())
	if n == nil {
		return errors.New("attempt to update user at a non-existent node (3)")
	}
	req.Node = c.thisNodeName
	var rejected bool
	err := n.call("Cluster.UserCacheUpdate", req, &rejected)
	if rejected {
		err = errors.New("cluster: master node out of sync")
	}
	return err
}

// Given topic name, find appropriate cluster node to route message to
func (c *Cluster) nodeForTopic(topic string) *ClusterNode {
	key := c.ring.Get(topic)
	if key == c.thisNodeName {
		log.Println("cluster: request to route to self")
		// Do not route to self
		return nil
	}

	node := globals.cluster.nodes[key]
	if node == nil {
		log.Println("cluster: no node for topic", topic, key)
	}
	return node
}

// isRemoteTopic checks if the given topic is handled by this node or a remote node.
func (c *Cluster) isRemoteTopic(topic string) bool {
	if c == nil {
		// Cluster not initialized, all topics are local
		return false
	}
	return c.ring.Get(topic) != c.thisNodeName
}

// Returns remote node name where the topic is hosted.
// If the topic is hosted locally, returns an empty string.
func (c *Cluster) nodeNameForTopicIfRemote(topic string) string {
	if c == nil {
		// Cluster not initialized, all topics are local
		return ""
	}
	key := c.ring.Get(topic)
	if key == c.thisNodeName {
		return ""
	}
	return key
}

// isPartitioned checks if the cluster is partitioned due to network or other failure and if the
// current node is a part of the smaller partition.
func (c *Cluster) isPartitioned() bool {
	if c == nil || c.fo == nil {
		// Cluster not initialized or failover disabled therefore not partitioned.
		return false
	}

	return (len(c.nodes)+1)/2 >= len(c.fo.activeNodes)
}

// Forward client request message to the Master (cluster node which owns the topic)
func (c *Cluster) routeToTopic(msg *ClientComMessage, topic string, sess *Session) error {
	// Find the cluster node which owns the topic, then forward to it.
	n := c.nodeForTopic(topic)
	if n == nil {
		return errors.New("attempt to route to non-existent node")
	}

	// Save node name: it's needed in order to inform relevant nodes when the session is disconnected.
	if sess.nodes == nil {
		sess.nodes = make(map[string]bool)
	}
	sess.nodes[n.name] = true

	req := &ClusterReq{
		Node:        c.thisNodeName,
		Signature:   c.ring.Signature(),
		Fingerprint: c.fingerprint,
		CliMsg:      msg,
		RcptTo:      topic,
		Sess: &ClusterSess{
			Uid:        sess.uid,
			AuthLvl:    sess.authLvl,
			RemoteAddr: sess.remoteAddr,
			UserAgent:  sess.userAgent,
			Ver:        sess.ver,
			Lang:       sess.lang,
			DeviceID:   sess.deviceID,
			Platform:   sess.platf,
			Sid:        sess.sid}}

	if sess.authLvl == auth.LevelRoot {
		// Assign these values only when the sender is root
		req.OnBehalfOf = msg.from
		req.AuthLvl = msg.authLvl
	}

	return n.forward(req)

}

// Forward server response message to the node that owns topic.
func (c *Cluster) routeToTopicIntraCluster(topic string, msg *ServerComMessage) error {
	n := c.nodeForTopic(topic)
	if n == nil {
		return errors.New("attempt to route to non-existent node")
	}

	req := &ClusterReq{
		Node:        c.thisNodeName,
		Signature:   c.ring.Signature(),
		Fingerprint: c.fingerprint,
		RcptTo:      topic,
		SrvMsg:      msg}
	if msg.Pres != nil && msg.Pres.wantReply {
		req.PresWantReply = true
	}

	return n.route(req)
}

// Session terminated at origin. Inform remote Master nodes that the session is gone.
func (c *Cluster) sessionGone(sess *Session) error {
	if c == nil {
		return nil
	}

	// Save node name: it's need in order to inform relevant nodes when the session is disconnected
	for name := range sess.nodes {
		n := c.nodes[name]
		if n != nil {
			return n.forward(
				&ClusterReq{
					Node:        c.thisNodeName,
					Fingerprint: c.fingerprint,
					SessGone:    true,
					Sess: &ClusterSess{
						Uid:        sess.uid,
						RemoteAddr: sess.remoteAddr,
						UserAgent:  sess.userAgent,
						Ver:        sess.ver,
						Sid:        sess.sid}})
		}
	}
	return nil
}

// Returns snowflake worker id
func clusterInit(configString json.RawMessage, self *string) int {
	if globals.cluster != nil {
		log.Fatal("Cluster already initialized.")
	}

	// Registering variables even if it's a standalone server. Otherwise monitoring software will
	// complain about missing vars.

	// 1 if this node is cluster leader, 0 otherwise
	statsRegisterInt("ClusterLeader")
	// Total number of nodes configured
	statsRegisterInt("TotalClusterNodes")
	// Number of nodes currently believed to be up.
	statsRegisterInt("LiveClusterNodes")

	// This is a standalone server, not initializing
	if len(configString) == 0 {
		log.Println("Running as a standalone server.")
		return 1
	}

	var config clusterConfig
	if err := json.Unmarshal(configString, &config); err != nil {
		log.Fatal(err)
	}

	thisName := *self
	if thisName == "" {
		thisName = config.ThisName
	}

	// Name of the current node is not specified - disable clustering
	if thisName == "" {
		log.Println("Running as a standalone server.")
		return 1
	}

	gob.Register([]interface{}{})
	gob.Register(map[string]interface{}{})

	globals.cluster = &Cluster{
		thisNodeName: thisName,
		fingerprint:  time.Now().Unix(),
		nodes:        make(map[string]*ClusterNode)}

	var nodeNames []string
	for _, host := range config.Nodes {
		nodeNames = append(nodeNames, host.Name)

		if host.Name == thisName {
			globals.cluster.listenOn = host.Addr
			// Don't create a cluster member for this local instance
			continue
		}

		globals.cluster.nodes[host.Name] = &ClusterNode{
			address: host.Addr,
			name:    host.Name,
			done:    make(chan bool, 1)}
	}

	if len(globals.cluster.nodes) == 0 {
		// Cluster needs at least two nodes.
		log.Fatal("Invalid cluster size: 1")
	}

	if !globals.cluster.failoverInit(config.Failover) {
		globals.cluster.rehash(nil)
	}

	sort.Strings(nodeNames)
	workerId := sort.SearchStrings(nodeNames, thisName) + 1

	statsSet("TotalClusterNodes", int64(len(globals.cluster.nodes)+1))

	return workerId
}

// This is a session handler at a master node: forward messages from the master to the session origin.
func (sess *Session) rpcWriteLoop() {
	// There is no readLoop for RPC, delete the session here
	defer func() {
		log.Println("writeRPC - stop")
		sess.closeRPC()
		globals.sessionStore.Delete(sess)
		sess.unsubAll()
	}()

	for {
		select {
		case msg, ok := <-sess.send:
			if !ok || sess.clnode.endpoint == nil {
				// channel closed
				return
			}
			// The error is returned if the remote node is down. Which means the remote
			// session is also disconnected.
			if err := sess.clnode.respond(&ClusterResp{Msg: msg.([]byte), FromSID: sess.sid}); err != nil {

				log.Println("sess.writeRPC: " + err.Error())
				return
			}
		case msg := <-sess.stop:
			// Shutdown is requested, don't care if the message is delivered
			if msg != nil {
				sess.clnode.respond(&ClusterResp{Msg: msg.([]byte), FromSID: sess.sid})
			}
			return

		case topic := <-sess.detach:
			sess.delSub(topic)
		}
	}
}

// Proxied session is being closed at the Master node
func (sess *Session) closeRPC() {
	if sess.proto == CLUSTER {
		log.Println("cluster: session closed at master")
	}
}

// Start accepting connections.
func (c *Cluster) start() {
	addr, err := net.ResolveTCPAddr("tcp", c.listenOn)
	if err != nil {
		log.Fatal(err)
	}

	c.inbound, err = net.ListenTCP("tcp", addr)

	if err != nil {
		log.Fatal(err)
	}

	for _, n := range c.nodes {
		go n.reconnect()
	}

	if c.fo != nil {
		go c.run()
	}

	err = rpc.Register(c)
	if err != nil {
		log.Fatal(err)
	}

	go rpc.Accept(c.inbound)

	log.Printf("Cluster of %d nodes initialized, node '%s' listening on [%s]", len(globals.cluster.nodes)+1,
		globals.cluster.thisNodeName, c.listenOn)
}

func (c *Cluster) shutdown() {
	if globals.cluster == nil {
		return
	}
	globals.cluster = nil

	c.inbound.Close()

	if c.fo != nil {
		c.fo.done <- true
	}

	for _, n := range c.nodes {
		n.done <- true
	}

	log.Println("Cluster shut down")
}

// Recalculate the ring hash using provided list of nodes or only nodes in a non-failed state.
// Returns the list of nodes used for ring hash.
func (c *Cluster) rehash(nodes []string) []string {
	ring := rh.New(clusterHashReplicas, nil)

	var ringKeys []string

	if nodes == nil {
		for _, node := range c.nodes {
			ringKeys = append(ringKeys, node.name)
		}
		ringKeys = append(ringKeys, c.thisNodeName)
	} else {
		ringKeys = append(ringKeys, nodes...)
	}
	ring.Add(ringKeys...)

	c.ring = ring

	return ringKeys
}

// Iterates over sessions hosted on this node and for each session
// sends "{pres term}" to all displayed topics.
// Called immediately after Cluster.rehash().
func (c *Cluster) invalidateRemoteSubs(ss *SessionStore) {
	ss.lock.Lock()
	defer ss.lock.Unlock()

	for _, s := range ss.sessCache {
		if s.proto == CLUSTER || len(s.remoteSubs) == 0 {
			continue
		}
		s.remoteSubsLock.Lock()
		var topicsToTerminate []string
		var keysToDelete []string
		for topic, remSub := range s.remoteSubs {
			if remSub.node != c.ring.Get(topic) {
				topicsToTerminate = append(topicsToTerminate, remSub.originalTopic)
				keysToDelete = append(keysToDelete, topic)
				delete(s.remoteSubs, topic)
			}
		}
		s.remoteSubsLock.Unlock()
		s.presTermDirect(topicsToTerminate)
	}
}
