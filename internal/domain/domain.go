// Package domain holds the shared value types every cc-fuse unit speaks.
//
// T02 (policy) and T03 (tray shell) consume only these types plus the
// service interfaces of monitor/switcher/quotamode/config/store, so the
// shapes here are the cross-card contract and must change only with care.
package domain

import "time"

// Profile is one z.ai account cc-fuse can switch Claude Code onto.
//
// ID is the stable cross-card handle, always "A" (team shared, preferred)
// or "B" (personal, refuge + pacing budget). APIKey doubles as the provider
// identity fingerprint: A and B share the same endpoint host, so "who is
// active" is decided purely by comparing this key against settings.json.
// ModelEnv carries the profile's own ANTHROPIC_DEFAULT_* model mappings;
// the switcher applies the full set and removes keys the target lacks.
type Profile struct {
	ID       string            `json:"id"`       // "A" or "B"
	Name     string            `json:"name"`     // human label, shown in the tray UI
	APIKey   string            `json:"apiKey"`   // z.ai key; also the identity fingerprint
	BaseURL  string            `json:"baseUrl"`  // coding endpoint, may carry an /api/anthropic suffix
	ModelEnv map[string]string `json:"modelEnv"` // ANTHROPIC_DEFAULT_* model mapping applied on switch
}

// QuotaSnapshot is one monitor sample for one profile: the two usage
// windows plus the plan level, or the failure that replaced them.
//
// Nil percentage pointer = that window is missing entirely (e.g. legacy
// plans report no weekly bucket); a pointer to 0 is a real 0%. Err carries
// the query/parse failure (network, business error, dead key); HTTPStatus
// keeps the raw response code so 401/403 can be told apart from HTTP-200
// business errors. On failure the snapshot still carries ProfileID and
// QueriedAt so the policy engine can age the sample out.
type QuotaSnapshot struct {
	ProfileID     string     `json:"profileId,omitempty"`     // which profile this sample belongs to
	FiveHourPct   *float64   `json:"fiveHourPct,omitempty"`   // 5h window, USED percent, unclamped; nil = bucket absent
	FiveHourReset *time.Time `json:"fiveHourReset,omitempty"` // 5h reset instant; nil = absent (real shape at 0%)
	WeeklyPct     *float64   `json:"weeklyPct,omitempty"`     // weekly window, USED percent, unclamped; nil = bucket absent
	WeeklyReset   *time.Time `json:"weeklyReset,omitempty"`   // weekly reset instant; nil = absent
	PlanLevel     string     `json:"planLevel,omitempty"`     // plan level string from data.level, e.g. "glm-5.3-plan"
	QueriedAt     time.Time  `json:"queriedAt"`               // local sample time; zero only for bare Parse results
	Err           error      `json:"-"`                       // query/parse failure; nil = the percentages are usable (never marshaled)
	HTTPStatus    int        `json:"httpStatus,omitempty"`    // raw HTTP status; 401/403 = dead key, 200 + Err = business error
}
