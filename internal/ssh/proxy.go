package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
)

// ProxyConfig holds configuration for the SSH proxy server.
type ProxyConfig struct {
	ListenAddr            string
	HostKeySigner         ssh.Signer
	KeyStore              *KeyStore
	SandboxManager        *sandbox.SandboxManager
	MaxSessionsPerSandbox int
	ConnRatePerIP         int // connections per minute per IP
}

// Proxy is a Go SSH server that routes connections to sandbox shells.
type Proxy struct {
	config   ProxyConfig
	listener net.Listener
	done     chan struct{}
	cancel   context.CancelFunc

	// Per-IP rate limiters
	rateMu     sync.Mutex
	rateLimits map[string]*rateLimitEntry

	// Per-sandbox session counts
	sessionMu     sync.Mutex
	sessionCounts map[string]int

	// Per-IP auth-failure tracker. When an IP racks up too many
	// failed pubkey handshakes in a short window we ban it for a
	// while — first-line defense against fingerprint enumeration /
	// credential probing now that the listener is public on :2222.
	banMu sync.Mutex
	bans  map[string]*banEntry
}

type rateLimitEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// banEntry tracks the rolling auth-failure history for one IP plus
// any active ban. Sliding window: each failure timestamps into a slice;
// the slice is trimmed to entries newer than `failureWindow`. When the
// trimmed length crosses `maxFailuresPerWindow`, `bannedUntil` extends
// from now() by `banDuration`.
type banEntry struct {
	failures    []time.Time
	bannedUntil time.Time
}

// Ban policy. Tunable here (not config) because the right values fall
// out of "what does abuse traffic look like" rather than "what does
// the customer want" — same reason we hard-code the rate limit.
const (
	failureWindow        = 5 * time.Minute
	maxFailuresPerWindow = 5 // 5 bad keys in 5 min and the IP is hot
	banDuration          = 30 * time.Minute
)

// NewProxy creates a new SSH proxy.
func NewProxy(config ProxyConfig) *Proxy {
	if config.MaxSessionsPerSandbox == 0 {
		config.MaxSessionsPerSandbox = 5
	}
	if config.ConnRatePerIP == 0 {
		config.ConnRatePerIP = 10
	}

	return &Proxy{
		config:        config,
		done:          make(chan struct{}),
		rateLimits:    make(map[string]*rateLimitEntry),
		sessionCounts: make(map[string]int),
		bans:          make(map[string]*banEntry),
	}
}

// isBanned returns true if `ip` is currently in the banned window.
// Lock-free hot path: take the mutex only inside.
func (p *Proxy) isBanned(ip string) bool {
	if ip == "" {
		return false
	}
	p.banMu.Lock()
	defer p.banMu.Unlock()
	entry, ok := p.bans[ip]
	if !ok {
		return false
	}
	return time.Now().Before(entry.bannedUntil)
}

