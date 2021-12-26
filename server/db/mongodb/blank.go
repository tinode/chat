//go:build !mongodb
// +build !mongodb

// This file is needed for conditional compilation. It's used when
// the build tag 'mongodb' is not defined. Otherwise the adapter.go
// is compiled.

package mongodb
