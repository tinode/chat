/******************************************************************************
 *
 *  Description :
 *    A very basic and naive implementation of thread pool.
 *
 *****************************************************************************/
package main

// Task represents a work task to be run on the specified thread pool.
type Task struct {
	// Closure to execute.
	work func()
}

type ThreadPool struct {
	// Work queue.
	work chan *Task
	// Counter to control the number of already allocated/running goroutines.
	sem chan struct{}
	// Exit knob.
	stop chan struct{}
}

// NewThreadPool allocates a new thread pool with `numWorkers` goroutines.
func NewThreadPool(numWorkers int) *ThreadPool {
	return &ThreadPool{
		work: make(chan *Task),
		sem:  make(chan struct{}, numWorkers),
		stop: make(chan struct{}, numWorkers),
	}
}

// Schedule enqueus a closure to run on the ThreadPool's goroutines.
func (p *ThreadPool) Schedule(task *Task) {
	select {
	case p.work <- task:
	case p.sem <- struct{}{}:
		go p.worker(task)
	}
}

// Stop sends a stop signal to all running goroutines.
func (p *ThreadPool) Stop() {
	numWorkers := cap(p.sem)
	for i := 0; i < numWorkers; i++ {
		p.stop <- struct{}{}
	}
}

// Thread pool worker goroutine.
func (p *ThreadPool) worker(task *Task) {
	defer func() { <-p.sem }()
	for {
		task.work()
		select {
		case task = <-p.work:
		case <-p.stop:
			return
		}
	}
}
