//    This file is part of conntrack-stats-exporter.
//
//    conntrack-stats-exporter is free software: you can redistribute it and/or
//    modify it under the terms of the GNU General Public License as published
//    by the Free Software Foundation, either version 3 of the License, or (at
//    your option) any later version.
//
//    conntrack-stats-exporter is distributed in the hope that it will be
//    useful, but WITHOUT ANY WARRANTY; without even the implied warranty of
//    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU General
//    Public License for more details.
//
//    You should have received a copy of the GNU General Public License along
//    with conntrack-stats-exporter.  If not, see
//    <http://www.gnu.org/licenses/>.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jwkohnen/conntrack-stats-exporter/exporter"
)

func main() {
	// Set Go max procs to 1 even if number of (logical) CPUs is > 1.  This is a low performance program that might run
	// in an environment with very limited CPU resources via cgroups (e.g. Kubernetes resource limit).  GOMAXPROCS of 1
	// prevents the Go scheduler from using too much scheduler overhead in such environments.
	//
	// Usually I'd use go.uber.org/automaxprocs/maxprocs, but hard coding 1 is a better solution than having another
	// dependency.
	_ = runtime.GOMAXPROCS(1)

	if os.Getenv("GOGC") == "" {
		// Reduce memory overhead. This is a low performance program;
		// the CPU penalty is negligible.
		debug.SetGCPercent(10)
	}

	var (
		addr             = ":9371"
		path             = "/metrics"
		netns            = ""
		timeoutGathering = time.Second * 5
		timeoutShutdown  = time.Second * 3
		timeoutHTTP      = time.Second * 10
	)

	var fs = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	fs.StringVar(&path, "path", path, "metrics endpoint path")
	fs.StringVar(&addr, "addr", addr, "TCP address to listen on")
	fs.StringVar(&netns, "netns", netns, "List of netns names separated by comma")
	fs.DurationVar(&timeoutGathering, "timeout-gathering", timeoutGathering, "timeout for gathering metrics")
	fs.DurationVar(&timeoutShutdown, "timeout-shutdown", timeoutShutdown, "timeout for graceful shutdown")
	fs.DurationVar(&timeoutHTTP, "timeout-http", timeoutHTTP, "timeout for HTTP requests")

	_ = fs.Parse(os.Args[1:])

	mux := http.NewServeMux()
	mux.Handle(
		path,
		newAbortHandler(
			exporter.Handler(
				exporter.WithErrorLogWriter(os.Stderr),
				exporter.WithNetNs(strings.Split(netns, ",")),
				exporter.WithTimeout(timeoutGathering),
			),
		),
	)

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  timeoutHTTP,
		WriteTimeout: timeoutHTTP,
	}

	shutdown := make(chan os.Signal, 1)

	var (
		receivedSignal os.Signal
		wg             sync.WaitGroup
	)

	wg.Add(1)

	go func() {
		defer wg.Done()

		// Sadly Kubernetes sends SIGTERM, not SIGINT.  CTRL+C on a TTY sends SIGINT.
		signal.Notify(shutdown, os.Interrupt)
		signal.Notify(shutdown, syscall.SIGTERM)

		receivedSignal = <-shutdown

		signal.Stop(shutdown)

		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeoutShutdown)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			abort(fmt.Errorf("error shutting down server: %w", err))
		}
	}()

	_, _ = fmt.Fprintf(os.Stderr, "listening on %s with endpoint %q\n", addr, path)
	err := srv.ListenAndServe()

	wg.Wait()

	if errors.Is(err, http.ErrServerClosed) {
		const signaledExitCodeBase = 128

		os.Exit(signaledExitCodeBase + int(receivedSignal.(syscall.Signal)))
	}

	if err != nil {
		abort(err)
	}
}
