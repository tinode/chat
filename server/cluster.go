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

const DEFAULT_CLUSTER_RECONNECT = 1000 * time.Millisecond
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
	// Address:Port to listen on for incoming requests
	ListenOn string `json:"listen"`
}

type ClusterNode struct {
	lock sync.Mutex

	// RPC endpoint
	endpoint *rpc.Client
	// true if the endpoint is believed to be connected
	connected bool
	// Error returned by the last call, could be nil
	lastError error
	// TCP address in the form host:port
	address string
	// Name of the node
	name string

	// signals to shut down the runner; buffered, 1
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

	// Protocol version of the client: ((major & 0xff) << 8) | (minor & 0xff)
	Ver int

	// Session ID
	Sid string
}

// Proxy to Master request message
type ClusterReq struct {
	// Name of the node which sent the request
	Node string

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
// FIXME(gene): this will drain the outbound queue in case of a failure. Maybe it's a good thing, maybe not.
func (n *ClusterNode) reconnect() {
	var reconnTicker *time.Ticker

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
			n.endpoint.Close()
			n.connected = false
			log.Printf("cluster: node '%s' shut down completed", n.name)
			return
		}
	}
}

func (n *ClusterNode) call(proc string, msg interface{}) error {
	if !n.connected {
		return errors.New("cluster node not connected")
	}

	var unused bool
	if err := n.endpoint.Call(proc, msg, &unused); err != nil {
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

	log.Printf("cluster: Cluster.Master to '%s' successful", n.name)
	return nil
}

// Proxy forwards message to master
func (n *ClusterNode) forward(msg *ClusterReq) error {
	log.Printf("cluster: forwarding request to node '%s'", n.name)
	msg.Node = globals.cluster.thisNodeName
	return n.call("Cluster.Master", msg)
}

// Master responds to proxy
func (n *ClusterNode) respond(msg *ClusterResp) error {
	log.Printf("cluster: replying to node '%s'", n.name)
	return n.call("Cluster.Proxy", msg)
}

type Cluster struct {
	// List of RPC endpoints
	nodes map[string]*ClusterNode
	// Socket for inbound connections
	inbound *net.TCPListener
	// Ring hash for mapping topic names to nodes
	ring *rh.Ring
	// Name of the local node
	thisNodeName string
}

// Cluster.Master at topic's master node receives C2S messages from topic's proxy nodes.
// The message is treated like it came from a session: find or create a session locally,
// dispatch the message to it like it came from a normal session.
// Called by a remote node.
func (Cluster) Master(msg *ClusterReq, unused *bool) error {
	log.Printf("cluster: Master request received from node '%s'", msg.Node)

	// Find the local session associated with the given remote session.
	sess := globals.sessionStore.Get(msg.Sess.Sid)

	if msg.SessGone {
		// Original session has disconnected. Tear down the local proxied session.
		if sess != nil {
			sess.stop <- nil
		}
	} else {
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
		sess.ver = msg.Sess.Ver
		sess.userAgent = msg.Sess.UserAgent
		sess.remoteAddr = msg.Sess.RemoteAddr

		// Dispatch remote message to local session.
		sess.dispatch(msg.Msg)
	}

	return nil
}

// Proxy recieves messages from the master node addressed to a specific local session.
// Called by Session.writeRPC
func (Cluster) Proxy(msg *ClusterResp, unused *bool) error {
	log.Printf("cluster: Proxy response received for session %s", msg.FromSID)

	// This cluster member received a response from topic owner to be forwarded to a session
	// Find appropriate session, send the message to it
	if sess := globals.sessionStore.Get(msg.FromSID); sess != nil {
		select {
		case sess.send <- msg.Msg:
		case <-time.After(time.Millisecond * 10):
			log.Println("cluster.Proxy: timeout")
		}
	}

	return nil
}

// Given topic name, find appropriate cluster node to route message to
func (c *Cluster) nodeForTopic(topic string) *ClusterNode {
	key := c.ring.Get(topic)
	if key == c.thisNodeName {
		// Do not route to self
		return nil
	} else {
		return globals.cluster.nodes[key]
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
		log.Fatal("attempt to route to non-existent node")
	}

	// Save node name: it's need in order to inform relevant nodes when the session is disconnected
	if sess.nodes == nil {
		sess.nodes = make(map[string]bool)
	}
	sess.nodes[n.name] = true

	return n.forward(
		&ClusterReq{
			Node:   c.thisNodeName,
			Msg:    msg,
			RcptTo: topic,
			Sess: &ClusterSess{
				Uid:        sess.uid,
				RemoteAddr: sess.remoteAddr,
				UserAgent:  sess.userAgent,
				Ver:        sess.ver,
				Sid:        sess.sid}})
}

// Forward client message to the Master (cluster node which owns the topic)
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

func clusterInit(configString json.RawMessage) {
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

	globals.cluster = &Cluster{
		thisNodeName: config.ThisName,
		ring:         rh.New(CLUSTER_HASH_REPLICAS, nil),
		nodes:        make(map[string]*ClusterNode)}
	ringKeys := make([]string, 0, len(config.Nodes))

	for _, host := range config.Nodes {
		ringKeys = append(ringKeys, host.Name)

		if host.Name == globals.cluster.thisNodeName {
			// Don't create a cluster member for this local instance
			continue
		}

		n := ClusterNode{address: host.Addr, name: host.Name}
		n.done = make(chan bool, 1)
		go n.reconnect()

		globals.cluster.nodes[host.Name] = &n
	}

	if len(globals.cluster.nodes) == 0 {
		log.Fatal("Invalid cluster size: 0")
	}

	globals.cluster.ring.Add(ringKeys...)

	addr, err := net.ResolveTCPAddr("tcp", config.ListenOn)
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
		globals.cluster.thisNodeName, config.ListenOn)
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
			if !ok {
				// channel closed
				return
			}
			// The error is returned if the remote node is down. Which means the remote
			// session is also disconnected.
			if err := sess.rpcnode.endpoint.Call("Cluster.Proxy",
				&ClusterResp{Msg: msg, FromSID: sess.sid}, &unused); err != nil {

				log.Println("sess.writeRPC: " + err.Error())
				return
			}
		case msg := <-sess.stop:
			// Shutdown is requested, don't care if the message is delivered
			if msg != nil {
				sess.rpcnode.endpoint.Call("Cluster.Proxy", &ClusterResp{Msg: msg, FromSID: sess.sid},
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
		// Tell proxy node to shut down the proxied session
		//s.rpcnode.respond()
	}
}

func (c *Cluster) shutdown() {
	if globals.cluster == nil {
		return
	}

	globals.cluster.inbound.Close()
	for _, n := range globals.cluster.nodes {
		n.done <- true
	}
	globals.cluster = nil

	log.Println("cluster shut down")
}
