package openaiapi

import (
	"context"
	"errors"
	"time"
)

var errSessionOwnershipLost = errors.New("OpenAI agent session ownership was lost")

func (s *Service) environmentTimeoutFence(o Ownership, id string) func(context.Context) error {
	return func(ctx context.Context) error {
		fs, ok := s.store.(EnvironmentCleanupFenceStore)
		if !ok {
			return ErrUnavailable
		}
		fenced, err := fs.FenceEnvironmentTimeout(ctx, o, id, s.holderID, "executor connection timed out")
		if err != nil {
			return err
		}
		if !fenced {
			return errSessionOwnershipLost
		}
		return nil
	}
}

type sessionOwnership struct {
	owner  Ownership
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *Service) acquireSessionOwnership(ctx context.Context, o Ownership, id string) (owned, newlyOwned bool, err error) {
	ls, ok := s.store.(SessionLeaseStore)
	if !ok {
		return true, false, nil
	}
	s.leaseMu.Lock()
	localOwner := s.leasing[id]
	s.leaseMu.Unlock()
	if localOwner != nil {
		if localOwner.owner.OrganizationID != o.OrganizationID || localOwner.owner.InstanceID != o.InstanceID {
			return false, false, ErrNotFound
		}
		return true, false, nil
	}
	callStarted := time.Now()
	acquired, err := ls.AcquireSessionLease(ctx, o, id, s.holderID, s.leaseDuration)
	if err != nil || !acquired {
		return acquired, false, err
	}
	deadline := callStarted.Add(s.leaseDuration)
	if !time.Now().Before(deadline) {
		_ = ls.ReleaseSessionLease(ctx, o, id, s.holderID)
		return false, false, context.DeadlineExceeded
	}
	leaseCtx, leaseCancel := context.WithCancel(s.supervisorCtx)
	s.leaseMu.Lock()
	if raced := s.leasing[id]; raced != nil {
		s.leaseMu.Unlock()
		leaseCancel()
		if raced.owner.OrganizationID != o.OrganizationID || raced.owner.InstanceID != o.InstanceID {
			return false, false, ErrNotFound
		}
		return true, false, nil
	}
	ownership := &sessionOwnership{owner: o, ctx: leaseCtx, cancel: leaseCancel}
	s.leasing[id] = ownership
	s.leaseMu.Unlock()
	s.supervisorMu.Lock()
	if s.closing {
		s.supervisorMu.Unlock()
		s.leaseMu.Lock()
		delete(s.leasing, id)
		s.leaseMu.Unlock()
		leaseCancel()
		_ = ls.ReleaseSessionLease(ctx, o, id, s.holderID)
		return false, false, context.Canceled
	}
	s.supervisorWG.Add(1)
	s.supervisorMu.Unlock()
	go s.renewSessionOwnership(ownership, id, ls, deadline)
	return true, true, nil
}

func (s *Service) sessionOwnershipContext(id string) context.Context {
	s.leaseMu.Lock()
	defer s.leaseMu.Unlock()
	if owned := s.leasing[id]; owned != nil {
		return owned.ctx
	}
	return nil
}

func (s *Service) renewSessionOwnership(ownership *sessionOwnership, id string, ls SessionLeaseStore, deadline time.Time) {
	o := ownership.owner
	defer s.supervisorWG.Done()
	ticker := time.NewTicker(s.leaseDuration / 3)
	defer ticker.Stop()
	expiry := time.NewTimer(time.Until(deadline))
	defer expiry.Stop()
	defer func() {
		ownership.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ls.ReleaseSessionLease(ctx, o, id, s.holderID)
		s.leaseMu.Lock()
		if s.leasing[id] == ownership {
			delete(s.leasing, id)
		}
		s.leaseMu.Unlock()
	}()
	for {
		select {
		case <-s.supervisorCtx.Done():
			s.cancelOwnedSession(o, id)
			return
		case <-expiry.C:
			s.cancelOwnedSession(o, id)
			return
		case <-ticker.C:
			remaining := time.Until(deadline)
			if remaining <= 0 {
				s.cancelOwnedSession(o, id)
				return
			}
			callTimeout := s.leaseDuration / 3
			if callTimeout >= remaining {
				callTimeout = remaining / 2
			}
			if s.recoveryAuth != nil {
				authCtx, authCancel := context.WithTimeout(ownership.ctx, callTimeout)
				type authorizationResult struct {
					owner Ownership
					err   error
				}
				authResult := make(chan authorizationResult, 1)
				go func() {
					refreshed, authErr := s.recoveryAuth.AuthorizeRecovery(authCtx, o)
					authResult <- authorizationResult{owner: refreshed, err: authErr}
				}()
				var refreshed Ownership
				var authErr error
				select {
				case got := <-authResult:
					refreshed, authErr = got.owner, got.err
				case <-authCtx.Done():
					authErr = authCtx.Err()
				case <-expiry.C:
					authCancel()
					s.cancelOwnedSession(o, id)
					return
				case <-ownership.ctx.Done():
					authCancel()
					s.cancelOwnedSession(o, id)
					return
				}
				authCancel()
				if authErr != nil || authorize(refreshed, "session:write") != nil || !time.Now().Before(deadline) {
					s.cancelOwnedSession(o, id)
					stateCtx, stateCancel := context.WithTimeout(context.Background(), 2*time.Second)
					_ = s.store.UpdateSessionState(stateCtx, o, id, "recovery_pending", "failed", "session owner authorization was revoked or became unavailable")
					stateCancel()
					return
				}
				o = refreshed
			}
			callStarted := time.Now()
			ctx, cancel := context.WithTimeout(ownership.ctx, callTimeout)
			type renewalResult struct {
				renewed bool
				err     error
			}
			result := make(chan renewalResult, 1)
			go func() {
				renewed, err := ls.RenewSessionLease(ctx, o, id, s.holderID, s.leaseDuration)
				result <- renewalResult{renewed, err}
			}()
			var renewed bool
			var err error
			select {
			case got := <-result:
				renewed, err = got.renewed, got.err
			case <-expiry.C:
				cancel()
				s.cancelOwnedSession(o, id)
				return
			case <-ownership.ctx.Done():
				cancel()
				s.cancelOwnedSession(o, id)
				return
			}
			cancel()
			candidateDeadline := callStarted.Add(s.leaseDuration)
			if err == nil && renewed && time.Now().Before(deadline) && time.Now().Before(candidateDeadline) {
				deadline = candidateDeadline
				if !expiry.Stop() {
					select {
					case <-expiry.C:
					default:
					}
				}
				expiry.Reset(time.Until(deadline))
				continue
			}
			if err == nil || !time.Now().Before(deadline) {
				s.cancelOwnedSession(o, id)
				return
			}
		}
	}
}

func (s *Service) cancelOwnedSession(o Ownership, id string) {
	s.leaseMu.Lock()
	if owned := s.leasing[id]; owned != nil {
		owned.cancel()
	}
	s.leaseMu.Unlock()
	s.supervisorMu.Lock()
	if cancel := s.supervising[id]; cancel != nil {
		cancel()
	}
	s.supervisorMu.Unlock()
	if s.environments != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.environments.Relinquish(ctx, o, id)
	}
}
