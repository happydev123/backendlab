package replay

import (
	"net/http"
	"time"
)

// sent is a request that produced response headers. The caller owns
// resp.Body and must consume it with readAll or close it with closeBody.
type sent struct {
	resp    *http.Response
	timer   *timer
	headers time.Duration
}

// send performs req exactly once through client. There is no retry at
// any level: a failure is returned to the caller as is.
func send(client *http.Client, req *http.Request, now func() time.Time) (*sent, error) {
	tm := newTimer(now)
	req = req.WithContext(tm.begin(req.Context()))
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	return &sent{resp: resp, timer: tm, headers: tm.headersAfter(now())}, nil
}

// received is a fully read response.
type received struct {
	status  int
	header  http.Header
	body    []byte
	headers time.Duration
	total   time.Duration
	bytes   int64
}

// readAll reads the body within limit and closes it. On error the
// returned value still carries the status, headers and timings observed so
// far.
func (s *sent) readAll(limit int64) (*received, error) {
	defer closeBody(s.resp.Body)
	body, err := readBody(s.resp.Body, limit)
	r := &received{
		status:  s.resp.StatusCode,
		header:  s.resp.Header,
		body:    body,
		headers: s.headers,
		total:   s.timer.since(),
		bytes:   int64(len(body)),
	}
	return r, err
}
