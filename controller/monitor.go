package controller

import (
	"fmt"
	"github.com/golang/glog"
	c "github.com/mayuresh82/gocast/config"
	api "github.com/osrg/gobgp/api"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	defaultMonitorInterval = 10 * time.Second
	defaultCleanupTimer    = 15 * time.Minute
)

func portMonitor(protocol, port string) bool {
	switch protocol {
	case "tcp":
		conn, err := net.Listen(protocol, ":"+port)
		if err != nil {
			glog.V(4).Infof("Monitor tcp port up")
			return true
		}
		conn.Close()
	case "udp":
		conn, err := net.ListenPacket(protocol, ":"+port)
		if err != nil {
			glog.V(4).Infof("Monitor udp port up")
			return true
		}
		conn.Close()
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
	monitors map[string]*appMon
	cleanups map[string]chan bool
	config   *c.Config
	ctrl     *Controller
	consul   *ConsulMon

	sync.Mutex
}

func NewMonitor(config *c.Config) *MonitorMgr {
	ctrl, err := NewController(config)
	if err != nil {
		glog.Exitf("Failed to start BGP controller: %v", err)
	}
	mon := &MonitorMgr{
		ctrl:     ctrl,
		monitors: make(map[string]*appMon),
		cleanups: make(map[string]chan bool),
	}
	if config.Agent.ConsulAddr != "" {
		cmon, err := NewConsulMon(config.Agent.ConsulAddr)
		if err != nil {
			glog.Errorf("Failed to start consul monitor: %v", err)
		} else {
			mon.consul = cmon
			go mon.consulMon()
		}
	}
	if config.Agent.MonitorInterval == 0 {
		config.Agent.MonitorInterval = defaultMonitorInterval
	}
	if config.Agent.CleanupTimer == 0 {
		config.Agent.CleanupTimer = defaultCleanupTimer
	}
	mon.config = config
	// add apps defined in config
	for _, a := range config.Apps {
		app, err := NewApp(a.Name, a.Vip, a.Monitors, a.Nats)
		if err != nil {
			glog.Errorf("Failed to add configured app %s: %v", a.Name, err)
			continue
		}
		mon.Add(app)
	}
	return mon
}

func (m *MonitorMgr) consulMon() {
	for {
		apps, err := m.consul.queryServices()
		if err != nil {
			glog.Errorf("Failed to query consul: %v", err)
		} else {
			for _, app := range apps {
				m.Add(app)
			}
			// remove currently running apps that are not discovered in this pass
			var toRemove []string
			m.Lock()
			for name := range m.monitors {
				var found bool
				for _, app := range apps {
					if name == app.Name {
						found = true
						break
					}
				}
				if !found {
					glog.V(2).Infof("Removing app: %s as it was not found in consul", name)
					toRemove = append(toRemove, name)
				}
			}
			for _, tr := range toRemove {
				m.Remove(tr)
			}
			m.Unlock()
		}
		<-time.After(m.config.Agent.ConsulQueryInterval)
	}
}

func (m *MonitorMgr) Add(app *App) {
	// check if already running
	m.Lock()
	defer m.Unlock()
	for _, appMon := range m.monitors {
		if appMon.app.Equal(app) && appMon.checkOn {
			glog.V(2).Infof("App %s already exists", app.Name)
			return
		}
		if appMon.app.Vip.String() == app.Vip.String() && appMon.app.Name != app.Name {
			glog.Errorf("Error: Vip %s is already being announced by app: %s", app.Vip.String(), appMon.app.Name)
			return
		}
	}
	m.Remove(app.Name)
	appMon := &appMon{app: app, done: make(chan bool)}
	m.monitors[app.Name] = appMon
	go m.runLoop(appMon)
	glog.Infof("Registered a new app: %v", app)
}

func (m *MonitorMgr) Remove(appName string) {
	if a, ok := m.monitors[appName]; ok {
		if a.checkOn {
			a.done <- true
		}
		if a.announced {
			if err := m.ctrl.Withdraw(a.app.Vip); err != nil {
				glog.Errorf("Failed to withdraw route: %v", err)
			}
		}
		if err := deleteLoopback(a.app.Vip); err != nil {
			glog.Errorf("Failed to remove app: %s: %v", a.app.Name, err)
		}
		for _, nat := range a.app.Nats {
			parts := strings.Split(nat, ":")
			if len(parts) != 2 {
				continue
			}
			if err := natRule("D", a.app.Vip.IP, m.ctrl.localIP, parts[0], parts[1]); err != nil {
				glog.Errorf("Failed to remove app: %s: %v", a.app.Name, err)
			}
		}
	}
	delete(m.monitors, appName)
}
func (m *MonitorMgr) runMonitors(app *App) bool {
	for _, mon := range app.Monitors {
		var check bool
		switch mon.Type {
		case Monitor_PORT:
			check = portMonitor(mon.Protocol, mon.Port)
		case Monitor_EXEC:
			check = execMonitor(mon.Cmd)
		case Monitor_CONSUL:
			c, err := m.consul.healthCheck(app.Name)
			if err != nil {
				glog.Errorf("Failed to perform consul healthcheck for %s: %v", app.Name, err)
			}
			check = c
		}
		if !check {
			glog.V(2).Infof("%s Monitor for app: %s Failed", mon.Type.String(), app.Name)
			return false
		}
	}
	return true
}

func (m *MonitorMgr) checkCond(am *appMon) error {
	app := am.app
	m.Lock()
	defer m.Unlock()
	if m.runMonitors(app) {
		glog.V(2).Infof("All Monitors for app: %s succeeded", app.Name)
		if !am.announced {
			if err := addLoopback(app.Name, app.Vip); err != nil {
				return err
			}
			for _, nat := range app.Nats {
				parts := strings.Split(nat, ":")
				if len(parts) != 2 {
					continue
				}
				if err := natRule("A", app.Vip.IP, m.ctrl.localIP, parts[0], parts[1]); err != nil {
					return err
				}
			}
			if err := m.ctrl.Announce(app.Vip); err != nil {
				return fmt.Errorf("Failed to announce route: %v", err)
			}
			am.announced = true
			if exit, ok := m.cleanups[app.Name]; ok {
				exit <- true
			}
		}
	} else {
		if am.announced {
			if err := m.ctrl.Withdraw(app.Vip); err != nil {
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
	t := time.NewTicker(m.config.Agent.MonitorInterval)
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
	if err := m.ctrl.Shutdown(); err != nil {
		glog.Errorf("Failed to shut-down BGP: %v", err)
	}
	for _, am := range m.monitors {
		if am.checkOn {
			am.done <- true
		}
		deleteLoopback(am.app.Vip)
		for _, nat := range am.app.Nats {
			parts := strings.Split(nat, ":")
			if len(parts) != 2 {
				continue
			}
			natRule("D", am.app.Vip.IP, m.ctrl.localIP, parts[0], parts[1])
		}
	}
}

func (m *MonitorMgr) Cleanup(app string, exit chan bool) {
	t := time.NewTimer(m.config.Agent.CleanupTimer)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			glog.Infof("Cleaning up app %s", app)
			m.Lock()
			m.Remove(app)
			m.Unlock()
		case <-exit:
			return
		}
	}
}

func (m *MonitorMgr) GetInfo() (*api.Peer, error) {
	return m.ctrl.PeerInfo()
}
