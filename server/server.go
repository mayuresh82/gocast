package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
	app, err := controller.NewApp(queries["name"][0], queries["vip"][0], vipConf, queries["monitor"], queries["nat"], "http")
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
