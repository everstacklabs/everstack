package browserpool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

func resetDefaultBrowserContext(ctx context.Context, cdpBaseURL string) error {
	ctx, cancelReset := context.WithTimeout(ctx, 30*time.Second)
	defer cancelReset()
	wsURL, err := resolveCDPURL(ctx, cdpBaseURL)
	if err != nil {
		return fmt.Errorf("browserpool: resolve CDP URL: %w", err)
	}
	browser, cancel := rod.New().ControlURL(wsURL).Context(ctx).WithCancel()
	defer cancel()
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("browserpool: connect to browser: %w", err)
	}

	// Every reset step is attempted before verification. Any individual error
	// still makes the pod unsafe for another session and therefore disposable.
	var resetErrors []error
	var survivor *rod.Page
	var pages []*proto.TargetTargetInfo
	targets, err := (proto.TargetGetTargets{}).Call(browser)
	if err != nil {
		resetErrors = append(resetErrors, fmt.Errorf("browserpool: list page targets: %w", err))
	} else {
		pages = pageTargets(targets.TargetInfos)
		if len(pages) == 0 {
			survivor, err = browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
			if err != nil {
				resetErrors = append(resetErrors, fmt.Errorf("browserpool: create blank survivor page: %w", err))
			}
		} else {
			survivorTarget := chooseSurvivorTarget(pages)
			survivor, err = browser.PageFromTarget(survivorTarget.TargetID)
			if err != nil {
				resetErrors = append(resetErrors, fmt.Errorf("browserpool: attach to survivor page: %w", err))
			}
		}
	}

	if survivor == nil {
		resetErrors = append(resetErrors, fmt.Errorf("browserpool: no page target available for Network reset"))
	} else {
		if err := (proto.NetworkClearBrowserCookies{}).Call(survivor); err != nil {
			resetErrors = append(resetErrors, fmt.Errorf("browserpool: clear cookies: %w", err))
		}
		if err := (proto.NetworkClearBrowserCache{}).Call(survivor); err != nil {
			resetErrors = append(resetErrors, fmt.Errorf("browserpool: clear cache: %w", err))
		}
	}
	if err := (proto.StorageClearDataForOrigin{Origin: "*", StorageTypes: "all"}).Call(browser); err != nil {
		resetErrors = append(resetErrors, fmt.Errorf("browserpool: clear storage: %w", err))
	}

	if survivor != nil {
		for _, target := range pages {
			if target.TargetID == survivor.TargetID {
				continue
			}
			if _, closeErr := (proto.TargetCloseTarget{TargetID: target.TargetID}).Call(browser); closeErr != nil {
				resetErrors = append(resetErrors, fmt.Errorf("browserpool: close page target %s: %w", target.TargetID, closeErr))
			}
		}
		if err := survivor.Navigate("about:blank"); err != nil {
			resetErrors = append(resetErrors, fmt.Errorf("browserpool: navigate survivor to about:blank: %w", err))
		}
	}

	if err := verifyBrowserReset(browser); err != nil {
		resetErrors = append(resetErrors, err)
	}
	return errors.Join(resetErrors...)
}

func resolveCDPURL(ctx context.Context, cdpBaseURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	type result struct {
		url string
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		resolved := result{}
		defer func() {
			if recovered := recover(); recovered != nil {
				resolved.err = fmt.Errorf("browserpool: launcher.ResolveURL panicked: %v", recovered)
			}
			resultCh <- resolved
		}()
		resolved.url, resolved.err = launcher.ResolveURL(cdpBaseURL)
	}()

	select {
	case resolved := <-resultCh:
		if resolved.err != nil {
			return "", fmt.Errorf("browserpool: resolve CDP endpoint: %w", resolved.err)
		}
		return resolved.url, nil
	case <-ctx.Done():
		return "", fmt.Errorf("browserpool: resolve CDP endpoint: %w", ctx.Err())
	}
}

func verifyBrowserReset(browser *rod.Browser) error {
	var verificationErrors []error
	cookies, err := (proto.StorageGetCookies{}).Call(browser)
	if err != nil {
		verificationErrors = append(verificationErrors, fmt.Errorf("browserpool: verify cookies cleared: %w", err))
	} else if len(cookies.Cookies) != 0 {
		verificationErrors = append(verificationErrors, fmt.Errorf("browserpool: verify cookies cleared: %d cookies remain", len(cookies.Cookies)))
	}

	targets, err := (proto.TargetGetTargets{}).Call(browser)
	if err != nil {
		verificationErrors = append(verificationErrors, fmt.Errorf("browserpool: verify page targets: %w", err))
	} else {
		pages := pageTargets(targets.TargetInfos)
		if len(pages) != 1 {
			verificationErrors = append(verificationErrors, fmt.Errorf("browserpool: verify page targets: expected 1 page, found %d", len(pages)))
		} else if pages[0].URL != "about:blank" {
			verificationErrors = append(verificationErrors, fmt.Errorf("browserpool: verify survivor page: expected about:blank, found %q", pages[0].URL))
		}
	}
	return errors.Join(verificationErrors...)
}

func chooseSurvivorTarget(pages []*proto.TargetTargetInfo) *proto.TargetTargetInfo {
	for _, target := range pages {
		if target.Attached {
			return target
		}
	}
	return pages[0]
}

func pageTargets(targets []*proto.TargetTargetInfo) []*proto.TargetTargetInfo {
	pages := make([]*proto.TargetTargetInfo, 0, len(targets))
	for _, target := range targets {
		if target.Type == proto.TargetTargetInfoTypePage {
			pages = append(pages, target)
		}
	}
	return pages
}
