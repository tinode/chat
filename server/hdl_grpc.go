/******************************************************************************
 *
 *  Description :
 *
 *    Handler of gRPC connections. See also hdl_websock.go for websockets and
 *    hdl_longpoll.go for long polling.
 *
 *****************************************************************************/

package main

import (
	"io"
	"log"
	"net"

	"github.com/tinode/chat/pbx"
	"google.golang.org/grpc"
)

type grpcNodeServer struct {
}

func (sess *Session) closeGrpc() {
	if sess.proto == GRPC {
		sess.lock.Lock()
		sess.grpcnode = nil
		sess.lock.Unlock()
	}
}

// Equivalent of starting a new session and a read loop in one
func (*grpcNodeServer) MessageLoop(stream pbx.Node_MessageLoopServer) error {
	sess, _ := globals.sessionStore.Create(stream, "")

	defer func() {
		sess.closeGrpc()
		sess.cleanUp()
		log.Println("grpc exited", sess.sid)
	}()

	go sess.writeGrpcLoop()

	for {
		in, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		log.Println("grpc in:", truncateStringIfTooLong(in.String()))
		sess.dispatch(pbCliDeserialize(in))

		sess.lock.Lock()
		if sess.grpcnode == nil {
			sess.lock.Unlock()
			break
		}
		sess.lock.Unlock()
	}

	return nil
}

func (sess *Session) writeGrpcLoop() {

	defer func() {
		sess.closeGrpc() // exit MessageLoop
	}()

	for {
		select {
		case msg, ok := <-sess.send:
			if !ok {
				// channel closed
				return
			}
			if err := grpcWrite(sess, msg); err != nil {
				log.Println("g.writeGrpcLoop", err)
				return
			}
		case msg := <-sess.stop:
			// Shutdown requested, don't care if the message is delivered
			if msg != nil {
				grpcWrite(sess, msg)
			}
			return

		case topic := <-sess.detach:
			sess.delSub(topic)
		}
	}
}

func grpcWrite(sess *Session, msg interface{}) error {
	out := sess.grpcnode
	if out != nil {
		// Will panic if msg is not of *pbx.ServerMsg type. This is an intentional panic.
		return out.Send(msg.(*pbx.ServerMsg))
	}
	return nil
}

func serveGrpc(addr string) (*grpc.Server, error) {
	if addr == "" {
		return nil, nil
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	srv := grpc.NewServer()
	pbx.RegisterNodeServer(srv, &grpcNodeServer{})
	log.Printf("gRPC server is registered at [%s]", addr)

	go func() {
		if err := srv.Serve(lis); err != nil {
			log.Println("gRPC server failed:", err)
		}
	}()

	return srv, nil
}
