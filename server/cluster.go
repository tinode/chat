package main

import (
	"encoding/gob"
	"encoding/json"
	"errors"
	"net"
	"net/rpc"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tinode/chat/server/auth"
	"github.com/tinode/chat/server/concurrency"
	"github.com/tinode/chat/server/logs"
	"github.com/tinode/chat/server/push"
	rh "github.com/tinode/chat/server/ringhash"
	"github.com/tinode/chat/server/store/types"
)

const (
	// Default timeout before attempting to reconnect to a node.
	defaultClusterReconnect = 200 * time.Millisecond
	// Number of replicas in ringhash.
	clusterHashReplicas = 20
	// Period for running health check on cluster session: terminate sessions with no subscriptions.
	clusterSessionCleanup = 5 * time.Second
)

// ProxyReqType is the type of proxy requests.
type ProxyReqType int

// Individual request types.
const (
	ProxyReqNone ProxyReqType = iota
	ProxyReqJoin
	ProxyReqLeave
	ProxyReqMeta
	ProxyReqBroadcast
	ProxyReqBgSession
	ProxyReqMeUserAgent
)

// ProxyEventType is an enumeration of possible proxy event types processed in the clusterWriteLoop.
type ProxyEventType int

// Individual proxy events.
const (
	EventSend     ProxyEventType = 1
	EventStop     ProxyEventType = 2
	EventDetach   ProxyEventType = 3
	EventContinue ProxyEventType = 4
	EventAbort    ProxyEventType = 5
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
	// Deprecated: this field is no longer used.
	NumProxyEventGoRoutines int `json:"-"`
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

	// IDs of multiplexing sessions belonging to this node.
	msess map[string]struct{}

	// Default channel for receiving responses to RPC calls issued by this node.
	rpcDone chan *rpc.Call
}

func (n *ClusterNode) asyncRpcLoop() {
	for call := range n.rpcDone {
		n.handleRpcResponse(call)
	}
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
	// Country of the client
	CountryCode string

	// Device ID
	DeviceID string

	// Device platform: "web", "ios", "android"
	Platform string

	// Session ID
	Sid string

	// Background session
	Background bool
}

// ClusterSessUpdate represents a request to update a session.
// User Agent change or background session comes to foreground.
type ClusterSessUpdate struct {
	// User this session represents.
	Uid types.Uid
	// Session id.
	Sid string
	// Session user agent.
	UserAgent string
}

// ClusterReq is either a Proxy to Master or Topic Proxy to Topic Master or intra-cluster routing request message.
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

	// Type of request.
	ReqType ProxyReqType

	// Client message. Set for C2S requests.
	CliMsg *ClientComMessage
	// Message to be routed. Set for intra-cluster route requests.
	SrvMsg *ServerComMessage

	// Expanded (routable) topic name
	RcptTo string
	// Originating session
	Sess *ClusterSess
	// True when the topic proxy is gone.
	Gone bool
}

// ClusterRoute is intra-cluster routing request message.
type ClusterRoute struct {
	// Name of the node sending this request
	Node string

	// Ring hash signature of the node sending this request
	// Signature must match the signature of the receiver, otherwise the
	// Cluster is desynchronized.
	Signature string

	// Fingerprint of the node sending this request.
	// Fingerprint changes when the node is restarted.
	Fingerprint int64

	// Message to be routed. Set for intra-cluster route requests.
	SrvMsg *ServerComMessage

	// Originating session
	Sess *ClusterSess
}

// ClusterResp is a Master to Proxy response message.
type ClusterResp struct {
	// Server message with the response.
	SrvMsg *ServerComMessage
	// Originating session ID to forward response to, if any.
	OrigSid string
	// Expanded (routable) topic name
	RcptTo string

	// Parameters sent back by the topic master in response a topic proxy request.

	// Original request type.
	OrigReqType ProxyReqType
}

// ClusterPing is used to detect node restarts.
type ClusterPing struct {
	// Name of the node sending this request.
	Node string

	// Fingerprint of the node sending this request.
	// Fingerprint changes when the node restarts.
	Fingerprint int64
}

