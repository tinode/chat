//go:build !postgres
// +build !postgres

// This file is needed for conditional compilation. It's used when
// the build tag 'postgres' is not defined. Otherwise the adapter.go
// is compiled.

package postgres
