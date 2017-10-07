package main

import (
	"encoding/gob"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/rpc"
	"sync"
	"time"

	rh "github.com/tinode/chat/server/ringhash"
	"github.com/tinode/chat/server/store/types"
)

const DEFAULT_CLUSTER_RECONNECT = 200 * time.Millisecond
const CLUSTER_HASH_REPLICAS = 20

type ClusterNodeConfig struct {
	Name string `json:"name"`
	Addr string `json:"addr"`
}

type ClusterConfig struct {
	// List of all members of the cluster, including this member
	Nodes []ClusterNodeConfig `json:"nodes"`
	// Name of this cluster node
	ThisName string `json:"self"`
	// Failover configuration
	Failover *ClusterFailoverConfig
}

// Client connection to another node
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

	// A number of times this node has failed in a row
	failCount int

	// Channel for shutting down the runner; buffered, 1
	done chan bool
}

// Basic info on a remote session where the message was created
type ClusterSess struct {
	// IP address of the client. For long polling this is the IP of the last poll
	RemoteAddr string

	// User agent, a string provived by an authenticated client in {login} packet
	UserAgent string

	// ID of the current user or 0
	Uid types.Uid

	// User's authentication level
	AuthLvl int

	// Protocol version of the client: ((major & 0xff) << 8) | (minor & 0xff)
	Ver int

	// Human language of the client
	Lang string

	// Device ID
	DeviceId string

	// Session ID
	Sid string
}

// Proxy to Master request message
type ClusterReq struct {
	// Name of the node sending this request
	Node string

	// Ring hash signature of the node sending this request
	// Signature must match the signature of the receiver, otherwise the
	// Cluster is desynchronized.
	Signature string

	Msg *ClientComMessage
	// Expanded (routable) topic name
	RcptTo string
	// Originating session
	Sess *ClusterSess
	// True if the original session has disconnected
	SessGone bool
}

// Master to Proxy response message
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
			log.Printf("cluster: connection to '%s' established", n.name)
			return
		} else if count == 0 {
			reconnTicker = time.NewTicker(DEFAULT_CLUSTER_RECONNECT)
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

func (n *ClusterNode) call(proc string, msg interface{}, resp interface{}) error {
	if !n.connected {
		return errors.New("cluster: node '" + n.name + "' not connected")
	}

	if err := n.endpoint.Call(proc, msg, resp); err != nil {
		log.Printf("cluster: call failed to '%s' [%s]", n.name, err)

		n.lock.Lock()
		if n.connected {
			n.endpoint.Close()
			n.connected = false
			go n.reconnect()
		}
		n.lock.Unlock()
		return err
	}

	return nil
}

func (n *ClusterNode) callAsync(proc string, msg interface{}, resp interface{}, done chan *rpc.Call) *rpc.Call {
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
		select {
		case call := <-myDone:
			if call.Error != nil {
				n.lock.Lock()
				if n.connected {
					n.endpoint.Close()
					n.connected = false
					go n.reconnect()
				}
				n.lock.Unlock()
			}

			if done != nil {
				done <- call
			}
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
	rejected := false
	err := n.call("Cluster.Master", msg, &rejected)
	if err == nil && rejected {
		err = errors.New("cluster: master node out of sync")
	}
	return err
}

// Master responds to proxy
func (n *ClusterNode) respond(msg *ClusterResp) error {
	log.Printf("cluster: replying to node '%s'", n.name)
	unused := false
	return n.call("Cluster.Proxy", msg, &unused)
}

type Cluster struct {
	// Cluster nodes with RPC endpoints
	nodes map[string]*ClusterNode
	// Name of the local node
	thisNodeName string

	// Socket for inbound connections
	inbound *net.TCPListener
	// Ring hash for mapping topic names to nodes
	ring *rh.Ring

	// Failover parameters. Could be nil if failover is not enabled
	fo *ClusterFailover
}

// Cluster.Master at topic's master node receives C2S messages from topic's proxy nodes.
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

		if sess == nil {
			// If the session is not found, create it.
			node := globals.cluster.nodes[msg.Node]
			if node == nil {
				log.Println("cluster: request from an unknown node", msg.Node)
				return nil
			}

			sess = globals.sessionStore.Create(node, msg.Sess.Sid)
			go sess.rpcWriteLoop()
		}

		// Update session params which may have changed since the last call.
		sess.uid = msg.Sess.Uid
		sess.authLvl = msg.Sess.AuthLvl
		sess.ver = msg.Sess.Ver
		sess.userAgent = msg.Sess.UserAgent
		sess.remoteAddr = msg.Sess.RemoteAddr
		sess.lang = msg.Sess.Lang
		sess.deviceId = msg.Sess.DeviceId

		// Dispatch remote message to a local session.
		sess.dispatch(msg.Msg)
	} else {
		// Reject the request: wrong signature, cluster is out of sync.
		*rejected = true
	}

	return nil
}

