package main

import (
	"context"
	"flag"
	c "github.com/mayuresh82/gocast/config"
	"github.com/mayuresh82/gocast/controller"
	"github.com/mayuresh82/gocast/server"
	"os"
	"os/signal"
	"syscall"
)

var (
	config = flag.String("config", "", "Path to config file")
)

func main() {
	flag.Parse()
	conf := c.GetConfig(*config)
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
