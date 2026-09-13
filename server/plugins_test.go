package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tinode/chat/pbx"
	"github.com/tinode/chat/server/store/types"
	"google.golang.org/grpc"
)

type pluginTestConnection struct {
	grpc.ClientConnInterface
	invoke func(context.Context, string) error
}

func (c pluginTestConnection) Invoke(ctx context.Context, method string, _, _ any, _ ...grpc.CallOption) error {
	return c.invoke(ctx, method)
}

func TestPluginRPCDeadlines(t *testing.T) {
	previous := globals.plugins
	t.Cleanup(func() { globals.plugins = previous })
	calls := []struct {
		name string
		call func()
	}{
		{"FireHose", func() { pluginFireHose(&Session{}, &ClientComMessage{Hi: &MsgClientHi{}}) }},
		{"Find", func() { pluginFind(types.Uid(1), "query") }},
		{"Account", func() { pluginAccount(&types.User{}, plgActCreate) }},
		{"Topic", func() { pluginTopic(&Topic{name: "grpTest"}, plgActCreate) }},
		{"Subscription", func() { pluginSubscription(&types.Subscription{}, plgActCreate) }},
		{"Message", func() { pluginMessage(&MsgServerData{}, plgActCreate) }},
	}
	for _, config := range []struct {
		name          string
		timeout, want time.Duration
	}{
		{"default", 0, 5 * time.Second},
		{"negative", -time.Second, 5 * time.Second},
		{"configured", 2 * time.Second, 2 * time.Second},
	} {
		for _, rpc := range calls {
			t.Run(config.name+"/"+rpc.name, func(t *testing.T) {
				pluginsInit([]byte(fmt.Sprintf(`[{"enabled":true,"name":"test","service_addr":"tcp://localhost:1","timeout":%d}]`, config.timeout.Microseconds())))
				// RPCs below use the fake client; close the initialization connection.
				globals.plugins[0].conn.Close()
				timeout := globals.plugins[0].timeout
				if timeout != config.want {
					t.Fatalf("initialized timeout: got %v, want %v", timeout, config.want)
				}
				var callCtx context.Context
				client := pbx.NewPluginClient(pluginTestConnection{invoke: func(ctx context.Context, method string) error {
					callCtx = ctx
					if method != "/pbx.Plugin/"+rpc.name {
						t.Errorf("unexpected method %s", method)
					}
					return nil
				}})
				filter := &PluginFilter{byAction: plgActCreate, byPacket: plgHi}
				globals.plugins = []Plugin{{client: client, timeout: timeout,
					filterFireHose: filter, filterFind: true, filterAccount: filter,
					filterTopic: filter, filterSubscription: filter, filterMessage: filter}}
				before := time.Now()
				rpc.call()
				after := time.Now()
				if callCtx == nil {
					t.Fatal("RPC was not called")
				}
				deadline, ok := callCtx.Deadline()
				if !ok || deadline.Before(before.Add(config.want)) || deadline.After(after.Add(config.want)) {
					t.Fatalf("incorrect deadline: %v (present=%v), expected timeout %v", deadline, ok, config.want)
				}
				if !errors.Is(callCtx.Err(), context.Canceled) {
					t.Error("RPC context was not canceled after completion")
				}
			})
		}
	}
}

func TestPluginFindTimeout(t *testing.T) {
	previous := globals.plugins
	t.Cleanup(func() { globals.plugins = previous })
	client := pbx.NewPluginClient(pluginTestConnection{invoke: func(ctx context.Context, _ string) error {
		// Model a plugin that never responds; bound the test if the deadline is lost.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("plugin call did not time out")
		}
	}})
	globals.plugins = []Plugin{{client: client, filterFind: true, timeout: 10 * time.Millisecond}}
	_, _, err := pluginFind(types.Uid(1), "query")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}