// Proxy recieves messages from the master node addressed to a specific local session.
// Called by Session.writeRPC
func (Cluster) Proxy(msg *ClusterResp, unused *bool) error {
	log.Println("cluster: response from Master for session", msg.FromSID)

	// This cluster member received a response from topic owner to be forwarded to a session
	// Find appropriate session, send the message to it
	if sess := globals.sessionStore.Get(msg.FromSID); sess != nil {
		select {
		case sess.send <- msg.Msg:
		case <-time.After(time.Millisecond * 10):
			log.Println("cluster.Proxy: timeout")
		}
	} else {
		log.Println("cluster: master response for unknown session", msg.FromSID)
	}

	return nil
}

// Given topic name, find appropriate cluster node to route message to
func (c *Cluster) nodeForTopic(topic string) *ClusterNode {
	key := c.ring.Get(topic)
	if key == c.thisNodeName {
		log.Println("cluster: request to route to self")
		// Do not route to self
		return nil
	} else {
		node := globals.cluster.nodes[key]
		if node == nil {
			log.Println("cluster: no node for topic", topic, key)
		}
		return node
	}
}

func (c *Cluster) isRemoteTopic(topic string) bool {
	if c == nil {
		// Cluster not initialized, all topics are local
		return false
	}
	return c.ring.Get(topic) != c.thisNodeName
}

// Forward client message to the Master (cluster node which owns the topic)
func (c *Cluster) routeToTopic(msg *ClientComMessage, topic string, sess *Session) error {
	// Find the cluster node which owns the topic, then forward to it.
	n := c.nodeForTopic(topic)
	if n == nil {
		return errors.New("attempt to route to non-existent node")
	}

	// Save node name: it's need in order to inform relevant nodes when the session is disconnected
	if sess.nodes == nil {
		sess.nodes = make(map[string]bool)
	}
	sess.nodes[n.name] = true

	return n.forward(
		&ClusterReq{
			Node:      c.thisNodeName,
			Signature: c.ring.Signature(),
			Msg:       msg,
			RcptTo:    topic,
			Sess: &ClusterSess{
				Uid:        sess.uid,
				AuthLvl:    sess.authLvl,
				RemoteAddr: sess.remoteAddr,
				UserAgent:  sess.userAgent,
				Ver:        sess.ver,
				Lang:       sess.lang,
				DeviceId:   sess.deviceId,
				Sid:        sess.sid}})
}

