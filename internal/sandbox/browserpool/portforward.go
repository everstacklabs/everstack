package browserpool

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

type portForwardEntry struct {
	localPort int
	stopCh    chan struct{}
	stopOnce  sync.Once
}

func (entry *portForwardEntry) stop() {
	entry.stopOnce.Do(func() { close(entry.stopCh) })
}

type portForwardCall struct {
	done      chan struct{}
	localPort int
	err       error
}

type portForwardManager struct {
	runtime *kubernetesRuntime

	mu      sync.Mutex
	entries map[string]*portForwardEntry
	pending map[string]*portForwardCall
}

func (manager *portForwardManager) start(ctx context.Context, podName string, remotePort int) (int, error) {
	key := fmt.Sprintf("%s:%d", podName, remotePort)
	manager.mu.Lock()
	if entry := manager.entries[key]; entry != nil {
		manager.mu.Unlock()
		return entry.localPort, nil
	}
	if call := manager.pending[key]; call != nil {
		manager.mu.Unlock()
		select {
		case <-call.done:
			if call.err != nil {
				return 0, fmt.Errorf("browserpool: wait for port-forward setup: %w", call.err)
			}
			return call.localPort, nil
		case <-ctx.Done():
			return 0, fmt.Errorf("browserpool: wait for port-forward setup: %w", ctx.Err())
		}
	}
	call := &portForwardCall{done: make(chan struct{})}
	manager.pending[key] = call
	manager.mu.Unlock()

	localPort, entry, errCh, err := manager.startUncached(ctx, podName, remotePort)
	manager.mu.Lock()
	delete(manager.pending, key)
	call.localPort = localPort
	call.err = err
	if err == nil {
		manager.entries[key] = entry
	}
	close(call.done)
	manager.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("browserpool: start port-forward: %w", err)
	}

	logger.WithFields("pod", podName, "port", remotePort, "local_port", localPort).
		Info("browserpool: port-forward started")
	go manager.monitor(key, podName, remotePort, entry, errCh)
	return localPort, nil
}

func (manager *portForwardManager) startUncached(ctx context.Context, podName string, remotePort int) (int, *portForwardEntry, <-chan error, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, nil, fmt.Errorf("browserpool: allocate local port: %w", err)
	}
	localPort := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, nil, nil, fmt.Errorf("browserpool: release local port reservation: %w", err)
	}

	transport, upgrader, err := spdy.RoundTripperFor(manager.runtime.restConfig)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("browserpool: create SPDY transport: %w", err)
	}
	restClient := manager.runtime.client.CoreV1().RESTClient()
	if restClient == nil {
		return 0, nil, nil, fmt.Errorf("browserpool: Kubernetes REST client is unavailable for port-forward")
	}
	forwardURL := restClient.Post().
		Resource("pods").
		Namespace(manager.runtime.cfg.Namespace).
		Name(podName).
		SubResource("portforward").
		URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, forwardURL)

	entry := &portForwardEntry{stopCh: make(chan struct{}), localPort: localPort}
	readyCh := make(chan struct{})
	forwarder, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("%d:%d", localPort, remotePort)},
		entry.stopCh,
		readyCh,
		nil,
		nil,
	)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("browserpool: create port-forward: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- forwarder.ForwardPorts() }()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-readyCh:
		return localPort, entry, errCh, nil
	case forwardErr := <-errCh:
		entry.stop()
		if forwardErr == nil {
			forwardErr = fmt.Errorf("browserpool: port-forward stopped before becoming ready")
		}
		return 0, nil, nil, fmt.Errorf("browserpool: port-forward failed: %w", forwardErr)
	case <-timer.C:
		entry.stop()
		return 0, nil, nil, fmt.Errorf("browserpool: port-forward timed out after 10s")
	case <-ctx.Done():
		entry.stop()
		return 0, nil, nil, fmt.Errorf("browserpool: start port-forward: %w", ctx.Err())
	}
}

func (manager *portForwardManager) monitor(key, podName string, remotePort int, entry *portForwardEntry, errCh <-chan error) {
	err := <-errCh
	manager.mu.Lock()
	if manager.entries[key] == entry {
		delete(manager.entries, key)
	}
	manager.mu.Unlock()
	if err != nil {
		logger.WithFields("pod", podName, "port", remotePort, "error", err.Error()).
			Warn("browserpool: port-forward terminated")
	}
}

func (manager *portForwardManager) stopPod(podName string) {
	manager.mu.Lock()
	entries := make([]*portForwardEntry, 0, 2)
	for key, entry := range manager.entries {
		if len(key) > len(podName) && key[:len(podName)] == podName && key[len(podName)] == ':' {
			delete(manager.entries, key)
			entries = append(entries, entry)
		}
	}
	manager.mu.Unlock()
	for _, entry := range entries {
		entry.stop()
	}
}

func (manager *portForwardManager) stopAll() {
	manager.mu.Lock()
	entries := make([]*portForwardEntry, 0, len(manager.entries))
	for key, entry := range manager.entries {
		delete(manager.entries, key)
		entries = append(entries, entry)
	}
	manager.mu.Unlock()
	for _, entry := range entries {
		entry.stop()
	}
}
