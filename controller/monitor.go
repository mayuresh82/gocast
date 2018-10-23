package controller

import (
	"fmt"
	"github.com/golang/glog"
	api "github.com/osrg/gobgp/api"
	"net"
	"os/exec"
	"sync"
	"time"
)

func portMonitor(protocol, port string) bool {
	switch protocol {
	case "tcp":
		conn, err := net.Listen(protocol, ":"+port)
		if err != nil {
			glog.V(4).Infof("Monitor tcp port up")
			return true
		}
		defer conn.Close()
	case "udp":
		conn, err := net.ListenPacket(protocol, ":"+port)
		if err != nil {
			glog.V(4).Infof("Monitor udp port up")
			return true
		}
		defer conn.Close()
	}
	return false
}

func execMonitor(cmd string) bool {
	out := exec.Command("bash", "-c", cmd)
	if err := out.Start(); err != nil {
		glog.V(2).Infof("Cannot exec cmd: %s : %v", cmd, err)
		return false
	}
	if err := out.Wait(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			glog.V(4).Infof("Monitor cmd failed")
			return false
		}
	}
	return true
}

type appMon struct {
	app       *App
	done      chan bool
	announced bool
	checkOn   bool
}

type MonitorMgr struct {
	monitors        map[string]*appMon
	cleanups        map[string]chan bool
	c               *Controller
	monitorInterval time.Duration
	cleanupTimer    time.Duration
	sync.Mutex
}

func NewMonitor(localAS, peerAS int, monitorInterval time.Duration, peerIP string, cleanup time.Duration) *MonitorMgr {
	c, err := NewController(localAS, peerAS, peerIP)
	if err != nil {
		glog.Exitf("Failed to start BGP controller: %v", err)
	}
	return &MonitorMgr{
		c:               c,
		monitors:        make(map[string]*appMon),
		cleanups:        make(map[string]chan bool),
		monitorInterval: monitorInterval,
		cleanupTimer:    cleanup,
	}
}

func (m *MonitorMgr) Add(app *App) {
	// stop and start a new one if one already running
	m.Remove(app.Name)
	appMon := &appMon{app: app, done: make(chan bool)}
	m.Lock()
	m.monitors[app.Name] = appMon
	m.Unlock()
	go m.runLoop(appMon)
	glog.Infof("Registered a new app: %v", app)
}

func (m *MonitorMgr) Remove(appName string) {
	m.Lock()
	defer m.Unlock()
	if a, ok := m.monitors[appName]; ok {
		if a.checkOn {
			a.done <- true
		}
		if a.announced {
			if err := m.c.Withdraw(a.app.Vip); err != nil {
				glog.Errorf("Failed to withdraw route: %v", err)
			}
		}
		deleteLoopback(appName)
	}
	delete(m.monitors, appName)
}

func (m *MonitorMgr) checkCond(am *appMon) error {
	var cond bool
	app := am.app
	switch app.Monitor.Type {
	case Monitor_PORT:
		cond = portMonitor(app.Monitor.Protocol, app.Monitor.Port)
	case Monitor_EXEC:
		cond = execMonitor(app.Monitor.Cmd)
	}
	m.Lock()
	defer m.Unlock()
	if cond {
		glog.V(2).Infof("%s Monitor for app: %s succeeded", app.Monitor.Type.String(), app.Name)
		if !am.announced {
			if err := addLoopback(app.Name, app.Vip); err != nil {
				return err
			}
			if err := m.c.Announce(app.Vip); err != nil {
				return fmt.Errorf("Failed to announce route: %v", err)
			}
			am.announced = true
			if exit, ok := m.cleanups[app.Name]; ok {
				exit <- true
			}
		}
	} else {
		glog.V(2).Infof("%s Monitor for app: %s Failed", app.Monitor.Type.String(), app.Name)
		if am.announced {
			if err := m.c.Withdraw(app.Vip); err != nil {
				return fmt.Errorf("Failed to withdraw route: %v", err)
			}
			am.announced = false
			exit := make(chan bool)
			go m.Cleanup(app.Name, exit)
			m.cleanups[app.Name] = exit
		}
	}
	return nil
}

func (m *MonitorMgr) runLoop(am *appMon) {
	am.checkOn = true
	if err := m.checkCond(am); err != nil {
		glog.Errorln(err)
	}
	t := time.NewTicker(m.monitorInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := m.checkCond(am); err != nil {
				glog.Errorln(err)
			}
		case <-am.done:
			glog.V(2).Infof("Exit run-loop for app: %s", am.app.Name)
			return
		}
	}
}

func (m *MonitorMgr) CloseAll() {
	glog.Infof("Shutting down all open bgp sessions")
	if err := m.c.Shutdown(); err != nil {
		glog.Errorf("Failed to shut-down BGP: %v", err)
	}
	for name, am := range m.monitors {
		if am.checkOn {
			am.done <- true
		}
		deleteLoopback(name)
	}
}

func (m *MonitorMgr) Cleanup(app string, exit chan bool) {
	t := time.NewTimer(m.cleanupTimer)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			glog.Infof("Cleaning up app %s", app)
			m.Remove(app)
		case <-exit:
			return
		}
	}
}

func (m *MonitorMgr) GetInfo() (*api.Peer, error) {
	return m.c.PeerInfo()
}
