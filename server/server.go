package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/mayuresh82/gocast/config"
	"github.com/mayuresh82/gocast/controller"
)

// Server is the main entrypoint into the app and serves app requests
type Server struct {
	ListenAddr string
	mon        *controller.MonitorMgr
}

func NewServer(addr string, mon *controller.MonitorMgr) *Server {
	return &Server{
		ListenAddr: addr,
		mon:        mon,
	}
}

func (s *Server) Serve(ctx context.Context) {
	glog.Infof("Starting http server on %s", s.ListenAddr)
	http.HandleFunc("/register", s.registerHandler)
	http.HandleFunc("/unregister", s.unregisterHandler)
	http.HandleFunc("/info", s.infoHandler)
	http.HandleFunc("/ping", s.pingHandler)
	srv := &http.Server{Addr: s.ListenAddr}
	idleConnsClosed := make(chan struct{})
	go func() {
		<-ctx.Done()
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			glog.Errorf("HTTP server Shutdown Error: %v", err)
		}
		close(idleConnsClosed)
	}()
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		// Error starting or closing listener
		glog.Errorf("HTTP server ListenAndServe Error: %v", err)
	}
	<-idleConnsClosed
}

func (s *Server) registerHandler(w http.ResponseWriter, r *http.Request) {
	queries := r.URL.Query()
	var vipConf config.VipConfig
	if vipComm, ok := queries["vip_communities"]; ok {
		vipConf.BgpCommunities = strings.Split(vipComm[0], ",")
	}
	app, err := controller.NewApp(queries["name"][0], queries["vip"][0], vipConf, queries["monitor"], queries["nat"], "http", "", nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}
	s.mon.Add(app)
}

func (s *Server) unregisterHandler(w http.ResponseWriter, r *http.Request) {
	queries := r.URL.Query()
	appName, ok := queries["name"]
	if !ok {
		http.Error(w, "Invalid request, need app name specified", http.StatusBadRequest)
		return
	}
	s.mon.Remove(appName[0])
}

func (s *Server) infoHandler(w http.ResponseWriter, r *http.Request) {
	peer, err := s.mon.GetInfo()
	if err != nil {
		http.Error(w, fmt.Sprintf("Internal error getting peers: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(peer)
}

func (s *Server) pingHandler(w http.ResponseWriter, r *http.Request) {
	t := time.NewTimer(2 * time.Second)
	defer t.Stop()
	select {
	case s.mon.Health <- struct{}{}:
		// Non-blocking send to the channel. Wait for response in the select loop below.
	default:
		// If write to channel itself is blocked, we might have already hit a deadlock.
		// After timer t expires the loop below returns a 500 error to prevent a
		// goroutine leak as this healthcheck might be periodically polled.
	}
	for {
		select {
		case <-s.mon.Health:
			fmt.Fprintf(w, "I-AM-ALIVE\n")
			return
		case <-t.C: // If health doesnt pass before the timer expires, return 500 error.
			http.Error(w, fmt.Sprintf("Deadlock!\n"), http.StatusInternalServerError)
			return
		}
	}
}
