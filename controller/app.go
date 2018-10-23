package controller

import (
	"fmt"
	"github.com/golang/glog"
	"net"
	"strings"
)

type MonitorType int

const (
	Monitor_PORT MonitorType = 1
	Monitor_EXEC MonitorType = 2
)

var Monitors = map[string]MonitorType{"port": Monitor_PORT, "exec": Monitor_EXEC}

func (m MonitorType) String() string {
	for str, mtr := range Monitors {
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

type App struct {
	Name    string
	Vip     *net.IPNet
	Monitor Monitor
}

func NewApp(appName, vip, monitor, monitorType string) (*App, error) {
	if appName == "" {
		return nil, fmt.Errorf("Invalid app name")
	}
	app := &App{Name: appName}
	_, ipnet, err := net.ParseCIDR(vip)
	if err != nil {
		return nil, fmt.Errorf("Invalid VIP specified, need ip/mask")
	}
	app.Vip = ipnet
	m := Monitor{Type: Monitors[monitorType]}
	switch monitorType {
	case "port":
		parts := strings.Split(monitor, ":")
		m.Protocol = parts[0]
		m.Port = parts[1]
	case "exec":
		m.Cmd = monitor
	default:
		glog.V(2).Infof("No monitor specified")
	}
	app.Monitor = m
	return app, nil
}
