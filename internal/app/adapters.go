package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"cc-fuse/internal/domain"
	"cc-fuse/internal/monitor"
	"cc-fuse/internal/policy"
	"cc-fuse/internal/quotamode"
	"cc-fuse/internal/store"
	"cc-fuse/internal/switcher"
)

// Monitor queries one account's quota; the real *monitor.Client already has
// exactly this shape.
type Monitor interface {
	Query(ctx context.Context, p domain.Profile) domain.QuotaSnapshot
}

// Switcher applies one profile onto the settings file. The real switcher
// side is the package-level switcher.Apply (default backup dir) — wrap it in
// SwitcherFunc when wiring main.
type Switcher interface {
	Apply(p domain.Profile, settingsPath string) error
}

// QuotaFlipper rewrites the quota-mode file's mode: line; the real side is
// the package-level quotamode.Flip.
type QuotaFlipper interface {
	Flip(path, mode string) error
}

// Notifier raises a user-visible Windows notification (title + body).
type Notifier interface {
	Notify(title, body string)
}

// SwitcherFunc adapts a plain function to Switcher (DI glue for main).
type SwitcherFunc func(p domain.Profile, settingsPath string) error

// Apply implements Switcher.
func (f SwitcherFunc) Apply(p domain.Profile, settingsPath string) error { return f(p, settingsPath) }

// QuotaFlipperFunc adapts a plain function to QuotaFlipper (DI glue for main).
type QuotaFlipperFunc func(path, mode string) error

// Flip implements QuotaFlipper.
func (f QuotaFlipperFunc) Flip(path, mode string) error { return f(path, mode) }

// NotifierFunc adapts a plain function to Notifier (DI glue for main).
type NotifierFunc func(title, body string)

// Notify implements Notifier.
func (f NotifierFunc) Notify(title, body string) { f(title, body) }

// MonitorPool routes each Query to a monitor.Client bound to the profile's
// own BaseURL, building clients lazily. In practice A and B share one z.ai
// host and share one client, but profiles with different endpoints keep
// working without re-wiring (the Loop holds a single Monitor).
type MonitorPool struct {
	mu      sync.Mutex
	timeout time.Duration
	pool    map[string]*monitor.Client // key = profile BaseURL
}

// NewMonitorPool builds a pool whose clients use the given per-request HTTP
// timeout (production: 15s per T01).
func NewMonitorPool(timeout time.Duration) *MonitorPool {
	return &MonitorPool{timeout: timeout, pool: map[string]*monitor.Client{}}
}

// Query sends the quota request against p.BaseURL's client.
func (p *MonitorPool) Query(ctx context.Context, prof domain.Profile) domain.QuotaSnapshot {
	c := p.client(prof.BaseURL)
	return c.Query(ctx, prof)
}

// client returns (building if needed) the monitor client for one base URL.
func (p *MonitorPool) client(baseURL string) *monitor.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.pool[baseURL]
	if !ok {
		c = monitor.New(baseURL, p.timeout)
		p.pool[baseURL] = c
	}
	return c
}

// LoadBudget seeds the Loop's Budget from state.json (cold start: missing
// file / no budget payload → zero value, policy settles from current
// numbers). Corrupt JSON is surfaced for the boot log; the app still starts
// with a zero budget rather than refusing to run.
func LoadBudget(statePath string) (policy.BudgetState, error) {
	st, err := store.LoadState(statePath)
	if err != nil {
		return policy.BudgetState{}, err
	}
	if len(st.Budget) == 0 {
		return policy.BudgetState{}, nil
	}
	var b policy.BudgetState
	if err := json.Unmarshal(st.Budget, &b); err != nil {
		return policy.BudgetState{}, fmt.Errorf("budget payload in %s: %w", statePath, err)
	}
	return b, nil
}

// Compile-time proof the real collaborators fit the Loop's seams.
var (
	_ Monitor      = (*monitor.Client)(nil)
	_ Switcher     = SwitcherFunc(switcher.Apply)
	_ QuotaFlipper = QuotaFlipperFunc(quotamode.Flip)
	_ Notifier     = NotifierFunc(func(title, body string) {})
)
