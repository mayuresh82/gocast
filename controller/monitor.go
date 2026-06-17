package controller

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	c "github.com/mayuresh82/gocast/config"
	api "github.com/osrg/gobgp/api"
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

// appMon maintains the state of a registered app
type appMon struct {
	app       *App
	done      chan bool
	announced bool
	runLoopOn bool
}

// MonitorMgr manages the lifecycle of registered apps
type MonitorMgr struct {
	monitors map[string]*appMon
	cleanups map[string]chan bool
	config   *c.Config
	ctrl     *Controller
	consul   *ConsulMon

	monMu sync.Mutex
	clMu  sync.Mutex
}

func NewMonitor(config *c.Config) *MonitorMgr {
	ctrl, err := NewController(config.Bgp)
	if err != nil {
		glog.Exitf("Failed to start BGP controller: %v", err)
	}
	mon := &MonitorMgr{
		ctrl:     ctrl,
		monitors: make(map[string]*appMon),
		cleanups: make(map[string]chan bool),
	}
	if config.Agent.ConsulAddr != "" {
		cmon, err := NewConsulMon(config.Agent.ConsulAddr, config.Agent.ConsulToken)
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
		app, err := NewApp(a.Name, a.Vip, a.VipConfig, a.Monitors, a.Nats, "config")
		if err != nil {
			glog.Errorf("Failed to add configured app %s: %v", a.Name, err)
			continue
		}
		mon.Add(app)
	}
	return mon
}

// consulMon periodically queries consul for apps that need to be
// registered and adds them to the monitor manager
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
			m.monMu.Lock()
			for name, mon := range m.monitors {
				if mon.app.Source != "consul" {
					continue
				}
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
			m.monMu.Unlock()
			for _, tr := range toRemove {
				m.Remove(tr)
			}
		}
		<-time.After(m.config.Agent.ConsulQueryInterval)
	}
}

// Add adds a new app into monitor manager
func (m *MonitorMgr) Add(app *App) {
	// check if already running
	m.monMu.Lock()
	var existing *appMon
	for _, appMon := range m.monitors {
		if appMon.app.Equal(app) {
			glog.Infof("App %s already exists", app.Name)
			existing = appMon
			break
		}
		if appMon.app.Vip.Net.String() == app.Vip.Net.String() && appMon.app.Name != app.Name {
			glog.Errorf("Error: Vip %s is already being announced by app: %s", app.Vip.Net.String(), appMon.app.Name)
			m.monMu.Unlock()
			return
		}
	}
	m.monMu.Unlock()
	// if the same app already exists but its run loop is not running,
	// then just restart the run loop
	if existing != nil {
		if !existing.runLoopOn {
			go m.runLoop(existing)
		}
	} else {
		// else add a new app and start its run loop
		appMon := &appMon{app: app, done: make(chan bool)}
		m.monitors[app.Name] = appMon
		go m.runLoop(appMon)
		glog.Infof("Registered a new app: %v", app.String())
	}
}