// recordAuthFailure increments the rolling failure count for an IP and
// arms a ban once the threshold trips. Called from publicKeyCallback
// every time a presented key is rejected.
func (p *Proxy) recordAuthFailure(ip string) {
	if ip == "" {
		return
	}
	now := time.Now()
	cutoff := now.Add(-failureWindow)

	p.banMu.Lock()
	defer p.banMu.Unlock()

	entry, ok := p.bans[ip]
	if !ok {
		entry = &banEntry{}
		p.bans[ip] = entry
	}
	// Trim out failures older than the sliding window.
	keep := entry.failures[:0]
	for _, t := range entry.failures {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	entry.failures = append(keep, now)

	if len(entry.failures) >= maxFailuresPerWindow && now.After(entry.bannedUntil) {
		entry.bannedUntil = now.Add(banDuration)
		logger.WithFields(
			"ip", ip,
			"failures", len(entry.failures),
			"window_minutes", int(failureWindow.Minutes()),
			"ban_minutes", int(banDuration.Minutes()),
		).Warn("ssh proxy: banning IP for repeated auth failures")
	}
}

// recordAuthSuccess clears any soft state for an IP after a successful
// handshake — they proved they have a valid key, no point holding past
// failures against them.
func (p *Proxy) recordAuthSuccess(ip string) {
	if ip == "" {
		return
	}
	p.banMu.Lock()
	defer p.banMu.Unlock()
	delete(p.bans, ip)
}

// cleanupBans drops ban entries whose ban has fully expired AND whose
// failure history is empty. Bounded by the periodic cleanup loop.
func (p *Proxy) cleanupBans() {
	now := time.Now()
	cutoff := now.Add(-failureWindow)
	p.banMu.Lock()
	defer p.banMu.Unlock()
	for ip, entry := range p.bans {
		// Trim stale failures.
		keep := entry.failures[:0]
		for _, t := range entry.failures {
			if t.After(cutoff) {
				keep = append(keep, t)
			}
		}
		entry.failures = keep
		if len(entry.failures) == 0 && now.After(entry.bannedUntil) {
			delete(p.bans, ip)
		}
	}
}

// Start begins listening for SSH connections.
func (p *Proxy) Start() error {
	var err error
	p.listener, err = net.Listen("tcp", p.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("ssh proxy: failed to listen on %s: %w", p.config.ListenAddr, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	// Periodic rate limiter cleanup
	go p.cleanupRateLimiters(ctx)
	go p.cleanupBansLoop(ctx)

	logger.WithFields("addr", p.config.ListenAddr).Info("ssh proxy: listening")

	go func() {
		defer close(p.done)
		logger.Info("ssh proxy: accept loop running (v2)")
		for {
			conn, acceptErr := p.listener.Accept()
			if acceptErr != nil {
				select {
				case <-ctx.Done():
					return
				default:
					logger.WithFields("error", acceptErr.Error()).Warn("ssh proxy: accept error")
					continue
				}
			}
			logger.WithFields("remote", conn.RemoteAddr().String()).Info("ssh proxy: accepted TCP connection")
			go p.handleConnection(ctx, conn)
		}
	}()

	return nil
}

// Stop shuts down the SSH proxy.
func (p *Proxy) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	if p.listener != nil {
		_ = p.listener.Close()
	}
	<-p.done
	logger.Info("ssh proxy: stopped")
}

// Fingerprint returns the SSH host key fingerprint.
func (p *Proxy) Fingerprint() string {
	return ssh.FingerprintSHA256(p.config.HostKeySigner.PublicKey())
}

// ─── Connection Handling ────────────────────────────────────────────────

func (p *Proxy) handleConnection(ctx context.Context, netConn net.Conn) {
	remoteAddr := netConn.RemoteAddr().String()
	host, _, _ := net.SplitHostPort(remoteAddr)

	logger.WithFields("remote", remoteAddr).Info("ssh proxy: new connection")

	// Banned IPs get cut at the TCP layer — don't even start TLS/SSH
	// negotiation. Saves cycles and denies the attacker any signal
	// (no auth-failed banner, no key-exchange detail) about which keys
	// might be valid in this tenant.
	if p.isBanned(host) {
		logger.WithFields("ip", host).Warn("ssh proxy: banned IP rejected at accept")
		_ = netConn.Close()
		return
	}

	// Rate limit per IP
	if !p.allowConnection(host) {
		logger.WithFields("ip", host).Warn("ssh proxy: rate limited")
		_ = netConn.Close()
		return
	}

	sshConfig := &ssh.ServerConfig{
		PublicKeyCallback:           p.publicKeyCallback,
		KeyboardInteractiveCallback: p.keyboardInteractiveCallback,
	}
	sshConfig.AddHostKey(p.config.HostKeySigner)

	logger.WithFields("remote", remoteAddr).Debug("ssh proxy: starting handshake")

	serverConn, chans, reqs, err := ssh.NewServerConn(netConn, sshConfig)
	if err != nil {
		// Handshake failure is a strong signal of probing — count it
		// against the source IP so a sustained scanner trips the ban.
		p.recordAuthFailure(host)
		logger.WithFields("remote", remoteAddr, "error", err.Error()).Warn("ssh proxy: handshake failed")
		_ = netConn.Close()
		return
	}
	// Successful handshake — clear any prior ban state for this IP
	// (legitimate users sometimes typo a key name then fix it).
	p.recordAuthSuccess(host)
	defer serverConn.Close()

	// Discard global requests
	go ssh.DiscardRequests(reqs)

	// Use the resolved sandbox ID from auth (handles name-based login)
	sandboxID := serverConn.Permissions.Extensions["sandbox_id"]
	logger.WithFields("sandbox_id", sandboxID, "user", serverConn.User(), "remote", remoteAddr).Info("ssh proxy: authenticated")

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}

		// Check session limit
		if !p.acquireSession(sandboxID) {
			_ = newChannel.Reject(ssh.ResourceShortage, "too many sessions for this sandbox")
			continue
		}

		channel, requests, acceptErr := newChannel.Accept()
		if acceptErr != nil {
			p.releaseSession(sandboxID)
			continue
		}

		go func() {
			defer p.releaseSession(sandboxID)
			p.handleSession(ctx, sandboxID, channel, requests)
		}()
	}
}

