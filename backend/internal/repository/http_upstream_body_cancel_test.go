package repository

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type bodyCancelObservedConn struct {
	net.Conn
	armed   *atomic.Bool
	entered chan struct{}
}

func (c *bodyCancelObservedConn) Read(p []byte) (int, error) {
	if c.armed.CompareAndSwap(true, false) {
		close(c.entered)
	}
	return c.Conn.Read(p)
}

// Reproduce the actual HTTP/1 EOF handoff, with explicit ordering instead of
// relying on scheduler luck: Read is blocked on the socket, Close returns,
// then the server sends EOF. The old transport path strands the reader until
// the idle connection eventually closes (or another response supplies eofc).
func TestHTTPUpstreamConcurrentCloseDoesNotStrandEOF(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseServer := func() { releaseOnce.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "x")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer releaseServer()
	parent, cancelParent := context.WithCancel(t.Context())
	defer cancelParent()
	var armed atomic.Bool
	entered := make(chan struct{})
	tr := &http.Transport{
		IdleConnTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &bodyCancelObservedConn{Conn: conn, armed: &armed, entered: entered}, nil
		},
	}
	defer tr.CloseIdleConnections()
	entry, do := bodyCancelTestUpstream(t, &http.Client{Transport: tr}, false)
	req, err := http.NewRequestWithContext(parent, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := do(req)
	require.NoError(t, err)
	_, err = io.ReadFull(resp.Body, make([]byte, 1))
	require.NoError(t, err)
	armed.Store(true)
	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(readDone) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not reach the socket")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- resp.Body.Close() }()
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		cancelParent()
		<-closeDone
		t.Fatal("Close blocked before server EOF")
	}
	releaseServer()
	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		cancelParent()
		tr.CloseIdleConnections()
		<-readDone
		t.Fatal("reader stranded at HTTP EOF after concurrent Close")
	}
	require.NoError(t, parent.Err())
	require.Zero(t, atomic.LoadInt64(&entry.inFlight))
}

// Inject a trusted localhost client into each real application acquisition path.
// The TLS case exercises DoWithTLS lifecycle ownership, not a uTLS handshake.
func bodyCancelTestUpstream(t *testing.T, client *http.Client, fingerprint bool) (*upstreamClientEntry, func(*http.Request) (*http.Response, error)) {
	t.Helper()
	svc := NewHTTPUpstream(nil).(*httpUpstreamService)
	var entry *upstreamClientEntry
	var err error
	profile := &tlsfingerprint.Profile{Name: "body-close-test"}
	if fingerprint {
		entry, err = svc.getClientEntryWithTLS("", 123, 4, profile, service.HTTPUpstreamProfileDefault, false, true)
	} else {
		entry, err = svc.acquireClient("", 123, 4)
		if err == nil {
			atomic.AddInt64(&entry.inFlight, -1)
		}
	}
	require.NoError(t, err)
	entry.client.CloseIdleConnections()
	entry.client = client
	t.Cleanup(client.CloseIdleConnections)
	return entry, func(req *http.Request) (*http.Response, error) {
		if fingerprint {
			return svc.DoWithTLS(req, "", 123, 4, profile)
		}
		return svc.Do(req, "", 123, 4)
	}
}

func TestHTTPUpstreamBodyCloseInterruptsRead(t *testing.T) {
	for _, mode := range []string{"http1", "http2", "tls-entry"} {
		t.Run(mode, func(t *testing.T) {
			serverCanceled := make(chan struct{})
			release := make(chan struct{})
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "x")
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					close(serverCanceled)
				case <-release:
				}
			}))
			srv.EnableHTTP2 = mode == "http2"
			srv.StartTLS()
			defer srv.Close()
			defer close(release)
			parent, cancelParent := context.WithCancel(t.Context())
			defer cancelParent()
			entry, do := bodyCancelTestUpstream(t, srv.Client(), mode == "tls-entry")
			req, err := http.NewRequestWithContext(parent, http.MethodGet, srv.URL, nil)
			require.NoError(t, err)
			resp, err := do(req)
			require.NoError(t, err)
			wantProto := 1
			if mode == "http2" {
				wantProto = 2
			}
			require.Equal(t, wantProto, resp.ProtoMajor)
			_, err = io.ReadFull(resp.Body, make([]byte, 1))
			require.NoError(t, err)

			readDone := make(chan error, 1)
			readStarted := make(chan struct{})
			go func() {
				close(readStarted)
				_, readErr := resp.Body.Read(make([]byte, 1))
				readDone <- readErr
			}()
			<-readStarted
			closeDone := make(chan error, 1)
			go func() { closeDone <- resp.Body.Close() }()
			select {
			case err := <-readDone:
				require.Error(t, err, "closing the response must interrupt its reader")
			case <-time.After(2 * time.Second):
				cancelParent()
				<-readDone
				<-closeDone
				t.Fatal("response Close left its reader blocked")
			}
			select {
			case err := <-closeDone:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				cancelParent()
				<-closeDone
				t.Fatal("response Close did not return")
			}
			select {
			case <-serverCanceled:
			case <-time.After(2 * time.Second):
				t.Fatal("closed response kept the upstream request alive")
			}
			require.NoError(t, parent.Err(), "one response must not cancel its caller")
			require.NoError(t, resp.Body.Close())
			require.Zero(t, atomic.LoadInt64(&entry.inFlight))
		})
	}
}

