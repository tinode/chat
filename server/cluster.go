package main

import (
	"encoding/gob"
	"encoding/json"
	"errors"
	"net"
	"net/rpc"
	"os"
	"sort"
	"strconv"
	"strings"
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
	// Network connection timeout.
	clusterNetworkTimeout = 3 * time.Second
	// Default timeout before attempting to reconnect to a node.
	clusterDefaultReconnectTime = 200 * time.Millisecond
	// Number of replicas in ringhash.
	clusterHashReplicas = 20
	// Buffer size for sending requests from proxy to master.
	clusterProxyToMasterBuffer = 64
	// Expand buffer size by this value for nodes over the basic 3-node setup.
	clusterProxyToMasterBufferPerNode = 16
	// Timeout for attempting to enqueue a proxy-to-master request when the buffer is full.
	clusterP2MTimeout = 20 * time.Millisecond
	// How long asyncRpcLoop keeps draining rpcDone after shutdown starts.
	clusterAsyncRPCDrainTimeout = 500 * time.Millisecond
	// Buffer size for receiving responses from other nodes, per node.
	clusterRpcCompletionBuffer = 64
)

// ProxyReqType is the type of proxy requests.
type ProxyReqType int

// Individual request types.
const (
	ProxyReqNone      ProxyReqType = iota
	ProxyReqJoin                   // {sub}.
	ProxyReqLeave                  // {leave}
	ProxyReqMeta                   // {meta set|get}
	ProxyReqBroadcast              // {pub}, {note}
	ProxyReqBgSession
	ProxyReqMeUserAgent
	ProxyReqCall // Used in video call proxy sessions for routing call events.
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
	// Discovery selects how cluster membership is sourced. Missing => static
	// mode using the `Nodes` list above, which preserves legacy behavior.
	Discovery *discoveryConfig `json:"discovery,omitempty"`
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
	// True if the node is no longer part of the active peer set
	retired bool
	// TCP address in the form host:port
	address string
	// Name of the node
	name string
	// Fingerprint of the node: unique value which changes when the node restarts.
	fingerprint int64

	// A number of times this node has failed in a row
	failCount int

	// Channel for shutting down the runner. Closed by stopOnce.
	done     chan bool
	stopOnce sync.Once

	// IDs of multiplexing sessions belonging to this node.
	msess map[string]struct{}

	// Default channel for receiving responses to RPC calls issued by this node.
	// Buffered, clusterRpcCompletionBuffer * number_of_nodes.
	rpcDone chan *rpc.Call

	// Channel for sending proxy to master requests; buffered, clusterProxyToMasterBuffer.
	p2mSender chan *ClusterReq
}

func (n *ClusterNode) asyncRpcLoop() {
	draining := false
	var idle *time.Timer

	for {
		if !draining {
			select {
			case <-n.done:
				// Retirement/shutdown starts by closing done, but we continue
				// draining rpcDone for a bounded idle window so late Client.Go
				// completions do not block on the shared channel after the
				// endpoint is closed.
				draining = true
				n.lock.Lock()
				n.closeEndpointLocked()
				n.connected = false
				n.reconnecting = false
				n.lock.Unlock()
				idle = time.NewTimer(clusterAsyncRPCDrainTimeout)
				continue
			case call, ok := <-n.rpcDone:
				if !ok {
					return
				}
				n.handleRpcResponse(call)
				continue
			}
		}

		select {
		case call, ok := <-n.rpcDone:
			if idle != nil && !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			if !ok {
				return
			}
			n.handleRpcResponse(call)
			if idle != nil {
				idle.Reset(clusterAsyncRPCDrainTimeout)
			}
		case <-idle.C:
			return
		}
	}
}

func (n *ClusterNode) p2mSenderLoop() {
	for {
		select {
		case <-n.done:
			for {
				select {
				case req, ok := <-n.p2mSender:
					if !ok || req == nil {
						return
					}
					if err := n.proxyToMaster(req); err != nil {
						logs.Warn.Println("p2mSenderLoop: call failed", n.name, err)
					}
				default:
					return
				}
			}
		case req, ok := <-n.p2mSender:
			if !ok || req == nil {
				// Stop
				return
			}

			if err := n.proxyToMaster(req); err != nil {
				logs.Warn.Println("p2mSenderLoop: call failed", n.name, err)
			}
		}
	}
}