// Remove removes an app from monitor manager, stops BGP
/// announcement and cleans up state
func (m *MonitorMgr) Remove(appName string) {
	m.monMu.Lock()
	defer m.monMu.Unlock()
	if a, ok := m.monitors[appName]; ok {
		if a.runLoopOn {
			close(a.done)
		}
		if a.announced {
			if err := m.ctrl.Withdraw(a.app.Vip); err != nil {
				glog.Errorf("Failed to withdraw route: %v", err)
			}
		}
		if err := deleteLoopback(a.app.Vip.Net); err != nil {
			glog.Errorf("Failed to remove app: %s: %v", a.app.Name, err)
		}
		for _, nat := range a.app.Nats {
			parts := strings.Split(nat, ":")
			switch len(parts) {
			case 3:
				if err := natRule("D", a.app.Vip.Net.IP, m.ctrl.localIP, parts[0], parts[1], parts[2]); err != nil {
					glog.Errorf("Failed to remove app: %s: %v", a.app.Name, err)
				}
			case 2:
				if err := natRule("D", a.app.Vip.Net.IP, m.ctrl.localIP, parts[0], parts[1], parts[1]); err != nil {
					glog.Errorf("Failed to remove app: %s: %v", a.app.Name, err)
				}
			default:
				continue
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
	m.clMu.Lock()
	defer m.clMu.Unlock()
	if m.runMonitors(app) {
		glog.V(2).Infof("All Monitors for app: %s succeeded", app.Name)
		if !am.announced {
			if err := addLoopback(app.Name, app.Vip.Net); err != nil {
				return err
			}
			for _, nat := range app.Nats {
				parts := strings.Split(nat, ":")
				switch len(parts) {
				case 3:
					if err := natRule("A", app.Vip.Net.IP, m.ctrl.localIP, parts[0], parts[1], parts[2]); err != nil {
						return err
					}
				case 2:
					if err := natRule("A", app.Vip.Net.IP, m.ctrl.localIP, parts[0], parts[1], parts[1]); err != nil {
						return err
					}
				default:
					continue
				}
			}
			if err := m.ctrl.Announce(app.Vip); err != nil {
				return fmt.Errorf("Failed to announce route: %v", err)
			}
			am.announced = true
			if exit, ok := m.cleanups[app.Name]; ok {
				close(exit)
				delete(m.cleanups, app.Name)
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

// runLoop periodically checks if an app passes healthchecks
// and needs VIP announcement
func (m *MonitorMgr) runLoop(am *appMon) {
	glog.Infof("Starting run-loop for app %s", am.app.Name)
	am.runLoopOn = true
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
			glog.Infof("Exit run-loop for app: %s", am.app.Name)
			am.runLoopOn = false
			return
		}
	}
}

// CloseAll shuts down all BGP sessions removes state
func (m *MonitorMgr) CloseAll() {
	glog.Infof("Shutting down all open bgp sessions")
	if err := m.ctrl.Shutdown(); err != nil {
		glog.Errorf("Failed to shut-down BGP: %v", err)
	}
	for _, am := range m.monitors {
		if am.runLoopOn {
			close(am.done)
		}
		deleteLoopback(am.app.Vip.Net)
		for _, nat := range am.app.Nats {
			parts := strings.Split(nat, ":")
			switch len(parts) {
			case 3:
				natRule("D", am.app.Vip.Net.IP, m.ctrl.localIP, parts[0], parts[1], parts[2])
			case 2:
				natRule("D", am.app.Vip.Net.IP, m.ctrl.localIP, parts[0], parts[1], parts[1])
			default:
				continue
			}
		}
	}
}

// Reload re-reads the configuration file and applies changes
func (m *MonitorMgr) Reload(configPath string) error {
	glog.Infof("Reloading configuration from %s", configPath)

	// Read new configuration
	newConfig := c.GetConfig(configPath)
	if newConfig == nil {
		return fmt.Errorf("Failed to load configuration")
	}

	// Set defaults if not specified
	if newConfig.Agent.MonitorInterval == 0 {
		newConfig.Agent.MonitorInterval = defaultMonitorInterval
	}
	if newConfig.Agent.CleanupTimer == 0 {
		newConfig.Agent.CleanupTimer = defaultCleanupTimer
	}

	// Check if BGP configuration has changed
	bgpChanged := m.bgpConfigChanged(m.config.Bgp, newConfig.Bgp)

	if bgpChanged {
		glog.Infof("BGP configuration changed, restarting BGP controller")

		// Withdraw all current routes before shutting down
		m.monMu.Lock()
		for _, am := range m.monitors {
			if am.announced {
				if err := m.ctrl.Withdraw(am.app.Vip); err != nil {
					glog.Errorf("Failed to withdraw route during reload: %v", err)
				}
				am.announced = false
			}
		}
		m.monMu.Unlock()

		// Shutdown old BGP controller
		if err := m.ctrl.Shutdown(); err != nil {
			glog.Errorf("Failed to shutdown BGP controller: %v", err)
		}

		// Start new BGP controller
		ctrl, err := NewController(newConfig.Bgp)
		if err != nil {
			return fmt.Errorf("Failed to start new BGP controller: %v", err)
		}
		m.ctrl = ctrl

		// Re-announce all routes with new BGP config
		m.monMu.Lock()
		for _, am := range m.monitors {
			// Only re-announce if the app was previously announced and is still healthy
			if m.runMonitors(am.app) {
				if err := m.ctrl.Announce(am.app.Vip); err != nil {
					glog.Errorf("Failed to re-announce route %s: %v", am.app.Vip.Net.String(), err)
				} else {
					am.announced = true
					glog.Infof("Re-announced route %s", am.app.Vip.Net.String())
				}
			}
		}
		m.monMu.Unlock()
	}

	// Handle consul configuration changes
	if m.config.Agent.ConsulAddr != newConfig.Agent.ConsulAddr ||
	   m.config.Agent.ConsulToken != newConfig.Agent.ConsulToken {
		glog.Infof("Consul configuration changed")
		if newConfig.Agent.ConsulAddr != "" {
			cmon, err := NewConsulMon(newConfig.Agent.ConsulAddr, newConfig.Agent.ConsulToken)
			if err != nil {
				glog.Errorf("Failed to start consul monitor: %v", err)
			} else {
				m.consul = cmon
				// Start consul monitoring in background if not already running
				if m.config.Agent.ConsulAddr == "" {
					go m.consulMon()
				}
			}
		}
	}

	// Update agent configuration
	oldConfig := m.config
	m.config = newConfig

	// Handle app configuration changes
	m.reloadApps(oldConfig.Apps, newConfig.Apps)

	glog.Infof("Configuration reloaded successfully")
	return nil
}

// bgpConfigChanged checks if BGP configuration has changed
func (m *MonitorMgr) bgpConfigChanged(old, new c.BgpConfig) bool {
	// Check basic parameters
	if old.LocalAS != new.LocalAS || old.LocalIP != new.LocalIP || old.Origin != new.Origin {
		return true
	}

	// Check legacy peer config
	if old.PeerAS != new.PeerAS || old.PeerIP != new.PeerIP {
		return true
	}

	// Check peers
	if len(old.Peers) != len(new.Peers) {
		return true
	}

	// Compare each peer
	for i := range old.Peers {
		if old.Peers[i].PeerIP != new.Peers[i].PeerIP ||
			old.Peers[i].PeerAS != new.Peers[i].PeerAS ||
			old.Peers[i].MD5Password != new.Peers[i].MD5Password ||
			old.Peers[i].MD5EnvVar != new.Peers[i].MD5EnvVar {
			return true
		}

		// Check multi-hop
		if (old.Peers[i].MultiHop == nil) != (new.Peers[i].MultiHop == nil) {
			return true
		}
		if old.Peers[i].MultiHop != nil && new.Peers[i].MultiHop != nil {
			if *old.Peers[i].MultiHop != *new.Peers[i].MultiHop {
				return true
			}
		}

		// Check communities
		if len(old.Peers[i].Communities) != len(new.Peers[i].Communities) {
			return true
		}
		for j := range old.Peers[i].Communities {
			if old.Peers[i].Communities[j] != new.Peers[i].Communities[j] {
				return true
			}
		}
	}

	// Check global communities
	if len(old.Communities) != len(new.Communities) {
		return true
	}
	for i := range old.Communities {
		if old.Communities[i] != new.Communities[i] {
			return true
		}
	}

	return false
}

// reloadApps compares old and new app configurations and applies changes
func (m *MonitorMgr) reloadApps(oldApps, newApps []c.AppConfig) {
	// Build maps for easy comparison
	oldAppMap := make(map[string]c.AppConfig)
	for _, app := range oldApps {
		oldAppMap[app.Name] = app
	}

	newAppMap := make(map[string]c.AppConfig)
	for _, app := range newApps {
		newAppMap[app.Name] = app
	}

	// Remove apps that are no longer in config
	for name := range oldAppMap {
		if _, exists := newAppMap[name]; !exists {
			m.monMu.Lock()
			if am, ok := m.monitors[name]; ok {
				if am.app.Source == "config" {
					glog.Infof("Removing app %s (no longer in config)", name)
					m.monMu.Unlock()
					m.Remove(name)
					continue
				}
			}
			m.monMu.Unlock()
		}
	}

	// Add new apps or update existing ones
	for name, newAppConfig := range newAppMap {
		oldAppConfig, existed := oldAppMap[name]

		// Check if app configuration changed
		configChanged := !existed ||
			oldAppConfig.Vip != newAppConfig.Vip ||
			!equalStringSlices(oldAppConfig.Monitors, newAppConfig.Monitors) ||
			!equalStringSlices(oldAppConfig.Nats, newAppConfig.Nats) ||
			!equalStringSlices(oldAppConfig.VipConfig.BgpCommunities, newAppConfig.VipConfig.BgpCommunities)

		if configChanged {
			if existed {
				glog.Infof("App %s configuration changed, reloading", name)
				m.Remove(name)
			} else {
				glog.Infof("Adding new app %s from config", name)
			}

			app, err := NewApp(newAppConfig.Name, newAppConfig.Vip, newAppConfig.VipConfig,
				newAppConfig.Monitors, newAppConfig.Nats, "config")
			if err != nil {
				glog.Errorf("Failed to add app %s: %v", name, err)
				continue
			}
			m.Add(app)
		}
	}
}

// equalStringSlices compares two string slices
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CleanUp periodically monitors for stale apps and cleans them up
func (m *MonitorMgr) Cleanup(app string, exit chan bool) {
	t := time.NewTimer(m.config.Agent.CleanupTimer)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			glog.Infof("Cleaning up app %s", app)
			m.Remove(app)
			return
		case <-exit:
			return
		}
	}
}

// GetInfo returns basic BGP info for established peers
func (m *MonitorMgr) GetInfo() ([]*api.Peer, error) {
	return m.ctrl.PeerInfo()
}
