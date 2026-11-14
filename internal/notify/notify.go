// Package notify raises user-visible Windows Toast notifications (design §8:
// 每次自動切換、預警、暫停都發 Windows Toast).
//
// It implements app.Notifier so main can inject it straight into the Loop;
// errors are logged, never propagated — a failed toast must not fail the
// decision that caused it. go-toast drives the Windows runtime API and
// needs an installed AppID (PowerShell's is the well-known fallback).
package notify

import (
	"log"

	"github.com/go-toast/toast"

	"cc-fuse/internal/app"
)

// appID is the toast source shown in the Action Center. Using PowerShell's
// well-known AppID avoids per-app registration (start-menu shortcut) while
// still producing a normal Windows toast.
const appID = "Microsoft.Windows.PowerShell"

// Windows is the production Notifier: every Notify call pops one toast.
type Windows struct {
	// Log receives toast failures (optional; nil = drop silently).
	Log *log.Logger
}

// compile-time proof this satisfies the Loop's notification seam.
var _ app.Notifier = Windows{}

// Notify shows a toast with the given title/body.
func (n Windows) Notify(title, body string) {
	t := toast.Notification{
		AppID:   appID,
		Title:   title,
		Message: body,
		Audio:   toast.Default,
	}
	if err := t.Push(); err != nil && n.Log != nil {
		n.Log.Printf("toast 失敗(%s):%v", title, err)
	}
}
