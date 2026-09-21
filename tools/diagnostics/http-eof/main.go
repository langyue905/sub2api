// Run with: go run tools/diagnostics/http-eof/main.go
// This bounded diagnostic only uses a localhost HTTP/1 server. It does not
// connect to a real upstream or change application transport settings.
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	fmt.Println(runtime.Version())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.completed\"}\n\n")
		w.(http.Flusher).Flush()
		// Leave the reader in Read while the close goroutine starts.
		time.Sleep(time.Millisecond)
	}))
	defer server.Close()
	for _, mode := range []struct {
		name   string
		reuse  bool
		mixed  bool
		cancel bool
	}{
		{name: "read-to-EOF", reuse: true},
		{name: "mixed-concurrent-close", reuse: true, mixed: true},
		{name: "mixed-cancel-before-close", reuse: true, mixed: true, cancel: true},
		{name: "mixed-without-reuse", mixed: true},
	} {
		tr := &http.Transport{MaxConnsPerHost: 4, MaxIdleConnsPerHost: 4, DisableKeepAlives: !mode.reuse, IdleConnTimeout: 200 * time.Millisecond}
		client := &http.Client{Transport: tr}
		var wg sync.WaitGroup
		var total, slow, normalSlow, readErrors, requestErrors, eofWaitSnapshots atomic.Int64
		stopWatch := make(chan struct{})
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			ticker := time.NewTicker(25 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopWatch:
					return
				case <-ticker.C:
					buf := make([]byte, 1<<20)
					n := runtime.Stack(buf, true)
					count := int64(strings.Count(string(buf[:n]), "net/http.(*persistConn).readLoop.func4"))
					if count > eofWaitSnapshots.Load() {
						eofWaitSnapshots.Store(count)
					}
				}
			}
		}()
		start := time.Now()
		for j := 0; j < 12; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 60; i++ {
					ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
					req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
					resp, err := client.Do(req)
					if err != nil {
						requestErrors.Add(1)
						cancel()
						continue
					}
					scanner := bufio.NewScanner(resp.Body)
					if !scanner.Scan() {
						readErrors.Add(1)
						cancel()
						resp.Body.Close()
						continue
					}
					completed := time.Now()
					closeConcurrently := mode.mixed && i%4 == 0
					closed := make(chan struct{})
					if closeConcurrently {
						go func() {
							if mode.cancel {
								cancel()
							}
							resp.Body.Close()
							close(closed)
						}()
					} else {
						close(closed)
					}
					for scanner.Scan() {
					}
					if scanner.Err() != nil {
						readErrors.Add(1)
					}
					<-closed
					if time.Since(completed) > 100*time.Millisecond {
						slow.Add(1)
						if !closeConcurrently {
							normalSlow.Add(1)
						}
					}
					resp.Body.Close()
					cancel()
					total.Add(1)
				}
			}()
		}
		wg.Wait()
		close(stopWatch)
		<-watchDone
		tr.CloseIdleConnections()
		fmt.Printf("mode=%s requests=%d slowEOF=%d normalReaderSlow=%d requestErrors=%d readErrors=%d blockedEOFSnapshot=%d elapsed=%v\n",
			mode.name, total.Load(), slow.Load(), normalSlow.Load(), requestErrors.Load(), readErrors.Load(), eofWaitSnapshots.Load(), time.Since(start))
	}
}
