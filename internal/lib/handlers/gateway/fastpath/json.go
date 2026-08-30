//go:build linux && amd64 && cgo && !purego
// +build linux,amd64,cgo,!purego

// Package fastpath provides high-performance request processing for the gateway.
// This file uses bytedance/sonic for JIT-compiled JSON parsing on Linux/amd64 with CGO.
package fastpath

import "github.com/bytedance/sonic"

// Marshal serializes v to JSON using sonic (JIT-compiled, ~3000 MB/s).
var Marshal = sonic.Marshal

// Unmarshal deserializes JSON data into v using sonic.
var Unmarshal = sonic.Unmarshal

// ConfigDefault returns a sonic config for standard JSON operations.
var ConfigDefault = sonic.ConfigDefault

// JSONName identifies the JSON library in use.
const JSONName = "sonic"