// publicKeyCallback authenticates the SSH connection.
// Username can be a sandbox id, name, or short_code. Key must belong to
// a user with access.
func (p *Proxy) publicKeyCallback(connMeta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	idOrName := connMeta.User()
	fingerprint := ssh.FingerprintSHA256(key)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if IsSandboxSSHTokenValue(idOrName) {
		p.logSSHAudit("ssh_token_username_rejected",
			"user", tokenLogPrefix(idOrName),
			"remote", connMeta.RemoteAddr().String(),
		).Warn("ssh audit: token username rejected")
		return nil, fmt.Errorf("auth failed")
	}

	keys, err := p.config.KeyStore.LookupKeysByFingerprint(ctx, fingerprint)
	if err != nil {
		logger.WithFields("user", idOrName, "fingerprint", fingerprint, "error", err.Error()).Warn("ssh auth: key lookup failed")
		return nil, fmt.Errorf("auth failed")
	}
	if len(keys) == 0 {
		logger.WithFields("user", idOrName, "fingerprint", fingerprint).Warn("ssh auth: key not found")
		return nil, fmt.Errorf("auth failed")
	}

	for i := range keys {
		sshKey := &keys[i]
		scope := sandbox.TenantInstanceScope{
			OrganizationID: sshKey.TenantID,
			TenantID:       sshKey.TenantID,
			InstanceID:     sshKey.TenantID,
		}
		inst, ok := p.lookupSandboxByIdentifierInScope(ctx, idOrName, scope)
		if !ok || inst == nil {
			continue
		}
		if !isSandboxRunningForSSH(inst) {
			logger.WithFields("user", idOrName, "sandbox_id", inst.ID, "status", inst.Status, "lifecycle", inst.LifecycleState).Warn("ssh auth: sandbox not running")
			continue
		}

		sandboxID := inst.ID
		tenantID := inst.Config.TenantID
		if tenantID == "" {
			tenantID = scope.SandboxTenantID()
		}

		hasAccess, accessErr := p.config.KeyStore.CheckAccess(ctx, sandboxID, sshKey.UserID, tenantID)
		if accessErr != nil || !hasAccess {
			p.logSSHAudit("ssh_key_auth_denied",
				"user", idOrName,
				"sandbox_id", sandboxID,
				"tenant_id", tenantID,
				"ssh_user_id", sshKey.UserID,
				"has_access", hasAccess,
			).Warn("ssh audit: key auth denied")
			continue
		}

		go p.config.KeyStore.TouchKeyLastUsed(context.Background(), sshKey.ID)
		p.logSSHAudit("ssh_key_auth_succeeded",
			"user", idOrName,
			"sandbox_id", sandboxID,
			"tenant_id", tenantID,
			"ssh_user_id", sshKey.UserID,
			"key_id", sshKey.ID,
		).Info("ssh audit: key auth succeeded")

		return &ssh.Permissions{
			Extensions: map[string]string{
				"user_id":    sshKey.UserID,
				"tenant_id":  tenantID,
				"sandbox_id": sandboxID,
			},
		}, nil
	}

	logger.WithFields("user", idOrName, "fingerprint", fingerprint).Warn("ssh auth: no scoped sandbox access matched")
	return nil, fmt.Errorf("auth failed")
}

