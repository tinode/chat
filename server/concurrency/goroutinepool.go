// Package concurrency is a very simple implementation of a mutex with channels.
// Provides TryLock functionality absent in Go's regular sync.Mutex.
// See https://github.com/golang/go/issues/6123 for details.
package concurrency

// Task represents a work task to be run on the specified thread pool.
type Task func()

// GoRoutinePool is a pull of Go routines with associated locking mechanism.
type GoRoutinePool struct {
	// Work queue.
	work chan Task
	// Counter to control the number of already allocated/running goroutines.
	sem chan struct{}
	// Exit knob.
	stop chan struct{}
}

// NewGoRoutinePool allocates a new thread pool with `numWorkers` goroutines.
func NewGoRoutinePool(numWorkers int) *GoRoutinePool {
	return &GoRoutinePool{
		work: make(chan Task),
		sem:  make(chan struct{}, numWorkers),
		stop: make(chan struct{}, numWorkers),
	}
}

// Schedule enqueus a closure to run on the GoRoutinePool's goroutines.
func (p *GoRoutinePool) Schedule(task Task) {
	select {
	case p.work <- task:
	case p.sem <- struct{}{}:
		go p.worker(task)
	}
}

// Stop sends a stop signal to all running goroutines.
func (p *GoRoutinePool) Stop() {
	numWorkers := cap(p.sem)
	for i := 0; i < numWorkers; i++ {
		p.stop <- struct{}{}
	}
}

// Thread pool worker goroutine.
func (p *GoRoutinePool) worker(task Task) {
	defer func() { <-p.sem }()
	for {
		task()
		select {
		case task = <-p.work:
		case <-p.stop:
			return
		}
	}
}
