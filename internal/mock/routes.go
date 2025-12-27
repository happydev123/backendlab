package mock

import "net/http"

// route is one endpoint registered under each variant prefix.
type route struct {
	method  string
	suffix  string
	handler func(*Server, Variant) http.HandlerFunc
}

// variantRoutes lists the endpoints served under /baseline and /candidate.
// Each feature group keeps its handlers in its own file; this table is the
// only place that knows the URL layout.
func variantRoutes() []route {
	return []route{
		{http.MethodGet, "/health", (*Server).handleHealth},
		{http.MethodGet, "/invoices", (*Server).handleInvoiceList},
		{http.MethodGet, "/invoices/{id}", (*Server).handleInvoice},
		{http.MethodGet, "/slow", (*Server).handleSlow},
		{http.MethodGet, "/stream/chat", (*Server).handleChatStream},
		{http.MethodGet, "/stream/gap", (*Server).handleGapStream},
		{http.MethodGet, "/stream/truncated", (*Server).handleTruncatedStream},
		{http.MethodPost, "/counter/increment", (*Server).handleIncrement},
		{http.MethodGet, "/counter", (*Server).handleCount},
	}
}

// Handler returns the HTTP handler for both variants and the control
// endpoints. Unknown paths get 404 and known paths with the wrong method
// get 405, both from http.ServeMux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, v := range Variants() {
		for _, rt := range variantRoutes() {
			mux.Handle(rt.method+" /"+string(v)+rt.suffix, rt.handler(s, v))
		}
	}
	mux.HandleFunc("POST /_mock/reset", s.handleReset)
	mux.HandleFunc("GET /_mock/config", s.handleConfig)
	return mux
}