func (p *Proxy) keyboardInteractiveCallback(connMeta ssh.ConnMetadata, challenge ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
	idOrName := connMeta.User()
	if !IsSandboxSSHTokenValue(idOrName) {
		answers, err := challenge(
			"Everstack temporary SSH token",
			"Paste the temporary SSH token generated for this sandbox.",
			[]string{"Token: "},
			[]bool{false},
		)
		if err != nil || len(answers) != 1 || strings.TrimSpace(answers[0]) == "" {
			p.logSSHAudit("ssh_token_prompt_failed",
				"user", idOrName,
				"remote", connMeta.RemoteAddr().String(),
			).Warn("ssh audit: token prompt failed")
			return nil, fmt.Errorf("auth failed")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return p.tokenPermissions(ctx, connMeta, strings.TrimSpace(answers[0]), idOrName)
	}
	p.logSSHAudit("ssh_token_username_rejected",
		"user", tokenLogPrefix(idOrName),
		"remote", connMeta.RemoteAddr().String(),
	).Warn("ssh audit: token username rejected")
	return nil, fmt.Errorf("auth failed")
}

func (p *Proxy) tokenPermissions(ctx context.Context, connMeta ssh.ConnMetadata, rawToken, idOrName string) (*ssh.Permissions, error) {
	token, err := p.config.KeyStore.LookupActiveSandboxSSHToken(ctx, rawToken)
	if err != nil {
		p.logSSHAudit("ssh_token_auth_failed",
			"token_prefix", tokenLogPrefix(rawToken),
			"user", idOrName,
			"remote", connMeta.RemoteAddr().String(),
			"reason", "lookup_failed",
			"error", err.Error(),
		).Warn("ssh audit: token auth failed")
		return nil, fmt.Errorf("auth failed")
	}

	scope := sandbox.SandboxScope{
		OrganizationID: token.OrganizationID,
		TenantID:       token.TenantID,
		InstanceID:     token.InstanceID,
		SandboxID:      token.SandboxID,
	}.Normalize()
	if !scope.Complete() {
		p.logSSHAudit("ssh_token_auth_failed",
			"sandbox_id", token.SandboxID,
			"token_id", token.ID,
			"token_prefix", token.TokenPrefix,
			"reason", "scope_incomplete",
		).Warn("ssh audit: token auth failed")
		return nil, fmt.Errorf("auth failed")
	}

	inst, ok := p.lookupSandboxByIdentifierInScope(ctx, token.SandboxID, scope.TenantInstance())
	if !ok {
		p.logSSHAudit("ssh_token_auth_failed",
			"sandbox_id", token.SandboxID,
			"token_id", token.ID,
			"token_prefix", token.TokenPrefix,
			"reason", "sandbox_not_found",
		).Warn("ssh audit: token auth failed")
		return nil, fmt.Errorf("auth failed")
	}
	if idOrName != "" {
		requested, requestedOK := p.lookupSandboxByIdentifierInScope(ctx, idOrName, scope.TenantInstance())
		if !requestedOK || requested == nil || requested.ID != token.SandboxID {
			p.logSSHAudit("ssh_token_auth_failed",
				"user", idOrName,
				"sandbox_id", token.SandboxID,
				"token_id", token.ID,
				"token_prefix", token.TokenPrefix,
				"reason", "username_scope_mismatch",
			).Warn("ssh audit: token auth failed")
			return nil, fmt.Errorf("auth failed")
		}
	}
	if !isSandboxRunningForSSH(inst) {
		p.logSSHAudit("ssh_token_auth_failed",
			"sandbox_id", inst.ID,
			"token_id", token.ID,
			"token_prefix", token.TokenPrefix,
			"status", inst.Status,
			"lifecycle", inst.LifecycleState,
			"reason", "sandbox_not_running",
		).Warn("ssh audit: token auth failed")
		return nil, fmt.Errorf("auth failed")
	}
	if !scope.MatchesInstance(inst) {
		p.logSSHAudit("ssh_token_auth_failed",
			"sandbox_id", inst.ID,
			"token_id", token.ID,
			"token_prefix", token.TokenPrefix,
			"reason", "sandbox_scope_mismatch",
		).Warn("ssh audit: token auth failed")
		return nil, fmt.Errorf("auth failed")
	}

	remoteIP, _, _ := net.SplitHostPort(connMeta.RemoteAddr().String())
	go p.config.KeyStore.TouchSandboxSSHToken(context.Background(), token.ID, remoteIP)
	p.logSSHAudit("ssh_token_auth_succeeded",
		"organization_id", token.OrganizationID,
		"tenant_id", token.TenantID,
		"instance_id", token.InstanceID,
		"sandbox_id", token.SandboxID,
		"token_id", token.ID,
		"token_prefix", token.TokenPrefix,
		"created_by", token.CreatedBy,
		"remote_ip", remoteIP,
	).Info("ssh audit: token auth succeeded")

	return &ssh.Permissions{
		Extensions: map[string]string{
			"auth_method":     "ssh_token",
			"token_id":        token.ID,
			"user_id":         token.CreatedBy,
			"organization_id": token.OrganizationID,
			"tenant_id":       token.TenantID,
			"instance_id":     token.InstanceID,
			"sandbox_id":      token.SandboxID,
		},
	}, nil
}

func isSandboxRunningForSSH(inst *sandbox.Instance) bool {
	if inst == nil {
		return false
	}
	if inst.LifecycleState != "" {
		return inst.LifecycleState == sandbox.LifecycleRunning
	}
	return inst.Status == sandbox.StatusRunning
}

func (p *Proxy) lookupSandboxByIdentifierInScope(ctx context.Context, idOrName string, scope sandbox.TenantInstanceScope) (*sandbox.Instance, bool) {
	if p.config.SandboxManager == nil || !scope.HasSandboxTenant() {
		return nil, false
	}
	inst, ok := p.config.SandboxManager.GetBySandboxIDOrNameInScope(idOrName, scope)
	if ok && inst != nil {
		return inst, true
	}
	dbInst, dbErr := p.config.SandboxManager.LookupInstanceByIDFromDBInScope(ctx, idOrName, scope)
	if dbErr != nil || dbInst == nil {
		return nil, false
	}
	return dbInst, true
}

func (p *Proxy) logSSHAudit(event string, fields ...interface{}) *logger.Entry {
	return logger.WithFields(fields...).WithLogEvent(event)
}

func tokenLogPrefix(rawToken string) string {
	if len(rawToken) <= 12 {
		return rawToken
	}
	return rawToken[:12]
}

// handleSession handles an SSH session channel (shell request).
func (p *Proxy) handleSession(ctx context.Context, sandboxID string, channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	var shellStarted bool
	var shellSession *sandbox.ShellSession

	for req := range requests {
		switch req.Type {
		case "pty-req":
			// Accept PTY request — dimensions are handled on shell request
			if req.WantReply {
				_ = req.Reply(true, nil)
			}

		case "window-change":
			if shellSession != nil && shellSession.Resize != nil && len(req.Payload) >= 8 {
				cols := uint16(req.Payload[0])<<8 | uint16(req.Payload[1])
				rows := uint16(req.Payload[2])<<8 | uint16(req.Payload[3])
				_ = shellSession.Resize(rows, cols)
			}
			if req.WantReply {
				_ = req.Reply(true, nil)
			}

		case "shell":
			if shellStarted {
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				continue
			}
			shellStarted = true

			// SSH sessions don't carry a known shell session ID — let
			// the backend create a fresh one. A future revision can
			// pipe a per-SSH-key session map so reconnects land back
			// in the same tmux session.
			sess, err := p.config.SandboxManager.ShellBySandboxID(ctx, sandboxID, []string{"/bin/sh", "-lc", "if command -v bash >/dev/null 2>&1; then exec bash -l; fi; exec /bin/sh"}, "")
			if err != nil {
				logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).Warn("ssh proxy: failed to open shell")
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				return
			}
			shellSession = sess

			if req.WantReply {
				_ = req.Reply(true, nil)
			}

			// Bidirectional I/O proxy
			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()
				_, _ = io.Copy(channel, sess.Conn)
				_ = channel.CloseWrite()
			}()

			go func() {
				defer wg.Done()
				_, _ = io.Copy(sess.Conn, channel)
				_ = sess.Conn.Close()
			}()

			wg.Wait()
			return

		case "env":
			// Ignore env requests
			if req.WantReply {
				_ = req.Reply(true, nil)
			}

		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// ─── Rate Limiting ──────────────────────────────────────────────────────

func (p *Proxy) allowConnection(ip string) bool {
	p.rateMu.Lock()
	defer p.rateMu.Unlock()

	entry, ok := p.rateLimits[ip]
	if !ok {
		// tokens per second = ConnRatePerIP / 60, burst = ConnRatePerIP
		r := rate.Limit(float64(p.config.ConnRatePerIP) / 60.0)
		entry = &rateLimitEntry{
			limiter:  rate.NewLimiter(r, p.config.ConnRatePerIP),
			lastSeen: time.Now(),
		}
		p.rateLimits[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter.Allow()
}

func (p *Proxy) cleanupRateLimiters(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.rateMu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for ip, entry := range p.rateLimits {
				if entry.lastSeen.Before(cutoff) {
					delete(p.rateLimits, ip)
				}
			}
			p.rateMu.Unlock()
		}
	}
}

// cleanupBansLoop drops expired ban + stale failure-history entries on
// a slow tick so the bans map doesn't grow unbounded under sustained
// probing.
func (p *Proxy) cleanupBansLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.cleanupBans()
		}
	}
}

// ─── Session Counting ───────────────────────────────────────────────────

func (p *Proxy) acquireSession(sandboxID string) bool {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()

	count := p.sessionCounts[sandboxID]
	if count >= p.config.MaxSessionsPerSandbox {
		return false
	}
	p.sessionCounts[sandboxID] = count + 1
	return true
}

func (p *Proxy) releaseSession(sandboxID string) {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()

	count := p.sessionCounts[sandboxID]
	if count <= 1 {
		delete(p.sessionCounts, sandboxID)
	} else {
		p.sessionCounts[sandboxID] = count - 1
	}
}
