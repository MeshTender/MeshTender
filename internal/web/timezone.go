package web

import (
	"time"
	// Embed the IANA tz database so time.LoadLocation resolves every zone name
	// regardless of the runtime environment. The production image is
	// distroless/static and the build is CGO_ENABLED=0, so without this the OS
	// zoneinfo may be absent and validation would reject valid zones. Embedding it
	// here (where the only LoadLocation call lives) also makes ValidTimeZone
	// deterministic in tests, independent of the host's zoneinfo.
	_ "time/tzdata"
)

// ValidTimeZone reports whether name is an acceptable stored zone: "" (meaning
// auto-detect in the browser) or a real IANA name that time.LoadLocation
// resolves. The zone list itself isn't maintained here — the account picker is
// populated in the browser from Intl.supportedValuesOf("timeZone"); this is just
// the server-side guard that the submitted value is a genuine zone.
//
// "Local" is rejected: LoadLocation accepts it (the server's own zone), but it's
// meaningless as a per-user display preference and wouldn't round-trip to the
// browser's Intl.
func ValidTimeZone(name string) bool {
	if name == "" {
		return true
	}
	if name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}
