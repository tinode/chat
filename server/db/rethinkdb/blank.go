//go:build !rethinkdb
// +build !rethinkdb

// This file is needed for conditional compilation. It's used when
// the build tag 'rethinkdb' is not defined. Otherwise the adapter.go
// is compiled.

package rethinkdb
