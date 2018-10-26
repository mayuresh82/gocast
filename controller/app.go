package controller

import (
	"fmt"
	"github.com/golang/glog"
	"net"
	"strings"
)

type MonitorType int

const (
	Monitor_PORT   MonitorType = 1
	Monitor_EXEC   MonitorType = 2
	Monitor_CONSUL MonitorType = 3
)

var MonitorMap = map[string]MonitorType{"port": Monitor_PORT, "exec": Monitor_EXEC, "consul": Monitor_CONSUL}

func (m MonitorType) String() string {
	for str, mtr := range MonitorMap {
		if m == mtr {
			return str
		}
	}
	return "unknown"
}

type Monitor struct {
	Type     MonitorType
	Port     string
	Protocol string
	Cmd      string
}

func (m *Monitor) Equal(other *Monitor) bool {
	return m.Type == other.Type && m.Port == other.Port && m.Protocol == other.Protocol && m.Cmd == other.Cmd
}

type Monitors []*Monitor

func (m Monitors) Contains(elem *Monitor) bool {
	for _, mon := range m {
		if mon.Equal(elem) {
			return true
		}
	}
	return false
}

type App struct {
	Name     string
	Vip      *net.IPNet
	Monitors Monitors
}

func (a *App) Equal(other *App) bool {
	if len(a.Monitors) != len(other.Monitors) {
		return false
	}
	for _, m := range other.Monitors {
		if !a.Monitors.Contains(m) {
			return false
		}
	}
	return a.Name == other.Name && a.Vip.String() == other.Vip.String()
}

func (a *App) needsNatRule() (bool, *Monitor) {
	for _, m := range a.Monitors {
		if m.Type == Monitor_CONSUL && m.Port != "" {
			return true, m
		}
	}
	return false, nil
}

func NewApp(appName, vip string, monitors []string) (*App, error) {
	if appName == "" {
		return nil, fmt.Errorf("Invalid app name")
	}
	app := &App{Name: appName}
	_, ipnet, err := net.ParseCIDR(vip)
	if err != nil {
		return nil, fmt.Errorf("Invalid VIP specified, need ip/mask")
	}
	app.Vip = ipnet
	for _, m := range monitors {
		parts := strings.Split(m, ":")
		if len(parts) != 2 && len(parts) != 3 {
			glog.Errorf("Invalid monitor specified, ignoring")
			continue
		}
		mon := &Monitor{Type: MonitorMap[parts[0]]}
		switch mon.Type.String() {
		case "port":
			mon.Protocol = parts[1]
			mon.Port = parts[2]
		case "exec":
			mon.Cmd = parts[1]
		case "consul":
			glog.V(2).Infof("Using consul health monitor")
		default:
			glog.V(2).Infof("No monitor specified")
		}
		app.Monitors = append(app.Monitors, mon)
	}
	return app, nil
}
