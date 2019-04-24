package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang/glog"
	c "github.com/mayuresh82/gocast/config"
	"github.com/mayuresh82/gocast/controller"
	"github.com/mayuresh82/gocast/server"
	log "github.com/sirupsen/logrus"
)

var (
	config      = flag.String("config", "", "Path to config file")
	nextVersion = "0.0.1"
	version     string
	commit      string
	branch      string
)

func getVersion() string {
	if version == "" {
		return fmt.Sprintf("v%s~%s", nextVersion, commit)
	}
	return fmt.Sprintf("%s~%s", version, commit)
}

func main() {
	flag.Parse()
	if glog.V(4) {
		log.SetLevel(log.DebugLevel)
	}
	conf := c.GetConfig(*config)
	mon := controller.NewMonitor(conf)
	srv := server.NewServer(conf.Agent.ListenAddr, mon)

	glog.Infof("Starting GoCast %s", getVersion())
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	// catch interrupt
	shutdown := make(chan struct{})
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		for {
			sig := <-signalChan
			if sig == os.Interrupt || sig == syscall.SIGTERM {
				mon.CloseAll()
				cancel()
				shutdown <- struct{}{}
				return
			}
		}
	}()
	<-shutdown
}
