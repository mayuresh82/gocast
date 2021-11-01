package controller

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/mayuresh82/gocast/config"
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

type VipConsulChecks struct {
	Protocol string // only 'tcp` check is currently supported
	Port     string
	Interval string
	Timeout  string
}

type App struct {
	Name             string
	Vip              *Route
	VipConfig        config.VipConfig
	Monitors         Monitors
	Nats             []string
	Source           string
	VipConsulSvcName string
	VipConsulChecks  []VipConsulChecks
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
	return a.Name == other.Name && a.Vip.Net.String() == other.Vip.Net.String()
}

func (a *App) String() string {
	return fmt.Sprintf("Name: %s, Vip: %s, VipConf: %v, Monitors: %v, Nats: %v, Source: %s",
		a.Name, a.Vip.Net.String(), a.VipConfig, a.Monitors, a.Nats, a.Source)
}

func parseVipChecks(vipchecks []string) ([]VipConsulChecks, error) {
	var consulCheckList []VipConsulChecks
	for _, m := range vipchecks {
		// valid vip consul check format:
		// "port:tcp:123,interval:10s,timeout:2s"
		checks := strings.Split(m, ",")
		var newCheck VipConsulChecks
		// Use following as the default interval & timeout if not specified
		newCheck.Interval = "15s"
		newCheck.Timeout = "2s"
		for _, check := range checks {
			parts := strings.Split(check, ":")
			switch parts[0] {
			case "port":
				if len(parts) != 3 || parts[1] != "tcp" { // TBD: HTTP health check currently not supported
					return nil, fmt.Errorf("Invalid port vip check, must specify proto:port")
				}
				newCheck.Protocol = parts[1]
				portInt, err := strconv.Atoi(parts[2])
				if err != nil || portInt < 1 || portInt > 0xFFFF {
					return nil, fmt.Errorf("Invalid port number")
				}
				newCheck.Port = parts[2]
			case "interval":
				if len(parts) != 2 {
					return nil, fmt.Errorf("Invalid vip check interval format, must specify interval:duration")
				}
				if _, err := time.ParseDuration(parts[1]); err != nil {
					return nil, fmt.Errorf("Invalid interval value")
				}
				newCheck.Interval = parts[1]
			case "timeout":
				if len(parts) != 2 {
					return nil, fmt.Errorf("Invalid vip check timeout format, must specify timeout:duration")
				}
				if _, err := time.ParseDuration(parts[1]); err != nil {
					return nil, fmt.Errorf("Invalid timeout value")
				}
				newCheck.Timeout = parts[1]
			default:
				glog.V(2).Infof("Invalid vip check specified")
			}
		}
		consulCheckList = append(consulCheckList, newCheck)
	}
	return consulCheckList, nil
}

func NewApp(appName, vip string, vipConfig config.VipConfig, monitors []string, nats []string, source string, vipService string, vipChecks []string) (*App, error) {
	if appName == "" {
		return nil, fmt.Errorf("Invalid app name")
	}
	app := &App{Name: appName, Nats: nats, Source: source, VipConsulSvcName: vipService}
	_, ipnet, err := net.ParseCIDR(vip)
	if err != nil {
		return nil, fmt.Errorf("Invalid VIP specified, need ip/mask")
	}
	app.Vip = &Route{Net: ipnet, Communities: vipConfig.BgpCommunities}
	app.VipConfig = vipConfig
	app.VipConsulChecks, err = parseVipChecks(vipChecks)
	if err != nil {
		return nil, err
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
