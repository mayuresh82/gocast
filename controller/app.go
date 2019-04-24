package controller

import (
	"fmt"
	"net"
	"strings"

	"github.com/golang/glog"
)

type MonitorType int

const (
	Monitor_PORT         MonitorType = 1
	Monitor_EXEC         MonitorType = 2
	Monitor_CONSUL       MonitorType = 3
	defaultFailThreshold             = 3
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
	Type      MonitorType
	Port      string
	Protocol  string
	Cmd       string
	FailCount int
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

type Vip struct {
	IP     net.IP
	Net    *net.IPNet
	Family string
}

func (v *Vip) Equal(other *Vip) bool {
	return v.IP.Equal(other.IP)
}

type App struct {
	Name     string
	Vip      *Vip
	Monitors Monitors
	Nats     []string
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
	return a.Name == other.Name && a.Vip.Equal(other.Vip)
}

func NewApp(appName, vip string, monitors []string, nats []string) (*App, error) {
	if appName == "" {
		return nil, fmt.Errorf("Invalid app name")
	}
	app := &App{Name: appName, Nats: nats}
	ip, ipnet, err := net.ParseCIDR(vip)
	if err != nil {
		return nil, fmt.Errorf("Invalid VIP specified, need ip/mask")
	}
	app.Vip = &Vip{IP: ip, Net: ipnet}
	if ip.To4() != nil {
		app.Vip.Family = "4"
	} else {
		app.Vip.Family = "6"
	}
	for _, m := range monitors {
		// valid monitor formats:
		// "port:tcp:123" , "exec:/local/check.sh", "consul"
		parts := strings.Split(m, ":")
		mon := &Monitor{Type: MonitorMap[parts[0]]}
		switch mon.Type.String() {
		case "port":
			if len(parts) != 3 {
				return nil, fmt.Errorf("Invalid port monitor, must specify proto:port")
			}
			mon.Protocol = parts[1]
			mon.Port = parts[2]
		case "exec":
			if len(parts) != 2 {
				return nil, fmt.Errorf("Invalid exec monitor, must specify command")
			}
			mon.Cmd = parts[1]
		case "consul":
			glog.V(2).Infof("Will use consul healthcheck monitor")
		default:
			glog.V(2).Infof("Invalid monitor specified")
		}
		app.Monitors = append(app.Monitors, mon)
	}
	return app, nil
}
