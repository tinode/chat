// Go implementation of tn-cli.
package main

import (
	"context"
	"flag"
	pb "github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/logs"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log"
	"os"
	"strings"
	"time"
)

var (
	logFlags   = flag.String("log_flags", "stdFlags", "comma-separated list of log flags")
	host       = flag.String("host", "localhost:16060", "address of Tinode gRPC server")
	loginBasic = flag.String("login-basic", "", "login using basic authentication username:password")
	verbose    = flag.Bool("verbose", false, "log full JSON representation of all messages")
)

func main() {
	flag.Parse()
	logs.Init(os.Stderr, *logFlags)

	if *loginBasic == "" {
		log.Fatal("--login-basic must be provided in the format username:password")
	}
	if !strings.Contains(*loginBasic, ":") {
		log.Fatal("invalid format for --login-basic, expected username:password.")
	}

	conn, err := grpc.NewClient(*host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("failed to connect to server: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := pb.NewNodeClient(conn)
	stream, err := client.MessageLoop(ctx)
	if err != nil {
		log.Fatalf("failed to initiate message loop: %v", err)
	}

	logs.Info.Printf("logging in with login:password:%v\n", *loginBasic)

	loginMsg := &pb.ClientLogin{
		Scheme: "basic",
		Secret: []byte(*loginBasic),
	}
	if *verbose {
		log.Printf("sending login message:%v\n", loginMsg)
	}

	err = stream.Send(&pb.ClientMsg{
		Message: &pb.ClientMsg_Hi{
			Hi: &pb.ClientHi{
				Ver: "1.69.2",
			},
		},
	})
	if err != nil {
		log.Fatalf("failed to send hi message: %v", err)
	}

	response, err := stream.Recv()
	if err != nil {
		log.Fatalf("failed to receive response: %v", err)
	}

	err = stream.Send(&pb.ClientMsg{
		Message: &pb.ClientMsg_Login{
			Login: loginMsg,
		},
	})
	if err != nil {
		log.Fatalf("failed to send login message: %v", err)
	}

	response, err = stream.Recv()
	if err != nil {
		log.Fatalf("failed to receive response: %v", err)
	}

	switch msg := response.Message.(type) {
	case *pb.ServerMsg_Ctrl:
		if msg.Ctrl.Code == 200 {
			log.Printf("logged in as %s\n", msg.Ctrl.Params["user"])
			log.Printf("token: %s\n", msg.Ctrl.Params["token"])
		} else {
			log.Printf("login failed: %s\n", msg.Ctrl.Text)
		}
	default:
		log.Println("unexpected response from server.")
	}
}
