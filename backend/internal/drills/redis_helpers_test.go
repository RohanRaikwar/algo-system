//go:build drills

package drills

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	goredis "github.com/go-redis/redis/v8"
)

// redisProc is a throwaway redis-server with persistence disabled, so a
// restart behaves like a production Redis restart without AOF/RDB: all
// streams, keys and consumer groups are gone.
type redisProc struct {
	t    *testing.T
	port int
	cmd  *exec.Cmd
}

func (r *redisProc) addr() string { return "127.0.0.1:" + strconv.Itoa(r.port) }

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func startRedis(t *testing.T) *redisProc {
	t.Helper()
	if _, err := exec.LookPath("redis-server"); err != nil {
		t.Skip("redis-server not on PATH")
	}
	r := &redisProc{t: t, port: freePort(t)}
	r.start()
	t.Cleanup(r.kill)
	return r
}

func (r *redisProc) start() {
	r.t.Helper()
	r.cmd = exec.Command("redis-server",
		"--port", strconv.Itoa(r.port), "--bind", "127.0.0.1",
		"--save", "", "--appendonly", "no", "--daemonize", "no", "--loglevel", "warning")
	if err := r.cmd.Start(); err != nil {
		r.t.Fatalf("start redis-server: %v", err)
	}
	c := goredis.NewClient(&goredis.Options{Addr: r.addr()})
	defer c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		err := c.Ping(ctx).Err()
		cancel()
		if err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.t.Fatalf("redis-server on %s did not come up", r.addr())
}

// kill is SIGKILL: connections are reset, clients get immediate errors.
func (r *redisProc) kill() {
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(syscall.SIGCONT) // in case it was stopped
		_ = r.cmd.Process.Kill()
		_, _ = r.cmd.Process.Wait()
		r.cmd = nil
	}
}

// stall is SIGSTOP: sockets stay open but nothing answers — the case that
// only a client-side deadline can bound.
func (r *redisProc) stall() {
	if err := r.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		r.t.Fatalf("SIGSTOP: %v", err)
	}
}

func (r *redisProc) resume() {
	if err := r.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		r.t.Fatalf("SIGCONT: %v", err)
	}
}

func (r *redisProc) restart() {
	r.kill()
	r.start()
}

// failCounter records Writer.OnWriteError calls by op.
type failCounter struct {
	ch chan string
	n  map[string]int
}

func newFailCounter() *failCounter {
	return &failCounter{ch: make(chan string, 1<<16), n: map[string]int{}}
}

func (f *failCounter) hook(op string) {
	select {
	case f.ch <- op:
	default:
	}
}

// drain folds pending hook calls into the totals and returns them.
func (f *failCounter) drain() map[string]int {
	for {
		select {
		case op := <-f.ch:
			f.n[op]++
		default:
			out := make(map[string]int, len(f.n))
			for k, v := range f.n {
				out[k] = v
			}
			return out
		}
	}
}

func (f *failCounter) String() string { return fmt.Sprint(f.drain()) }