func TestHTTPUpstreamCompletedBodyPreservesConnectionReuse(t *testing.T) {
	for _, fingerprint := range []bool{false, true} {
		t.Run(map[bool]string{false: "Do", true: "DoWithTLS"}[fingerprint], func(t *testing.T) {
			const payload = "data: {\"type\":\"response.completed\"}\n\n: accounting-tail\n\n"
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, payload)
				w.(http.Flusher).Flush()
			}))
			defer srv.Close()
			entry, do := bodyCancelTestUpstream(t, srv.Client(), fingerprint)
			for i := 0; i < 4; i++ {
				var reused bool
				parent := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
					GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused },
				})
				req, err := http.NewRequestWithContext(parent, http.MethodGet, srv.URL, nil)
				require.NoError(t, err)
				resp, err := do(req)
				require.NoError(t, err)
				require.NoError(t, resp.Request.Context().Err(), "request must remain active after headers")
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, payload, string(body), "completed event must not truncate trailing data")
				require.NoError(t, resp.Body.Close())
				require.NoError(t, resp.Body.Close())
				require.Zero(t, atomic.LoadInt64(&entry.inFlight))
				require.NoError(t, parent.Err())
				if i > 0 {
					require.True(t, reused, "successful reads must retain HTTP/1 connection reuse")
				}
			}
		})
	}
}

func TestHTTPUpstreamFailedRequestReleasesChildContext(t *testing.T) {
	for _, fingerprint := range []bool{false, true} {
		t.Run(map[bool]string{false: "Do", true: "DoWithTLS"}[fingerprint], func(t *testing.T) {
			var observed context.Context
			failure := errors.New("synthetic transport failure")
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				observed = req.Context()
				return nil, failure
			})}
			entry, do := bodyCancelTestUpstream(t, client, fingerprint)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://upstream.example/test", nil)
			require.NoError(t, err)
			_, err = do(req)
			require.ErrorIs(t, err, failure)
			require.ErrorIs(t, observed.Err(), context.Canceled)
			require.NoError(t, req.Context().Err())
			require.Zero(t, atomic.LoadInt64(&entry.inFlight))
		})
	}
}

func TestHTTPUpstreamClosePreservesDetachedSiblingUsage(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[http2], func(t *testing.T) {
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseUsage := func() { releaseOnce.Do(func() { close(release) }) }
			const usageTail = "data: {\"type\":\"response.completed\",\"usage\":{\"output_tokens\":17}}\n\n"
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "x")
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-release:
					_, _ = io.WriteString(w, usageTail)
				}
			}))
			srv.EnableHTTP2 = http2
			srv.StartTLS()
			defer srv.Close()
			defer releaseUsage()
			entry, do := bodyCancelTestUpstream(t, srv.Client(), false)
			caller, disconnect := context.WithCancel(t.Context())
			defer disconnect()
			// Match gateway behavior: client disconnection must not discard usage.
			upstreamContext, stop := context.WithTimeout(context.WithoutCancel(caller), 5*time.Second)
			defer stop()
			responses := make([]*http.Response, 2)
			for i := range responses {
				req, err := http.NewRequestWithContext(upstreamContext, http.MethodGet, srv.URL, nil)
				require.NoError(t, err)
				responses[i], err = do(req)
				require.NoError(t, err)
				_, err = io.ReadFull(responses[i].Body, make([]byte, 1))
				require.NoError(t, err)
			}
			disconnect()
			require.NoError(t, responses[1].Request.Context().Err())
			var closes sync.WaitGroup
			for i := 0; i < 8; i++ {
				closes.Add(1)
				go func() { defer closes.Done(); _ = responses[0].Body.Close() }()
			}
			closes.Wait()
			require.Equal(t, int64(1), atomic.LoadInt64(&entry.inFlight), "concurrent Close releases exactly one request")
			require.NoError(t, upstreamContext.Err())
			require.NoError(t, responses[1].Request.Context().Err(), "sibling stream must remain active")
			releaseUsage()
			body, err := io.ReadAll(responses[1].Body)
			require.NoError(t, err)
			require.Equal(t, usageTail, string(body))
			require.NoError(t, responses[1].Body.Close())
			require.Zero(t, atomic.LoadInt64(&entry.inFlight))
		})
	}
}
