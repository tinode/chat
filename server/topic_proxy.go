/******************************************************************************
 *  Description :
 *    Topic in a cluster which serves as a local representation of the master
 *    topic hosted at another node.
 *****************************************************************************/

package main

import (
	"log"
	"net/http"
	"time"

	"github.com/tinode/chat/server/store/types"
)

// Direct communication from proxy topic to master and service requests.
type topicMasterRequest struct {
	// Deferred notifications request (list of sessions to notify).
	deferredNotificationsRequest []*deferredNotification
	// List of proxied user IDs and the counts of proxied origin sessions for each uid.
	// Required for accounting.
	proxySessionCleanUp map[types.Uid]int
}

func (t *Topic) runProxy(hub *Hub) {
	killTimer := time.NewTimer(time.Hour)
	killTimer.Stop()

	// Ticker for deferred presence notifications.
	defrNotifTimer := time.NewTicker(time.Millisecond * 500)

	for {
		select {
		case join := <-t.reg:
			// Request to add a connection to this topic
			if t.isInactive() {
				asUid := types.ParseUserId(join.pkt.AsUser)
				join.sess.queueOut(ErrLocked(join.pkt.Id, t.original(asUid), types.TimeNow()))
			} else {
				// Response (ctrl message) will be handled when it's received via the proxy channel.
				if err := globals.cluster.routeToTopicMaster(ProxyReqJoin, join.pkt, t.name, join.sess); err != nil {
					log.Println("proxy topic: route join request from proxy to master failed:", err)
				}
			}

		case leave := <-t.unreg:
			// Detach session from topic; session may continue to function.
			log.Printf("t[%s] leave %+v", t.name, leave)
			var asUid types.Uid
			if leave.pkt != nil {
				asUid = types.ParseUserId(leave.pkt.AsUser)
			}

			// Explicitly specify user id because the proxy session hosts multiple client sessions.
			if asUid.IsZero() {
				if pssd, ok := t.sessions[leave.sess]; ok {
					asUid = pssd.uid
				} else {
					log.Println("proxy topic: leave request sent for unknown session")
					continue
				}
			}
			// Remove the session from the topic without waiting for a response from the master node
			// because by the time the response arrives this session may be already gone from the session store
			// and we won't be able to find and remove it by its sid.
			t.remSession(leave.sess, asUid)
			if err := globals.cluster.routeToTopicMaster(ProxyReqLeave, leave.pkt, t.name, leave.sess); err != nil {
				log.Println("proxy topic: route broadcast request from proxy to master failed:", err)
			}

		case msg := <-t.broadcast:
			// Content message intended for broadcasting to recipients
			log.Printf("t[%s] broadcast %+v", t.name, msg)
			if err := globals.cluster.routeToTopicMaster(ProxyReqBroadcast, msg, t.name, msg.sess); err != nil {
				log.Println("proxy topic: route broadcast request from proxy to master failed:", err)
			}

		case meta := <-t.meta:
			// Request to get/set topic metadata
			log.Printf("t[%s] meta %+v", t.name, meta)
			if err := globals.cluster.routeToTopicMaster(ProxyReqMeta, meta.pkt, t.name, meta.sess); err != nil {
				log.Println("proxy topic: route meta request from proxy to master failed:", err)
			}

		case ua := <-t.uaChange:
			if t.cat == types.TopicCatMe {
				// Process an update to user agent from one of the sessions.
				if err := globals.cluster.routeToTopicMaster(ProxyReqUAChange, nil, t.name,
					&Session{userAgent: ua}); err != nil {
					log.Println("proxy topic: route ua change request from proxy to master failed:", err)
				}
			}

		case msg := <-t.proxy:
			t.proxyMasterResponse(msg, killTimer)

		case sd := <-t.exit:
			log.Printf("t[%s] exit %+v", t.name, sd)
			// Tell sessions to remove the topic
			for s := range t.sessions {
				s.detach <- t.name
			}
			if t.isProxy {
				if err := globals.cluster.topicProxyGone(t.name); err != nil {
					log.Printf("topic proxy shutdown [%s]: failed to notify master - %s", t.name, err)
				}
			}
			// Report completion back to sender, if 'done' is not nil.
			if sd.done != nil {
				sd.done <- true
			}
			return

		case <-killTimer.C:
			// Topic timeout
			hub.unreg <- &topicUnreg{rcptTo: t.name}

		case <-defrNotifTimer.C:
			t.onDeferredNotificationTimer()
		}
	}
}

