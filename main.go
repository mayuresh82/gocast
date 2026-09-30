package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/golang/glog"
	log "github.com/sirupsen/logrus"

	c "github.com/mayuresh82/gocast/config"
	"github.com/mayuresh82/gocast/controller"
	"github.com/mayuresh82/gocast/server"
)

var (
	configFilePath = flag.String("config", "", "Path to config file")
)

func main() {
	_ = flag.Set("logtostderr", "true")
	flag.Parse()
	if glog.V(4) {
		log.SetLevel(log.DebugLevel)
	}
	conf := c.GetConfig(*configFilePath)
	mon := controller.NewMonitor(conf)
	srv := server.NewServer(conf.Agent.ListenAddr, mon)

	ctx, cancel := context.WithCancel(context.Background())
	// catch interrupt
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	go func() {
		for {
			sig := <-signalChan
			if sig == os.Interrupt || sig == syscall.SIGTERM {
				mon.CloseAll()
				cancel()
				return
			}
		}
	}()
	srv.Serve(ctx)
}
