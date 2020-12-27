// Package concurrency is a very simple implementation of a mutex with channels.
// Provides TryLock functionality absent in Go's regular sync.Mutex.
// See https://github.com/golang/go/issues/6123 for details.
package concurrency

// SimpleMutex is a channel used for locking.
type SimpleMutex chan struct{}

// NewSimpleMutex creates and returns a new SimpleMutex object.
func NewSimpleMutex() SimpleMutex {
	return make(SimpleMutex, 1)
}

// Lock acquires a lock on the mutex.
func (s SimpleMutex) Lock() {
	s <- struct{}{}
}

// TryLock attempts to acquire a lock on the mutex.
// Returns true if the lock has been acquired, false otherwise.
func (s SimpleMutex) TryLock() bool {
	select {
	case s <- struct{}{}:
		return true
	default:
		return false
	}
}

// Unlock releases the mutex.
func (s SimpleMutex) Unlock() {
	<-s
}