func (t *Topic) proxyMasterResponse(msg *ClusterResp, killTimer *time.Timer) {
	// Kills topic after a period of inactivity.
	keepAlive := idleProxyTopicTimeout

	if msg.SrvMsg.Pres != nil && msg.SrvMsg.Pres.What == "acs" && msg.SrvMsg.Pres.Acs != nil {
		// If the server changed acs on this topic, update the internal state.
		t.updateAcsFromPresMsg(msg.SrvMsg.Pres)
	}
	if msg.OrigSid == "*" {
		// It is a broadcast.
		msg.SrvMsg.uid = types.ParseUserId(msg.SrvMsg.AsUser)
		switch {
		case msg.SrvMsg.Pres != nil || msg.SrvMsg.Data != nil || msg.SrvMsg.Info != nil:
			// Regular broadcast.
			t.proxyFanoutBroadcast(msg.SrvMsg)
		case msg.SrvMsg.Ctrl != nil:
			// Ctrl broadcast. E.g. for user eviction.
			t.proxyCtrlBroadcast(msg.SrvMsg)
		default:
		}
	} else {
		sess := globals.sessionStore.Get(msg.OrigSid)
		switch msg.OrigReqType {
		case ProxyReqJoin:
			if sess != nil && msg.SrvMsg != nil && msg.SrvMsg.Ctrl != nil {
				if msg.SrvMsg.Ctrl.Code < 300 {
					if t.addSession(sess, msg.SrvMsg.uid) {
						sess.addSub(t.name, &Subscription{
							broadcast: t.broadcast,
							done:      t.unreg,
							meta:      t.meta,
							uaChange:  t.uaChange})
						if msg.IsBackground {
							// It's a background session. Add deferred notification.
							pssd, _ := t.sessions[sess]
							pssd.ref = t.defrNotif.PushFront(&deferredNotification{
								sess:      sess,
								uid:       msg.SrvMsg.uid,
								timestamp: msg.SrvMsg.Timestamp,
							})
							t.sessions[sess] = pssd
						}
					}
					killTimer.Stop()
				} else {
					if len(t.sessions) == 0 {
						killTimer.Reset(keepAlive)
					}
				}
			}
		case ProxyReqBroadcast:
			// do nothing
		case ProxyReqMeta:
			// do nothing
		case ProxyReqLeave:
			log.Printf("proxy topic [%s]: session %p unsubscribed", t.name, sess)
			if msg.SrvMsg != nil && msg.SrvMsg.Ctrl != nil {
				log.Printf("proxy topic [%s]: ctrl msg = %+v", t.name, msg.SrvMsg.Ctrl)
				if msg.SrvMsg.Ctrl.Code < 300 {
					if sess != nil {
						t.remSession(sess, sess.uid)
					}
					// All sessions are gone. Start the kill timer.
					if len(t.sessions) == 0 {
						killTimer.Reset(keepAlive)
					}
				}
			}
		default:
			log.Printf("proxy topic [%s] received response referencing unknown request type %d",
				t.name, msg.OrigReqType)
		}
		if !sess.queueOut(msg.SrvMsg) {
			log.Println("topic proxy: timeout")
		}
	}
}

// proxyFanoutBroadcast broadcasts msg to all sessions attached to this topic.
func (t *Topic) proxyFanoutBroadcast(msg *ServerComMessage) {
	for sess, pssd := range t.sessions {
		if sess.sid == msg.SkipSid {
			continue
		}
		if msg.Pres != nil {
			if !t.passesPresenceFilters(msg, pssd.uid) {
				continue
			}
		} else if msg.Data != nil {
			if !t.userIsReader(pssd.uid) {
				continue
			}
		}
		t.maybeFixTopicName(msg, pssd.uid)
		log.Printf("broadcast fanout [%s] to %s", t.name, sess.sid)
		if !sess.queueOut(msg) {
			log.Printf("topic[%s]: connection stuck, detaching", t.name)
			t.unreg <- &sessionLeave{sess: sess}
		}
	}
}

// proxyCtrlBroadcast broadcasts a ctrl command to certain sessions attached to this topic.
func (t *Topic) proxyCtrlBroadcast(msg *ServerComMessage) {
	if msg.Ctrl.Code == http.StatusResetContent && msg.Ctrl.Text == "evicted" {
		// We received a ctrl command for evicting a user.
		if msg.uid.IsZero() {
			log.Panicf("topic[%s]: proxy received evict message with empty uid", t.name)
		}
		for sess := range t.sessions {
			if t.remSession(sess, msg.uid) != nil {
				sess.detach <- t.name
				if sess.sid != msg.SkipSid {
					sess.queueOut(msg)
				}
			}
		}
	}
}

// updateAcsFromPresMsg modifies user acs in Topic's perUser struct based on the data in `pres`.
func (t *Topic) updateAcsFromPresMsg(pres *MsgServerPres) {
	uid := types.ParseUserId(pres.Src)
	dacs := pres.Acs
	if uid.IsZero() {
		log.Printf("proxy topic[%s]: received acs change for invalid user id '%s'", t.name, pres.Src)
		return
	}

	// If t.perUser[uid] does not exist, pud is initialized with blanks, otherwise it gets existing values.
	pud := t.perUser[uid]
	if err := pud.modeWant.ApplyMutation(dacs.Want); err != nil {
		log.Printf("proxy topic[%s]: could not process acs change - want: %+v", t.name, err)
		return
	}
	if err := pud.modeGiven.ApplyMutation(dacs.Given); err != nil {
		log.Printf("proxy topic[%s]: could not process acs change - given: %+v", t.name, err)
		return
	}
	// Update existing or add new.
	t.perUser[uid] = pud
}

// routeDeferredNotificationsToMaster forwards deferred notification requests to the master topic.
func (t *Topic) routeDeferredNotificationsToMaster(notifs []*deferredNotification) {
	var sendReqs []*ProxyDeferredSession
	for _, dn := range notifs {
		s := &ProxyDeferredSession{
			AsUser:    dn.uid.UserId(),
			Sid:       dn.sid,
			UserAgent: dn.userAgent,
		}
		sendReqs = append(sendReqs, s)
	}

	if err := globals.cluster.routeToTopicMaster(ProxyReqBkgNotif, sendReqs, t.name, nil); err != nil {
		log.Println("proxy topic: deferred notifications request from proxy to master failed:", err)
	}
}
