//go:build !linux || !amd64 || !cgo || purego
// +build !linux !amd64 !cgo purego

// Package fastpath provides high-performance request processing for the gateway.
// This file uses goccy/go-json as a fallback for non-Linux/amd64 platforms or when CGO is disabled.
package fastpath

import "github.com/goccy/go-json"

// Marshal serializes v to JSON using go-json (~1500 MB/s).
var Marshal = json.Marshal

// Unmarshal deserializes JSON data into v using go-json.
var Unmarshal = json.Unmarshal

// JSONName identifies the JSON library in use.
const JSONName = "go-json"
