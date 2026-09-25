// Package drills holds fault-injection drills for the market-data and
// storage paths. The drills are tests behind the `drills` build tag, so a
// normal `go test ./...` skips them:
//
//	cd backend && go test -tags drills -v ./internal/drills/
//
// They need a redis-server binary on PATH (they start their own throwaway
// instance on a free port and never touch REDIS_ADDR). See docs/ops/DRILLS.md
// for what each drill proves and for the manual drills that cannot be
// automated without hooks in production packages.
package drills