// Session terminated at origin. Inform remote Master nodes that the session is gone.
func (c *Cluster) sessionGone(sess *Session) error {
	if c == nil {
		return nil
	}

	// Save node name: it's need in order to inform relevant nodes when the session is disconnected
	for name, _ := range sess.nodes {
		n := c.nodes[name]
		if n != nil {
			return n.forward(
				&ClusterReq{
					Node:     c.thisNodeName,
					SessGone: true,
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

func clusterInit(configString json.RawMessage, self *string) {
	if globals.cluster != nil {
		log.Fatal("Cluster already initialized")
	}

	// This is a standalone server, not initializing
	if configString == nil || len(configString) == 0 {
		log.Println("Running as a standalone server.")
		return
	}

	var config ClusterConfig
	if err := json.Unmarshal(configString, &config); err != nil {
		log.Fatal(err)
	}

	gob.Register([]interface{}{})
	gob.Register(map[string]interface{}{})

	thisName := *self
	if thisName == "" {
		thisName = config.ThisName
	}
	globals.cluster = &Cluster{
		thisNodeName: thisName,
		nodes:        make(map[string]*ClusterNode)}

	listenOn := ""
	for _, host := range config.Nodes {
		if host.Name == globals.cluster.thisNodeName {
			listenOn = host.Addr
			// Don't create a cluster member for this local instance
			continue
		}

		n := ClusterNode{
			address: host.Addr,
			name:    host.Name,
			done:    make(chan bool, 1)}
		go n.reconnect()

		globals.cluster.nodes[host.Name] = &n
	}

	if len(globals.cluster.nodes) == 0 {
		// Cluster needs at least two nodes.
		log.Fatal("Invalid cluster size: 1")
	}

	if !globals.cluster.failoverInit(config.Failover) {
		globals.cluster.rehash(nil)
	}

	addr, err := net.ResolveTCPAddr("tcp", listenOn)
	if err != nil {
		log.Fatal(err)
	}

	globals.cluster.inbound, err = net.ListenTCP("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	rpc.Register(globals.cluster)
	go rpc.Accept(globals.cluster.inbound)

	log.Printf("Cluster of %d nodes initialized, node '%s' listening on [%s]", len(globals.cluster.nodes)+1,
		globals.cluster.thisNodeName, listenOn)
}

// This is a session handler at a master node: forward messages from the master to the session origin.
func (sess *Session) rpcWriteLoop() {
	// There is no readLoop for RPC, delete the session here
	defer func() {
		log.Println("writeRPC - stop")
		sess.closeRPC()
		globals.sessionStore.Delete(sess)
		for _, sub := range sess.subs {
			// sub.done is the same as topic.unreg
			sub.done <- &sessionLeave{sess: sess, unsub: false}
		}
	}()
	var unused bool

	for {
		select {
		case msg, ok := <-sess.send:
			if !ok || sess.rpcnode.endpoint == nil {
				// channel closed
				return
			}
			// The error is returned if the remote node is down. Which means the remote
			// session is also disconnected.
			if err := sess.rpcnode.call("Cluster.Proxy",
				&ClusterResp{Msg: msg, FromSID: sess.sid}, &unused); err != nil {

				log.Println("sess.writeRPC: " + err.Error())
				return
			}
		case msg := <-sess.stop:
			// Shutdown is requested, don't care if the message is delivered
			if msg != nil {
				sess.rpcnode.call("Cluster.Proxy", &ClusterResp{Msg: msg, FromSID: sess.sid},
					&unused)
			}
			return

		case topic := <-sess.detach:
			delete(sess.subs, topic)
			if len(sess.subs) == 0 {
				// TODO(gene): the session is not connected to any topics here, shut it down
			}
		}
	}
}

// Proxied session is being closed at the Master node
func (s *Session) closeRPC() {
	if s.proto == RPC {
		log.Println("cluster: session closed at master")
	}
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
	ring := rh.New(CLUSTER_HASH_REPLICAS, nil)

	var ringKeys []string

	if nodes == nil {
		for _, node := range c.nodes {
			ringKeys = append(ringKeys, node.name)
		}
		ringKeys = append(ringKeys, c.thisNodeName)
	} else {
		for _, name := range nodes {
			ringKeys = append(ringKeys, name)
		}
	}
	ring.Add(ringKeys...)

	c.ring = ring

	return ringKeys
}