// closeEndpointLocked closes and clears the current RPC client.
// Caller must hold n.lock.
func (n *ClusterNode) closeEndpointLocked() {
	if n.endpoint != nil {
		n.endpoint.Close()
		n.endpoint = nil
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
	if n.retired {
		n.lock.Unlock()
		return
	}
	select {
	case <-n.done:
		n.lock.Unlock()
		return
	default:
	}
	if n.reconnecting {
		n.lock.Unlock()
		return
	}
	n.reconnecting = true
	n.lock.Unlock()

	count := 0
	for {
		// Attempt to reconnect right away
		if conn, err := net.DialTimeout("tcp", n.address, clusterNetworkTimeout); err == nil {
			if reconnTicker != nil {
				reconnTicker.Stop()
			}
			n.lock.Lock()
			if n.retired {
				n.reconnecting = false
				n.lock.Unlock()
				conn.Close()
				return
			}
			select {
			case <-n.done:
				n.reconnecting = false
				n.lock.Unlock()
				conn.Close()
				return
			default:
			}
			n.endpoint = rpc.NewClient(conn)
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
			reconnTicker = time.NewTicker(clusterDefaultReconnectTime)
		}

		count++

		select {
		case <-reconnTicker.C:
			// Wait for timer to try to reconnect again. Do nothing if the timer is inactive.
		case <-n.done:
			// Shutting down
			logs.Info.Println("cluster: shutdown started at node", n.name)
			reconnTicker.Stop()
			n.lock.Lock()
			n.closeEndpointLocked()
			n.connected = false
			n.reconnecting = false
			n.lock.Unlock()
			logs.Info.Println("cluster: shut down completed at node", n.name)
			return
		}
	}
}

func (n *ClusterNode) call(proc string, req, resp any) error {
	n.lock.Lock()
	retired := n.retired
	connected := n.connected
	endpoint := n.endpoint
	n.lock.Unlock()
	if retired || !connected {
		return errors.New("cluster: node '" + n.name + "' not connected")
	}

	if err := endpoint.Call(proc, req, resp); err != nil {
		logs.Warn.Println("cluster: call failed", n.name, err)

		n.lock.Lock()
		if n.connected && !n.retired {
			n.closeEndpointLocked()
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
		if n.connected && !n.retired {
			n.closeEndpointLocked()
			n.connected = false
			statsInc("LiveClusterNodes", -1)
			go n.reconnect()
		}
		n.lock.Unlock()
	}
}

func (n *ClusterNode) callAsync(proc string, req, resp any, done chan *rpc.Call) *rpc.Call {
	if done != nil && cap(done) == 0 {
		logs.Err.Panic("cluster: RPC done channel is unbuffered")
	}

	n.lock.Lock()
	if n.retired || !n.connected {
		n.lock.Unlock()
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
	endpoint := n.endpoint

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

	call := endpoint.Go(proc, req, resp, responseChan)
	n.lock.Unlock()

	return call
}

// proxyToMaster forwards request from topic proxy to topic master.
func (n *ClusterNode) proxyToMaster(msg *ClusterReq) error {
	var rejected bool
	err := n.call("Cluster.TopicMaster", msg, &rejected)
	if err == nil && rejected {
		err = errors.New("cluster: topic master node out of sync")
	}
	return err
}

// proxyToMaster forwards request from topic proxy to topic master.
func (n *ClusterNode) proxyToMasterAsync(msg *ClusterReq) error {
	n.lock.Lock()
	if n.retired {
		n.lock.Unlock()
		return errors.New("cluster: node '" + n.name + "' not connected")
	}

	select {
	case n.p2mSender <- msg:
		n.lock.Unlock()
		return nil
	default:
	}
	// Buffer is full. Wait briefly before giving up.
	timer := time.NewTimer(clusterP2MTimeout)
	defer timer.Stop()
	select {
	case n.p2mSender <- msg:
		n.lock.Unlock()
		return nil
	case <-timer.C:
		n.lock.Unlock()
		return errors.New("cluster: load exceeded")
	}
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
	// Mutated at runtime by the Kubernetes discovery reconciler (Phase D).
	// Must be accessed under nodesLock together with ring so routing sees a
	// coherent membership snapshot.
	nodes map[string]*ClusterNode
	// nodesLock guards reads and writes of nodes and ring. In static mode the
	// map is populated once at init and never mutated, so the lock is inert;
	// readers take it uniformly to keep runtime membership swaps coherent when
	// k8s mode is on.
	nodesLock sync.RWMutex
	// Name of the local node
	thisNodeName string
	// Fingerprint of the local node
	fingerprint int64

	// Resolved address to listed on
	listenOn string

	// Socket for inbound connections
	inbound *net.TCPListener
	// Ring hash for mapping topic names to nodes. Published atomically with
	// nodes under nodesLock.
	ring *rh.Ring

	// Failover parameters. Could be nil if failover is not enabled
	fo *clusterFailover

	// Thread pool to use for running proxy session (write) event processing logic.
	// The number of proxy sessions grows as O(number of topics x number of cluster nodes).
	// In large Tinode deployments (10s of thousands of topics, tens of nodes),
	// running a separate event processing goroutine for each proxy session
	// leads to a rather large memory usage and excessive scheduling overhead.
	proxyEventQueue *concurrency.GoRoutinePool

	// discovery is the source of cluster membership. In static mode it is a
	// staticDiscovery emitting one snapshot from tinode.conf. In kubernetes
	// mode (Phase D) it is a kubernetesDiscovery driven by an EndpointSlice
	// informer. Must be non-nil once clusterInit returns a clustered mode.
	discovery Discovery
}

func (n *ClusterNode) stopMultiplexingSession(msess *Session) {
	if msess == nil {
		return
	}
	msess.stopSession(nil)
	n.lock.Lock()
	delete(n.msess, msess.sid)
	n.lock.Unlock()
}

// TopicMaster is a gRPC endpoint which receives requests sent by proxy topic to master topic.
func (c *Cluster) TopicMaster(msg *ClusterReq, rejected *bool) error {
	*rejected = false

	c.nodesLock.RLock()
	node := c.nodes[msg.Node]
	c.nodesLock.RUnlock()
	if node == nil {
		logs.Warn.Println("cluster TopicMaster: request from an unknown node", msg.Node)
		return nil
	}

	// Master maintains one multiplexing session per proxy topic per node.
	// Except channel topics:
	// * one multiplexing session for channel subscriptions.
	// * one multiplexing session for group subscriptions.
	var msid string
	if msg.CliMsg != nil && types.IsChannel(msg.CliMsg.Original) {
		// If it's a channel request, use channel name.
		msid = msg.CliMsg.Original
	} else {
		msid = msg.RcptTo
	}
	// Append node name.
	msid += "-" + msg.Node
	msess := globals.sessionStore.Get(msid)

	if msg.Gone {
		// Proxy topic is gone. Tear down the local auxiliary session.
		// If it was the last session, master topic will shut down as well.
		node.stopMultiplexingSession(msess)

		if t := globals.hub.topicGet(msg.RcptTo); t != nil && t.isChan {
			// If it's a channel topic, also stop the "chnX-" local auxiliary session.
			msidChn := types.GrpToChn(t.name) + "-" + msg.Node
			node.stopMultiplexingSession(globals.sessionStore.Get(msidChn))
		}

		return nil
	}

	if msg.Signature != c.ringSignature() {
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
		msg.CliMsg.init = true
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
func (*Cluster) TopicProxy(msg *ClusterResp, unused *bool) error {
	// This cluster member received a response from the topic master to be forwarded to the topic.
	// Find appropriate topic, send the message to it.
	if t := globals.hub.topicGet(msg.RcptTo); t != nil {
		msg.SrvMsg.uid = types.ParseUserId(msg.SrvMsg.AsUser)
		select {
		case t.proxy <- msg:
		default:
			logs.Warn.Printf("cluster: proxy channel full, topic %s", msg.RcptTo)
		}
	} else {
		logs.Warn.Println("cluster: master response for unknown topic", msg.RcptTo)
	}

	return nil
}

// Route endpoint receives intra-cluster messages destined for the nodes hosting the topic.
// Called by Hub.route channel consumer for messages send without attaching to topic first.
func (c *Cluster) Route(msg *ClusterRoute, rejected *bool) error {
	logError := func(err string) {
		sid := ""
		if msg.Sess != nil {
			sid = msg.Sess.Sid
		}
		logs.Warn.Println(err, sid)
		*rejected = true
	}

	*rejected = false
	if msg.Signature != c.ringSignature() {
		logError("cluster Route: session signature mismatch")
		return nil
	}

	if msg.SrvMsg == nil {
		// TODO: maybe panic here.
		logError("cluster Route: nil server message")
		return nil
	}

	select {
	case globals.hub.routeSrv <- msg.SrvMsg:
	default:
		logError("cluster Route: server busy")
	}
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
	c.nodesLock.RLock()
	node := c.nodes[ping.Node]
	c.nodesLock.RUnlock()
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
		c.nodesLock.RLock()
		for _, n := range c.nodes {
			reqByNode[n.name] = r
		}
		c.nodesLock.RUnlock()
	}

	if len(reqByNode) > 0 {
		for nodeName, r := range reqByNode {
			c.nodesLock.RLock()
			n := c.nodes[nodeName]
			c.nodesLock.RUnlock()
			if n == nil {
				continue
			}
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
	c.nodesLock.RLock()
	key := c.ring.Get(topic)
	if key == c.thisNodeName {
		c.nodesLock.RUnlock()
		logs.Err.Println("cluster: request to route to self")
		// Do not route to self
		return nil
	}
	node := c.nodes[key]
	c.nodesLock.RUnlock()
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
	_, remote := c.topicOwner(topic)
	return remote
}

// topicOwner returns the cluster node that owns the topic and whether that
// owner is remote, using a single ring snapshot.
func (c *Cluster) topicOwner(topic string) (string, bool) {
	if c == nil {
		// Cluster not initialized, all topics are local.
		return "", false
	}
	c.nodesLock.RLock()
	owner := c.ring.Get(topic)
	remote := owner != c.thisNodeName
	c.nodesLock.RUnlock()
	return owner, remote
}

// genLocalTopicName is just like genTopicName(), but the generated name belongs to the current cluster node.
func (c *Cluster) genLocalTopicName() string {
	topic := genTopicName()
	if c == nil {
		// Cluster not initialized, all topics are local
		return topic
	}

	// TODO: if cluster is large it may become too inefficient.
	for c.ringOwner(topic) != c.thisNodeName {
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
		Signature:   c.ringSignature(),
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

	if sess != nil && reqType != ProxyReqLeave {
		if atomic.LoadInt32(&sess.terminating) > 0 {
			// The session is terminating.
			// Do not forward any requests except "leave" to the topic master.
			return nil
		}
	}

	req := c.makeClusterReq(reqType, msg, topic, sess)

	// Find the cluster node which owns the topic, then forward to it.
	n := c.nodeForTopic(topic)
	if n == nil {
		return errors.New("node for topic not found")
	}
	return n.proxyToMasterAsync(req)
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
		Signature:   c.ringSignature(),
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
	return n.proxyToMasterAsync(req)
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
	// Discovery mode: 0 = static / no cluster, 1 = kubernetes.
	// Operators alerting on cluster health can use this to branch between
	// "leader election must succeed" (ClusterLeader=1 in static mode) and
	// "ring must match EndpointSlice count" (TotalClusterNodes in k8s mode).
	statsRegisterInt("ClusterDiscoveryMode")

	// This is a standalone server, not initializing
	if len(configString) == 0 {
		logs.Info.Println("Cluster: running as a standalone server.")
		return 1
	}

	var config clusterConfig
	if err := json.Unmarshal(configString, &config); err != nil {
		logs.Err.Fatal(err)
	}

	// Select discovery mode. An absent `discovery` block or `mode: static`
	// yields a staticDiscovery over config.Nodes, which reproduces the legacy
	// behavior bit-for-bit.
	mode := "static"
	if config.Discovery != nil && config.Discovery.Mode != "" {
		mode = config.Discovery.Mode
	}

	thisName := resolvePodName(mode, self, config.ThisName)
	// Name of the current node is not specified: clustering disabled.
	if mode != "kubernetes" && thisName == "" {
		logs.Info.Println("Cluster: running as a standalone server.")
		return 1
	}

	gob.Register([]any{})
	gob.Register(map[string]any{})
	gob.Register(map[string]int{})
	gob.Register(map[string]string{})
	gob.Register(MsgAccessMode{})

	if config.NumProxyEventGoRoutines != 0 {
		logs.Warn.Println("Cluster config: field num_proxy_event_goroutines is deprecated.")
	}

	var discovery Discovery
	switch mode {
	case "static":
		discovery = newStaticDiscovery(config.Nodes)
	case "kubernetes":
		if config.Discovery == nil || config.Discovery.Kubernetes == nil {
			logs.Err.Fatal("Cluster: discovery.kubernetes config block is required when mode=kubernetes")
		}
		kd, err := newKubernetesDiscovery(*config.Discovery.Kubernetes)
		if err != nil {
			logs.Err.Fatalf("Cluster: %v", err)
		}
		discovery = kd
	default:
		logs.Err.Fatalf("Cluster: unsupported discovery mode %q", mode)
	}

	snapshot := discovery.Snapshot()

	globals.cluster = &Cluster{
		thisNodeName:    thisName,
		fingerprint:     time.Now().Unix(),
		nodes:           make(map[string]*ClusterNode),
		proxyEventQueue: concurrency.NewGoRoutinePool(max(len(snapshot), 1) * 5),
		discovery:       discovery,
	}

	var nodeNames []string
	for _, ep := range snapshot {
		nodeNames = append(nodeNames, ep.Name)

		if ep.Name == thisName {
			if mode != "kubernetes" {
				globals.cluster.listenOn = ep.Addr
			}
			// Don't create a cluster member for this local instance
			continue
		}

		globals.cluster.nodes[ep.Name] = &ClusterNode{
			address: ep.Addr,
			name:    ep.Name,
			done:    make(chan bool, 1),
			msess:   make(map[string]struct{}),
		}
	}

	if mode == "kubernetes" {
		// K8s mode still derives peer membership from the API server, but the
		// local cluster RPC listener binds the explicitly configured self_port.
		globals.cluster.listenOn = k8sListenAddr(config.Discovery.Kubernetes.SelfPort)
	} else {
		if len(globals.cluster.nodes) == 0 {
			// Cluster needs at least two nodes.
			logs.Err.Fatal("Cluster: invalid cluster size: 1")
		}
		if len(globals.cluster.nodes)%2 == 1 {
			// Even number of cluster nodes (self + odd number).
			logs.Warn.Println("Cluster: use odd number of cluster nodes")
		}
	}

	// failoverInit is a no-op in kubernetes mode: we skip it explicitly rather
	// than letting the leader election goroutine start. Membership is driven by
	// the discovery reconciler (started from Cluster.start()).
	var workerId int
	switch mode {
	case "static":
		if !globals.cluster.failoverInit(config.Failover) {
			globals.cluster.rehash(nil)
		}
		sort.Strings(nodeNames)
		workerId = sort.SearchStrings(nodeNames, thisName) + 1
		statsSet("ClusterDiscoveryMode", 0)
	case "kubernetes":
		// In k8s mode the reconciler will rehash on each snapshot, but we need
		// a valid initial ring before start() accepts traffic.
		globals.cluster.rehash(nil)
		workerId = k8sWorkerId(thisName)
		// Leader election is disabled in k8s mode; pin the gauge so operators
		// alerting on "no leader for N minutes" don't false-positive.
		statsSet("ClusterLeader", 0)
		statsSet("ClusterDiscoveryMode", 1)
	}

	statsSet("TotalClusterNodes", int64(len(globals.cluster.nodes)+1))

	return workerId
}

// resolvePodName picks the local cluster identity for the selected discovery
// mode. In kubernetes mode the identity is derived from POD_NAME; in static
// mode it follows the legacy CLI flag -> config fallback order.
func resolvePodName(mode string, self *string, configThisName string) string {
	// In kubernetes mode the node name is derived from the pod (not from the
	// static config / CLI flag), and the listen address is bound to all
	// interfaces since peers dial via the pod IP published in EndpointSlices.
	if mode == "kubernetes" {
		if *self != "" || configThisName != "" {
			logs.Warn.Println("Cluster: ignoring cluster_self / self config in kubernetes mode; identity is derived from POD_NAME")
		}

		thisName := os.Getenv("POD_NAME")
		if thisName == "" {
			logs.Err.Fatal("Cluster: POD_NAME env var is required in kubernetes mode (inject via downward API)")
		}
		return thisName
	}

	if *self != "" {
		return *self
	}
	return configThisName
}

// k8sWorkerId derives the snowflake worker ID for a pod in kubernetes mode.
//
// Numeric suffix of podName, e.g. "tinode-2" → 2, yielding workerId 3.
// Fails the process (log.Fatal) on any validation error. The pod-name
// fallback guards against accidental Deployment usage: random ReplicaSet
// suffixes do not match the StatefulSet ordinal pattern and will fail here.
func k8sWorkerId(podName string) int {
	ord, ok := parseWorkerID(podName)
	if !ok {
		logs.Err.Fatalf("Cluster: POD_NAME %q does not match `<name>-<non-negative-integer>` (StatefulSet required in kubernetes mode)", podName)
	}

	workerId := ord + 1
	if workerId >= 1024 {
		logs.Err.Fatalf("Cluster: pod ordinal %d yields worker ID ≥ 1024 (snowflake 10-bit limit)", ord)
	}
	return workerId
}

func k8sListenAddr(port int) string {
	return ":" + strconv.Itoa(port)
}

func parseWorkerID(podName string) (int, bool) {
	idx := strings.LastIndex(podName, "-")
	if idx < 0 || idx == len(podName)-1 {
		return 0, false
	}
	suffix := podName[idx+1:]
	n, err := strconv.Atoi(suffix)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func portOnlyFromAddr(addr string) string {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return addr
	}
	return addr[idx+1:]
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

	var bufferSize = clusterProxyToMasterBuffer
	if len(c.nodes) > 2 {
		// Expand the buffer for larger (>3 node) clusters.
		bufferSize += clusterProxyToMasterBufferPerNode * (len(c.nodes) - 2)
	}
	for _, n := range c.nodes {
		go n.reconnect()
		n.rpcDone = make(chan *rpc.Call, len(c.nodes)*clusterRpcCompletionBuffer)
		n.p2mSender = make(chan *ClusterReq, bufferSize)
		go n.asyncRpcLoop()
		go n.p2mSenderLoop()
	}

	if c.fo != nil {
		go c.run()
	}

	err = rpc.Register(c)
	if err != nil {
		logs.Err.Fatal(err)
	}

	go rpc.Accept(c.inbound)

	// Start the discovery reconciler for modes that drive membership at
	// runtime (currently only kubernetes). staticDiscovery's Subscribe()
	// channel is closed, so runReconciler would return immediately anyway,
	// but we skip it to make the intent explicit.
	if _, ok := c.discovery.(*kubernetesDiscovery); ok {
		go c.runReconciler()
	}

	logs.Info.Printf("Cluster of %d nodes initialized, node '%s' is listening on [%s]", len(globals.cluster.nodes)+1,
		globals.cluster.thisNodeName, c.listenOn)
}

// runReconciler loops on the discovery subscription and applies membership
// snapshots to the cluster by atomically publishing the next nodes+ring
// snapshot, then invalidating proxy subs whose master moved and
// garbage-collecting orphaned multiplexing sessions.
//
// Intended for kubernetes mode. Returns when the discovery Subscribe()
// channel closes (i.e., Discovery.Stop() has been called).
func (c *Cluster) runReconciler() {
	logs.Info.Println("cluster: reconciler loop started")
	for snapshot := range c.discovery.Subscribe() {
		c.applySnapshot(snapshot)
	}
	logs.Info.Println("cluster: reconciler loop exited")
}

// applySnapshot diffs the incoming endpoint list against the current
// membership, builds the next nodes+ring snapshot off-lock, starts newly
// provisioned peers, publishes the swap atomically under nodesLock, then
// retires replaced peers.
func (c *Cluster) applySnapshot(snapshot []NodeEndpoint) {
	// Compute the target set of peer names (snapshot minus self).
	targetNames := make(map[string]string, len(snapshot))
	for _, ep := range snapshot {
		if ep.Name == c.thisNodeName {
			continue
		}
		targetNames[ep.Name] = ep.Addr
	}

	c.nodesLock.RLock()
	currentNodes := make(map[string]*ClusterNode, len(c.nodes))
	for name, node := range c.nodes {
		currentNodes[name] = node
	}
	c.nodesLock.RUnlock()

	activeNames := make([]string, 0, len(targetNames)+1)
	activeNames = append(activeNames, c.thisNodeName)
	nextNodes := make(map[string]*ClusterNode, len(targetNames))
	newNodes := make([]*ClusterNode, 0, len(targetNames))
	retiredNodes := make([]*ClusterNode, 0, len(currentNodes))
	desiredPeerCount := len(targetNames)

	for name, addr := range targetNames {
		activeNames = append(activeNames, name)

		if existing := currentNodes[name]; existing != nil && existing.address == addr {
			nextNodes[name] = existing
			continue
		}

		node := c.prepareClusterNode(name, addr, desiredPeerCount)
		nextNodes[name] = node
		newNodes = append(newNodes, node)

		if existing := currentNodes[name]; existing != nil {
			retiredNodes = append(retiredNodes, existing)
		}
	}
	for name, node := range currentNodes {
		if _, keep := nextNodes[name]; !keep {
			retiredNodes = append(retiredNodes, node)
		}
	}

	nextRing := buildRing(activeNames)

	for _, node := range newNodes {
		c.startClusterNode(node)
	}

	c.nodesLock.Lock()
	c.nodes = nextNodes
	c.ring = nextRing
	c.nodesLock.Unlock()

	c.invalidateProxySubs("")
	for _, node := range retiredNodes {
		c.gcProxySessionsOnNode(node)
	}

	// Update stats gauges. TotalClusterNodes includes self.
	statsSet("TotalClusterNodes", int64(len(targetNames)+1))

	// Nudge the hub to reflect the new ring (topics may have re-homed).
	// Hub may be nil during tests that exercise applySnapshot in isolation.
	if globals.hub != nil {
		select {
		case globals.hub.rehash <- true:
		default:
		}
	}

	for _, node := range retiredNodes {
		stopClusterNode(node)
	}
}

func (c *Cluster) shutdown() {
	if globals.cluster == nil {
		return
	}
	// Stop the discovery source first so the reconciler stops scheduling
	// add/remove operations against the peer map we are about to tear down.
	// Safe to call on staticDiscovery (no-op).
	if c.discovery != nil {
		c.discovery.Stop()
	}

	c.nodesLock.RLock()
	peersSnapshot := make([]*ClusterNode, 0, len(c.nodes))
	for _, n := range c.nodes {
		peersSnapshot = append(peersSnapshot, n)
	}
	c.nodesLock.RUnlock()

	for _, n := range peersSnapshot {
		stopClusterNode(n)
	}

	globals.cluster.proxyEventQueue.Stop()
	globals.cluster = nil

	c.inbound.Close()

	if c.fo != nil {
		c.fo.done <- true
	}

	logs.Info.Println("Cluster shut down")
}

// Recalculate the ring hash using provided list of nodes or only nodes in a non-failed state.
// Returns the list of nodes used for ring hash.
func (c *Cluster) rehash(nodes []string) []string {
	var ringKeys []string

	if nodes == nil {
		c.nodesLock.RLock()
		for _, node := range c.nodes {
			ringKeys = append(ringKeys, node.name)
		}
		c.nodesLock.RUnlock()
		ringKeys = append(ringKeys, c.thisNodeName)
	} else {
		ringKeys = append(ringKeys, nodes...)
	}
	ring := buildRing(ringKeys)
	c.nodesLock.Lock()
	c.ring = ring
	c.nodesLock.Unlock()

	return ringKeys
}

func buildRing(ringKeys []string) *rh.Ring {
	ring := rh.New(clusterHashReplicas, nil)
	ring.Add(ringKeys...)
	return ring
}

// addClusterNode provisions a new peer at runtime: allocates RPC channels,
// spawns asyncRpcLoop / p2mSenderLoop, and starts a reconnect goroutine that
// dials the peer address. Returns the provisioned node.
//
// Kept as a helper for targeted runtime add/remove operations and unit tests.
// The Kubernetes reconciler publishes full membership snapshots via
// applySnapshot instead of mutating the peer set incrementally. Does nothing
// and returns nil if a node with `name` already exists.
func (c *Cluster) addClusterNode(name, addr string) *ClusterNode {
	if name == c.thisNodeName {
		return nil
	}

	c.nodesLock.Lock()
	if existing, ok := c.nodes[name]; ok {
		c.nodesLock.Unlock()
		return existing
	}
	node := c.prepareClusterNode(name, addr, len(c.nodes)+1)
	c.nodes[name] = node
	c.nodesLock.Unlock()

	c.nodesLock.RLock()
	stillCurrent := c.nodes[name] == node
	c.nodesLock.RUnlock()
	if stillCurrent {
		c.startClusterNode(node)
	}

	logs.Info.Printf("cluster: added peer '%s' at %s", name, addr)
	return node
}

func (c *Cluster) prepareClusterNode(name, addr string, peerCount int) *ClusterNode {
	bufferSize := clusterProxyToMasterBuffer
	if peerCount > 2 {
		bufferSize += clusterProxyToMasterBufferPerNode * (peerCount - 2)
	}
	rpcBufSize := max(peerCount, 1) * clusterRpcCompletionBuffer

	return &ClusterNode{
		address:   addr,
		name:      name,
		done:      make(chan bool, 1),
		msess:     make(map[string]struct{}),
		rpcDone:   make(chan *rpc.Call, rpcBufSize),
		p2mSender: make(chan *ClusterReq, bufferSize),
	}
}

func (c *Cluster) startClusterNode(node *ClusterNode) {
	go node.reconnect()
	go node.asyncRpcLoop()
	go node.p2mSenderLoop()
}

// removeClusterNode tears down a peer at runtime: removes it from c.nodes,
// closes its RPC client, and signals the runner goroutines to exit. Safe to
// call on an already-removed name.
//
// Kept as a helper for targeted runtime add/remove operations and unit tests.
// The Kubernetes reconciler publishes full membership snapshots via
// applySnapshot instead of mutating the peer set incrementally.
func (c *Cluster) removeClusterNode(name string) {
	c.nodesLock.Lock()
	node, ok := c.nodes[name]
	if !ok {
		c.nodesLock.Unlock()
		return
	}
	delete(c.nodes, name)
	c.nodesLock.Unlock()

	stopClusterNode(node)
	logs.Info.Printf("cluster: removed peer '%s'", name)
}

func stopClusterNode(node *ClusterNode) {
	node.lock.Lock()
	node.retired = true
	node.closeEndpointLocked()
	if node.connected {
		statsInc("LiveClusterNodes", -1)
	}
	node.connected = false
	node.reconnecting = false
	node.lock.Unlock()

	if node.done != nil {
		node.stopOnce.Do(func() {
			// Closing done starts bounded post-shutdown draining in
			// asyncRpcLoop; it does not abandon rpcDone immediately.
			close(node.done)
		})
	}
}

// invalidateProxySubs iterates over sessions proxied on this node and for each session
// sends "{pres term}" informing that the topic subscription (attachment) was lost:
// - Called immediately after Cluster.rehash() for all relocated topics (forNode == "").
// - Called for topics hosted at a specific node when a node restart is detected.
// TODO: consider resubscribing to topics instead of forcing sessions to resubscribe.
func (c *Cluster) invalidateProxySubs(forNode string) {
	sessions := make(map[*Session][]string)
	globals.hub.topics.Range(func(_, v any) bool {
		topic := v.(*Topic)
		if !topic.isProxy {
			// Topic isn't a proxy.
			return true
		}
		if forNode == "" {
			if topic.masterNode == c.ringOwner(topic.name) {
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
	c.nodesLock.RLock()
	for name := range c.nodes {
		allNodes = append(allNodes, name)
	}
	c.nodesLock.RUnlock()
	_, failedNodes, _ := stringSliceDelta(allNodes, activeNodes)
	for _, node := range failedNodes {
		// Iterate sessions of a failed node
		c.gcProxySessionsForNode(node)
	}
}

// gcProxySessionsForNode terminates orphaned proxy sessions at a master node for the given node.
// For example, a remote node is restarted or the cluster is rehashed without the node.
func (c *Cluster) gcProxySessionsForNode(node string) {
	c.nodesLock.RLock()
	n := c.nodes[node]
	c.nodesLock.RUnlock()
	if n == nil {
		return
	}
	c.gcProxySessionsOnNode(n)
}

func (c *Cluster) gcProxySessionsOnNode(node *ClusterNode) {
	if node == nil {
		return
	}
	node.lock.Lock()
	msess := node.msess
	node.msess = make(map[string]struct{})
	node.lock.Unlock()
	for sid := range msess {
		if sess := globals.sessionStore.Get(sid); sess != nil {
			sess.stop <- nil
		}
	}
}

func (c *Cluster) ringSignature() string {
	c.nodesLock.RLock()
	sig := c.ring.Signature()
	c.nodesLock.RUnlock()
	return sig
}

func (c *Cluster) ringOwner(topic string) string {
	c.nodesLock.RLock()
	owner := c.ring.Get(topic)
	c.nodesLock.RUnlock()
	return owner
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
				case ProxyReqJoin, ProxyReqLeave, ProxyReqMeta, ProxyReqBgSession, ProxyReqMeUserAgent, ProxyReqCall:
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
			if msg == nil {
				// Terminating multiplexing session.
				return
			}
			// There are two cases of msg != nil:
			//  * user is being deleted
			//  * node shutdown
			// In both cases the msg does not need to be forwarded to the proxy.

		case <-sess.detach:
			return
		default:
			terminate = false
			return
		}
	}
}
