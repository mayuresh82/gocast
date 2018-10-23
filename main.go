package main

import (
	"context"
	"flag"
	"github.com/mayuresh82/gocast/controller"
	"github.com/mayuresh82/gocast/server"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	serverAddr      = flag.String("serverAddr", ":8080", "Addr for http service")
	localAS         = flag.Int("localAS", 65000, "Local ASN of the gocast host")
	peerAS          = flag.Int("peerAS", 65254, "AS to peer with")
	monitorInterval = flag.Duration("monitorInterval", 5*time.Second, "Interval for health check")
	peerIP          = flag.String("peerIP", "", "Override the IP to peer with. Default: gateway ip")
	cleanupTimer    = flag.Duration("cleanup", 15*time.Minute, "Time to flush out inactive apps")
)

func main() {
	flag.Parse()
	mon := controller.NewMonitor(*localAS, *peerAS, *monitorInterval, *peerIP, *cleanupTimer)
	srv := server.NewServer(*serverAddr, mon)

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
