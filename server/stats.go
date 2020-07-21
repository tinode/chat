// Logic related to expvar handling: reporting live stats such as
// session and topic counts, memory usage etc.
// The stats updates happen in a separate go routine to avoid
// locking on main logic routines.

package main

import (
	"expvar"
	"log"
	"net/http"
	"runtime"
	"time"
)

type varUpdate struct {
	// Name of the variable to update
	varname string
	// Integer value to publish
	count int64
	// Treat the count as an increment as opposite to the final value.
	inc bool
}

// Initialize stats reporting through expvar.
func statsInit(mux *http.ServeMux, path string) {
	if path == "" || path == "-" {
		return
	}

	mux.Handle(path, expvar.Handler())
	globals.statsUpdate = make(chan *varUpdate, 1024)

	start := time.Now()
	expvar.Publish("Uptime", expvar.Func(func() interface{} {
		return time.Since(start).Seconds()
	}))
	expvar.Publish("NumGoroutines", expvar.Func(func() interface{} {
		return runtime.NumGoroutine()
	}))

	go statsUpdater()

	log.Printf("stats: variables exposed at '%s'", path)
}

// Register integer variable. Don't check for initialization.
func statsRegisterInt(name string) {
	expvar.Publish(name, new(expvar.Int))
}

// Async publish int variable.
func statsSet(name string, val int64) {
	if globals.statsUpdate != nil {
		select {
		case globals.statsUpdate <- &varUpdate{name, val, false}:
		default:
		}
	}
}

// Async publish an increment (decrement) to int variable.
func statsInc(name string, val int) {
	if globals.statsUpdate != nil {
		select {
		case globals.statsUpdate <- &varUpdate{name, int64(val), true}:
		default:
		}
	}
}

// Stop publishing stats.
func statsShutdown() {
	if globals.statsUpdate != nil {
		globals.statsUpdate <- nil
	}
}

// The go routine which actually publishes stats updates.
func statsUpdater() {

	for upd := range globals.statsUpdate {
		if upd == nil {
			globals.statsUpdate = nil
			// Dont' care to close the channel.
			break
		}

		// Handle var update
		if ev := expvar.Get(upd.varname); ev != nil {
			// Intentional panic if the ev is not *expvar.Int.
			intvar := ev.(*expvar.Int)
			if upd.inc {
				intvar.Add(upd.count)
			} else {
				intvar.Set(upd.count)
			}
		} else {
			panic("stats: update to unknown variable " + upd.varname)
		}
	}

	log.Println("stats: shutdown")
}
