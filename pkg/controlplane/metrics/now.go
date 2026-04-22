package metrics

import "time"

// timeNow is a package-level variable so tests can freeze the clock when
// asserting worker liveness math.
var timeNow = func() time.Time { return time.Now() }
