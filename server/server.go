package main

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func (s *Server) loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		GetLogger().Info("ENDPOINT CALLED: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	}
}

func (s *Server) logAllRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		GetLogger().Info("REQUEST ATTEMPT: %s %s from %s - User-Agent: %s", r.Method, r.URL.Path, r.RemoteAddr, r.UserAgent())
		next.ServeHTTP(w, r)
	})
}

func (s *Server) timeoutMiddleware(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/models", s.loggingMiddleware(s.ModelsHandler))
	mux.HandleFunc("/v1/chat/completions", s.loggingMiddleware(s.ForwardRequest))

	// Local Router API endpoints
	mux.HandleFunc("/local-router/api/config/reload", s.loggingMiddleware(s.ConfigReloadHandler))
	mux.HandleFunc("/local-router/api/openapi.json", s.loggingMiddleware(s.OpenAPIHandler))

	handler := s.logAllRequests(mux)
	handler = s.timeoutMiddleware(30 * time.Second)(handler)

	return handler
}

func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	logger := GetLogger()
	logger.Info("Starting server on port %d", s.config.Port)
	logger.Info("Server ready to accept connections")

	handler := s.SetupRoutes()
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return server.ListenAndServe()
}
