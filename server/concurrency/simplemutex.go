/******************************************************************************
 *
 *  Description :
 *    A very simple implementation of a mutex with channels.
 *    Provides TryLock functionality absent in Go's regular sync.Mutex.
 *    See https://github.com/golang/go/issues/6123 for details.
 *
 *****************************************************************************/
package concurrency

type SimpleMutex chan struct{}

// Creates and returns a new SimpleMutex object.
func NewSimpleMutex() SimpleMutex {
	return make(SimpleMutex, 1)
}

// Acquires a lock on the mutex.
func (s SimpleMutex) Lock() {
	s <- struct{}{}
}

// Attempts to acquire a lock on the mutex.
// Returns true if the lock has been acquired, false otherwise.
func (s SimpleMutex) TryLock() bool {
	select {
	case s <- struct{}{}:
		return true
	default:
		return false
	}
}

// Releases the mutex.
func (s SimpleMutex) Unlock() {
	<-s
}