// Handle outbound node communication: read messages from the channel, forward to remote nodes.
// FIXME(gene): this will drain the outbound queue in case of a failure: all unprocessed messages will be dropped.
// Maybe it's a good thing, maybe not.
func (n *ClusterNode) reconnect() {
	var reconnTicker *time.Ticker

	// Avoid parallel reconnection threads.
	n.lock.Lock()
	if n.reconnecting {
		n.lock.Unlock()
		return
	}
	n.reconnecting = true
	n.lock.Unlock()

	count := 0
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
			logs.Info.Println("cluster: connected to", n.name)
			// Send this node credentials to the new node.
			var unused bool
			n.call("Cluster.Ping",
				&ClusterPing{
					Node:        globals.cluster.thisNodeName,
					Fingerprint: globals.cluster.fingerprint,
				},
				&unused)
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
			logs.Info.Println("cluster: shutdown started at node", n.name)
			reconnTicker.Stop()
			if n.endpoint != nil {
				n.endpoint.Close()
			}
			n.lock.Lock()
			n.connected = false
			n.reconnecting = false
			n.lock.Unlock()
			logs.Info.Println("cluster: shut down completed at node", n.name)
			return
		}
	}
}

func (n *ClusterNode) call(proc string, req, resp interface{}) error {
	if !n.connected {
		return errors.New("cluster: node '" + n.name + "' not connected")
	}

	if err := n.endpoint.Call(proc, req, resp); err != nil {
		logs.Warn.Println("cluster: call failed", n.name, err)

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

func (n *ClusterNode) handleRpcResponse(call *rpc.Call) {
	if call.Error != nil {
		logs.Warn.Printf("cluster: %s call failed: %s", call.ServiceMethod, call.Error)
		n.lock.Lock()
		if n.connected {
			n.endpoint.Close()
			n.connected = false
			statsInc("LiveClusterNodes", -1)
			go n.reconnect()
		}
		n.lock.Unlock()
	}
}

func (n *ClusterNode) callAsync(proc string, req, resp interface{}, done chan *rpc.Call) *rpc.Call {
	if done != nil && cap(done) == 0 {
		logs.Err.Panic("cluster: RPC done channel is unbuffered")
	}

	if !n.connected {
		call := &rpc.Call{
			ServiceMethod: proc,
			Args:          req,
			Reply:         resp,
			Error:         errors.New("cluster: node '" + n.name + "' not connected"),
			Done:          done,
		}
		if done != nil {
			done <- call
		}
		return call
	}

	var responseChan chan *rpc.Call
	if done != nil {
		// Make a separate response callback if we need to notify the caller.
		myDone := make(chan *rpc.Call, 1)
		go func() {
			call := <-myDone
			n.handleRpcResponse(call)
			if done != nil {
				done <- call
			}
		}()
		responseChan = myDone
	} else {
		responseChan = n.rpcDone
	}

	call := n.endpoint.Go(proc, req, resp, responseChan)

	return call
}

// proxyToMaster forwards request from topic proxy to topic master.
func (n *ClusterNode) proxyToMaster(msg *ClusterReq) error {
	msg.Node = globals.cluster.thisNodeName
	var rejected bool
	err := n.call("Cluster.TopicMaster", msg, &rejected)
	if err == nil && rejected {
		err = errors.New("cluster: topic master node out of sync")
	}
	return err
}

// masterToProxyAsync forwards response from topic master to topic proxy
// in a fire-and-forget manner.
func (n *ClusterNode) masterToProxyAsync(msg *ClusterResp) error {
	var unused bool
	if c := n.callAsync("Cluster.TopicProxy", msg, &unused, nil); c.Error != nil {
		return c.Error
	}
	return nil
}

// route routes server message within the cluster.
func (n *ClusterNode) route(msg *ClusterRoute) error {
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

	// Thread pool to use for running proxy session (write) event processing logic.
	// The number of proxy sessions grows as O(number of topics x number of cluster nodes).
	// In large Tinode deployments (10s of thousands of topics, tens of nodes),
	// running a separate event processing goroutine for each proxy session
	// leads to a rather large memory usage and excessive scheduling overhead.
	proxyEventQueue *concurrency.GoRoutinePool
}

// TopicMaster is a gRPC endpoint which receives requests sent by proxy topic to master topic.
func (c *Cluster) TopicMaster(msg *ClusterReq, rejected *bool) error {
	*rejected = false

	node := c.nodes[msg.Node]
	if node == nil {
		logs.Warn.Println("cluster TopicMaster: request from an unknown node", msg.Node)
		return nil
	}

	// Master maintains one multiplexing session per proxy topic per node.
	msid := msg.RcptTo + "-" + msg.Node
	msess := globals.sessionStore.Get(msid)

	if msg.Gone {
		// Proxy topic is gone. Tear down the local auxiliary session.
		// If it was the last session, master topic will shut down as well.
		if msess != nil {
			msess.stopSession(nil)
			node.lock.Lock()
			delete(node.msess, msid)
			node.lock.Unlock()
		}

		return nil
	}

	if msg.Signature != c.ring.Signature() {
		logs.Warn.Println("cluster TopicMaster: session signature mismatch", msg.RcptTo)
		*rejected = true
		return nil
	}

	// Create a new multiplexing session if needed.
	if msess == nil {
		// If the session is not found, create it.
		var count int
		msess, count = globals.sessionStore.NewSession(node, msid)
		node.lock.Lock()
		node.msess[msid] = struct{}{}
		node.lock.Unlock()

		logs.Info.Println("cluster: multiplexing session started", msid, count)
		msess.proxiedTopic = msg.RcptTo
	}

	// This is a local copy of a remote session.
	var sess *Session
	// Sess is nil for user agent changes and deferred presence notification requests.
	if msg.Sess != nil {
		// We only need some session info. No need to copy everything.
		sess = &Session{
			proto: PROXY,
			// Multiplexing session which actually handles the communication.
			multi: msess,
			// Local parameters specific to this session.
			sid:         msg.Sess.Sid,
			userAgent:   msg.Sess.UserAgent,
			remoteAddr:  msg.Sess.RemoteAddr,
			lang:        msg.Sess.Lang,
			countryCode: msg.Sess.CountryCode,
			proxyReq:    msg.ReqType,
			background:  msg.Sess.Background,
			uid:         msg.Sess.Uid,
		}
	}

	if msg.CliMsg != nil {
		msg.CliMsg.sess = sess
	}

	switch msg.ReqType {
	case ProxyReqJoin:
		select {
		case globals.hub.join <- msg.CliMsg:
		default:
			// Reply with a 500 to the user.
			sess.queueOut(ErrUnknownReply(msg.CliMsg, msg.CliMsg.Timestamp))
			logs.Warn.Println("cluster: join req failed - hub.join queue full, topic ", msg.CliMsg.RcptTo,
				"; orig sid ", sess.sid)
		}

	case ProxyReqLeave:
		if t := globals.hub.topicGet(msg.RcptTo); t != nil {
			t.unreg <- msg.CliMsg
		} else {
			logs.Warn.Println("cluster: leave request for unknown topic", msg.RcptTo)
		}

	case ProxyReqMeta:
		if t := globals.hub.topicGet(msg.RcptTo); t != nil {
			select {
			case t.meta <- msg.CliMsg:
			default:
				sess.queueOut(ErrUnknownReply(msg.CliMsg, msg.CliMsg.Timestamp))
				logs.Warn.Println("cluster: meta req failed - topic.meta queue full, topic ", msg.CliMsg.RcptTo,
					"; orig sid ", sess.sid)
			}
		} else {
			logs.Warn.Println("cluster: meta request for unknown topic", msg.RcptTo)
		}

	case ProxyReqBroadcast:
		select {
		case globals.hub.routeCli <- msg.CliMsg:
		default:
			logs.Err.Println("cluster: route req failed - hub.route queue full")
		}

	case ProxyReqBgSession, ProxyReqMeUserAgent:
		// sess could be nil
		if t := globals.hub.topicGet(msg.RcptTo); t != nil {
			if t.supd == nil {
				logs.Err.Panicln("cluster: invalid topic category in session update", t.name, msg.ReqType)
			}
			su := &sessionUpdate{}
			if msg.ReqType == ProxyReqBgSession {
				su.sess = sess
			} else {
				su.userAgent = sess.userAgent
			}
			t.supd <- su
		} else {
			logs.Warn.Println("cluster: session update for unknown topic", msg.RcptTo, msg.ReqType)
		}

	default:
		logs.Warn.Println("cluster: unknown request type", msg.ReqType, msg.RcptTo)
		*rejected = true
	}

	return nil
}

// TopicProxy is a gRPC endpoint at topic proxy which receives topic master responses.
func (Cluster) TopicProxy(msg *ClusterResp, unused *bool) error {
	// This cluster member received a response from the topic master to be forwarded to the topic.
	// Find appropriate topic, send the message to it.
	if t := globals.hub.topicGet(msg.RcptTo); t != nil {
		msg.SrvMsg.uid = types.ParseUserId(msg.SrvMsg.AsUser)
		t.proxy <- msg
	} else {
		logs.Warn.Println("cluster: master response for unknown topic", msg.RcptTo)
	}

	return nil
}

// Route endpoint receives intra-cluster messages destined for the nodes hosting the topic.
// Called by Hub.route channel consumer for messages send without attaching to topic first.
func (c *Cluster) Route(msg *ClusterRoute, rejected *bool) error {
	*rejected = false
	if msg.Signature != c.ring.Signature() {
		sid := ""
		if msg.Sess != nil {
			sid = msg.Sess.Sid
		}
		logs.Warn.Println("cluster Route: session signature mismatch", sid)
		*rejected = true
		return nil
	}
	if msg.SrvMsg == nil {
		sid := ""
		if msg.Sess != nil {
			sid = msg.Sess.Sid
		}
		// TODO: maybe panic here.
		logs.Warn.Println("cluster Route: nil server message", sid)
		*rejected = true
		return nil
	}
	globals.hub.routeSrv <- msg.SrvMsg
	return nil
}

// User cache & push notifications management. These are calls received by the Master from Proxy.
// The Proxy expects no payload to be returned by the master.

// UserCacheUpdate endpoint receives updates to user's cached values as well as sends push notifications.
func (c *Cluster) UserCacheUpdate(msg *UserCacheReq, rejected *bool) error {
	if msg.Gone {
		// User is deleted. Evict all user's sessions.
		globals.sessionStore.EvictUser(msg.UserId, "")

		if globals.cluster.isRemoteTopic(msg.UserId.UserId()) {
			// No need to delete user's cache if user is remote.
			return nil
		}
	}

	usersRequestFromCluster(msg)
	return nil
}

// Ping is a gRPC endpoint which receives ping requests from peer nodes.Used to detect node restarts.
func (c *Cluster) Ping(ping *ClusterPing, unused *bool) error {
	node := c.nodes[ping.Node]
	if node == nil {
		logs.Warn.Println("cluster Ping from unknown node", ping.Node)
		return nil
	}

	if node.fingerprint == 0 {
		// This is the first connection to remote node.
		node.fingerprint = ping.Fingerprint
	} else if node.fingerprint != ping.Fingerprint {
		// Remote node restarted.
		node.fingerprint = ping.Fingerprint
		c.invalidateProxySubs(ping.Node)
		c.gcProxySessionsForNode(ping.Node)
	}

	return nil
}

// Sends user cache update to user's Master node where the cache actually resides.
// The request is extected to contain users who reside at remote nodes only.
func (c *Cluster) routeUserReq(req *UserCacheReq) error {
	// Index requests by cluster node.
	reqByNode := make(map[string]*UserCacheReq)

	if req.PushRcpt != nil {
		// Request to send push notifications. Create separate packets for each affected cluster node.
		for uid, recipient := range req.PushRcpt.To {
			n := c.nodeForTopic(uid.UserId())
			if n == nil {
				return errors.New("attempt to update user at a non-existent node (1)")
			}
			r := reqByNode[n.name]
			if r == nil {
				r = &UserCacheReq{
					PushRcpt: &push.Receipt{
						Payload: req.PushRcpt.Payload,
						To:      make(map[types.Uid]push.Recipient),
					},
					Node: c.thisNodeName,
				}
			}
			r.PushRcpt.To[uid] = recipient
			reqByNode[n.name] = r
		}
	} else if len(req.UserIdList) > 0 {
		// Request to add/remove some users from cache.
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
	} else if req.Gone {
		// Message that the user is deleted is sent to all nodes.
		r := &UserCacheReq{Node: c.thisNodeName, UserIdList: req.UserIdList, Gone: true}
		for _, n := range c.nodes {
			reqByNode[n.name] = r
		}
	}

	if len(reqByNode) > 0 {
		for nodeName, r := range reqByNode {
			n := c.nodes[nodeName]
			var rejected bool
			err := n.call("Cluster.UserCacheUpdate", r, &rejected)
			if rejected {
				err = errors.New("master node out of sync")
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
		err = errors.New("master node out of sync")
	}
	return err
}

// Given topic name, find appropriate cluster node to route message to.
func (c *Cluster) nodeForTopic(topic string) *ClusterNode {
	key := c.ring.Get(topic)
	if key == c.thisNodeName {
		logs.Err.Println("cluster: request to route to self")
		// Do not route to self
		return nil
	}

	node := c.nodes[key]
	if node == nil {
		logs.Warn.Println("cluster: no node for topic", topic, key)
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

// genLocalTopicName is just like genTopicName(), but the generated name belongs to the current cluster node.
func (c *Cluster) genLocalTopicName() string {
	topic := genTopicName()
	if c == nil {
		// Cluster not initialized, all topics are local
		return topic
	}

	// TODO: if cluster is large it may become too inefficient.
	for c.ring.Get(topic) != c.thisNodeName {
		topic = genTopicName()
	}
	return topic
}

// isPartitioned checks if the cluster is partitioned due to network or other failure and if the
// current node is a part of the smaller partition.
func (c *Cluster) isPartitioned() bool {
	if c == nil || c.fo == nil {
		// Cluster not initialized or failover disabled therefore not partitioned.
		return false
	}

	c.fo.activeNodesLock.RLock()
	result := (len(c.nodes)+1)/2 >= len(c.fo.activeNodes)
	c.fo.activeNodesLock.RUnlock()

	return result
}

func (c *Cluster) makeClusterReq(reqType ProxyReqType, msg *ClientComMessage, topic string, sess *Session) *ClusterReq {
	req := &ClusterReq{
		Node:        c.thisNodeName,
		Signature:   c.ring.Signature(),
		Fingerprint: c.fingerprint,
		ReqType:     reqType,
		RcptTo:      topic,
	}

	var uid types.Uid

	if msg != nil {
		req.CliMsg = msg
		uid = types.ParseUserId(req.CliMsg.AsUser)
	}

	if sess != nil {
		if uid.IsZero() {
			uid = sess.uid
		}

		req.Sess = &ClusterSess{
			Uid:         uid,
			AuthLvl:     sess.authLvl,
			RemoteAddr:  sess.remoteAddr,
			UserAgent:   sess.userAgent,
			Ver:         sess.ver,
			Lang:        sess.lang,
			CountryCode: sess.countryCode,
			DeviceID:    sess.deviceID,
			Platform:    sess.platf,
			Sid:         sess.sid,
			Background:  sess.background,
		}
	}
	return req
}

// Forward client request message from the Topic Proxy to the Topic Master (cluster node which owns the topic).
func (c *Cluster) routeToTopicMaster(reqType ProxyReqType, msg *ClientComMessage, topic string, sess *Session) error {
	if c == nil {
		// Cluster may be nil due to shutdown.
		return nil
	}
	if sess != nil {
		if atomic.LoadInt32(&sess.terminating) > 0 {
			// The session is terminating.
			return nil
		}
	}

	// Find the cluster node which owns the topic, then forward to it.
	n := c.nodeForTopic(topic)
	if n == nil {
		return errors.New("node for topic not found")
	}

	req := c.makeClusterReq(reqType, msg, topic, sess)
	return n.proxyToMaster(req)
}

// Forward server response message to the node that owns topic.
func (c *Cluster) routeToTopicIntraCluster(topic string, msg *ServerComMessage, sess *Session) error {
	if c == nil {
		// Cluster may be nil due to shutdown.
		return nil
	}

	n := c.nodeForTopic(topic)
	if n == nil {
		return errors.New("node for topic not found (intra)")
	}

	route := &ClusterRoute{
		Node:        c.thisNodeName,
		Signature:   c.ring.Signature(),
		Fingerprint: c.fingerprint,
		SrvMsg:      msg,
	}

	if sess != nil {
		route.Sess = &ClusterSess{Sid: sess.sid}
	}
	return n.route(route)
}

// Topic proxy terminated. Inform remote Master node that the proxy is gone.
func (c *Cluster) topicProxyGone(topicName string) error {
	if c == nil {
		// Cluster may be nil due to shutdown.
		return nil
	}

	// Find the cluster node which owns the topic, then forward to it.
	n := c.nodeForTopic(topicName)
	if n == nil {
		return errors.New("node for topic not found")
	}

	req := c.makeClusterReq(ProxyReqLeave, nil, topicName, nil)
	req.Gone = true
	return n.proxyToMaster(req)
}

// Returns snowflake worker id.
func clusterInit(configString json.RawMessage, self *string) int {
	if globals.cluster != nil {
		logs.Err.Fatal("Cluster already initialized.")
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
		logs.Info.Println("Cluster: running as a standalone server.")
		return 1
	}

	var config clusterConfig
	if err := json.Unmarshal(configString, &config); err != nil {
		logs.Err.Fatal(err)
	}

	thisName := *self
	if thisName == "" {
		thisName = config.ThisName
	}

	// Name of the current node is not specified: clustering disabled.
	if thisName == "" {
		logs.Info.Println("Cluster: running as a standalone server.")
		return 1
	}

	gob.Register([]interface{}{})
	gob.Register(map[string]interface{}{})
	gob.Register(map[string]int{})
	gob.Register(map[string]string{})
	gob.Register(MsgAccessMode{})

	if config.NumProxyEventGoRoutines != 0 {
		logs.Warn.Println("Cluster config: field num_proxy_event_goroutines is deprecated.")
	}

	globals.cluster = &Cluster{
		thisNodeName:    thisName,
		fingerprint:     time.Now().Unix(),
		nodes:           make(map[string]*ClusterNode),
		proxyEventQueue: concurrency.NewGoRoutinePool(len(config.Nodes) * 5),
	}

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
			done:    make(chan bool, 1),
			msess:   make(map[string]struct{}),
		}
	}

	if len(globals.cluster.nodes) == 0 {
		// Cluster needs at least two nodes.
		logs.Err.Fatal("Cluster: invalid cluster size: 1")
	}

	if !globals.cluster.failoverInit(config.Failover) {
		globals.cluster.rehash(nil)
	}

	sort.Strings(nodeNames)
	workerId := sort.SearchStrings(nodeNames, thisName) + 1

	statsSet("TotalClusterNodes", int64(len(globals.cluster.nodes)+1))

	return workerId
}

// Proxied session is being closed at the Master node.
func (sess *Session) closeRPC() {
	if sess.isMultiplex() {
		logs.Info.Println("cluster: session proxy closed", sess.sid)
	}
}

// Start accepting connections.
func (c *Cluster) start() {
	addr, err := net.ResolveTCPAddr("tcp", c.listenOn)
	if err != nil {
		logs.Err.Fatal(err)
	}

	c.inbound, err = net.ListenTCP("tcp", addr)

	if err != nil {
		logs.Err.Fatal(err)
	}

	for _, n := range c.nodes {
		go n.reconnect()
		n.rpcDone = make(chan *rpc.Call, len(c.nodes)*50)
		go n.asyncRpcLoop()
	}

	if c.fo != nil {
		go c.run()
	}

	err = rpc.Register(c)
	if err != nil {
		logs.Err.Fatal(err)
	}

	go rpc.Accept(c.inbound)

	logs.Info.Printf("Cluster of %d nodes initialized, node '%s' is listening on [%s]", len(globals.cluster.nodes)+1,
		globals.cluster.thisNodeName, c.listenOn)
}

func (c *Cluster) shutdown() {
	if globals.cluster == nil {
		return
	}
	for _, n := range c.nodes {
		close(n.rpcDone)
	}

	globals.cluster.proxyEventQueue.Stop()
	globals.cluster = nil

	c.inbound.Close()

	if c.fo != nil {
		c.fo.done <- true
	}

	for _, n := range c.nodes {
		n.done <- true
	}

	logs.Info.Println("Cluster shut down")
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

// invalidateProxySubs iterates over sessions proxied on this node and for each session
// sends "{pres term}" informing that the topic subscription (attachment) was lost:
// - Called immediately after Cluster.rehash() for all relocated topics (forNode == "").
// - Called for topics hosted at a specific node when a node restart is detected.
// TODO: consider resubscribing to topics instead of forcing sessions to resubscribe.
func (c *Cluster) invalidateProxySubs(forNode string) {
	sessions := make(map[*Session][]string)
	globals.hub.topics.Range(func(_, v interface{}) bool {
		topic := v.(*Topic)
		if !topic.isProxy {
			// Topic isn't a proxy.
			return true
		}
		if forNode == "" {
			if topic.masterNode == c.ring.Get(topic.name) {
				// The topic hasn't moved. Continue.
				return true
			}
		} else if topic.masterNode != forNode {
			// The topic is hosted at a different node than the restarted node.
			return true
		}

		for s, psd := range topic.sessions {
			// FIXME: 'me' topic must be the last one in the list for each topic.
			sessions[s] = append(sessions[s], topicNameForUser(topic.name, psd.uid, psd.isChanSub))
		}
		return true
	})

	for s, topicsToTerminate := range sessions {
		s.presTermDirect(topicsToTerminate)
	}
}

// gcProxySessions terminates orphaned proxy sessions at a master node for all lost nodes (allNodes minus activeNodes).
// The session is orphaned when the origin node is gone.
func (c *Cluster) gcProxySessions(activeNodes []string) {
	allNodes := []string{c.thisNodeName}
	for name := range c.nodes {
		allNodes = append(allNodes, name)
	}
	_, failedNodes := stringSliceDelta(allNodes, activeNodes)
	for _, node := range failedNodes {
		// Iterate sessions of a failed node
		c.gcProxySessionsForNode(node)
	}
}

// gcProxySessionsForNode terminates orphaned proxy sessions at a master node for the given node.
// For example, a remote node is restarted or the cluster is rehashed without the node.
func (c *Cluster) gcProxySessionsForNode(node string) {
	n := c.nodes[node]
	n.lock.Lock()
	msess := n.msess
	n.msess = make(map[string]struct{})
	n.lock.Unlock()
	for sid := range msess {
		if sess := globals.sessionStore.Get(sid); sess != nil {
			sess.stop <- nil
		}
	}
}

// clusterWriteLoop implements write loop for multiplexing (proxy) session at a node which hosts master topic.
// The session is a multiplexing session, i.e. it handles requests for multiple sessions at origin.
func (sess *Session) clusterWriteLoop(forTopic string) {
	terminate := true
	defer func() {
		if terminate {
			sess.closeRPC()
			globals.sessionStore.Delete(sess)
			sess.inflightReqs = nil
			sess.unsubAll()
		}
	}()

	for {
		select {
		case msg, ok := <-sess.send:
			if !ok || sess.clnode.endpoint == nil {
				// channel closed
				return
			}
			srvMsg := msg.(*ServerComMessage)
			response := &ClusterResp{SrvMsg: srvMsg}
			if srvMsg.sess == nil {
				response.OrigSid = "*"
			} else {
				response.OrigReqType = srvMsg.sess.proxyReq
				response.OrigSid = srvMsg.sess.sid
				srvMsg.AsUser = srvMsg.sess.uid.UserId()

				switch srvMsg.sess.proxyReq {
				case ProxyReqJoin, ProxyReqLeave, ProxyReqMeta, ProxyReqBgSession, ProxyReqMeUserAgent:
				// Do nothing
				case ProxyReqBroadcast, ProxyReqNone:
					if srvMsg.Data != nil || srvMsg.Pres != nil || srvMsg.Info != nil {
						response.OrigSid = "*"
					} else if srvMsg.Ctrl == nil {
						logs.Warn.Println("cluster: request type not set in clusterWriteLoop", sess.sid,
							srvMsg.describe(), "src_sid:", srvMsg.sess.sid)
					}
				default:
					logs.Err.Panicln("cluster: unknown request type in clusterWriteLoop", srvMsg.sess.proxyReq)
				}
			}

			srvMsg.RcptTo = forTopic
			response.RcptTo = forTopic

			if err := sess.clnode.masterToProxyAsync(response); err != nil {
				logs.Warn.Printf("cluster: response to proxy failed \"%s\": %s", sess.sid, err.Error())
				return
			}
		case msg := <-sess.stop:
			logs.Info.Println("cluster: stop msg received - multi sid", sess.sid)
			if msg == nil {
				// Terminating multiplexing session.
				return
			}
			// There are two cases of msg != nil:
			//  * user is being deleted
			//  * node shutdown
			// In both cases the msg does not need to be forwarded to the proxy.

		case <-sess.detach:
			logs.Info.Println("cluster: detach msg received", sess.sid)
			return
		default:
			terminate = false
			return
		}
	}
}
