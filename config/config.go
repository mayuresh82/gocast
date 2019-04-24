package config

import (
	"io/ioutil"
	"path/filepath"
	"time"

	"github.com/golang/glog"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Agent struct {
		ListenAddr          string        `yaml:"listen_addr"`
		MonitorInterval     time.Duration `yaml:"monitor_interval"`
		CleanupTimer        time.Duration `yaml:"cleanup_timer"`
		ConsulAddr          string        `yaml:"consul_addr"`
		ConsulQueryInterval time.Duration `yaml:"consul_query_interval"`
	}
	Bgp []struct {
		LocalAS     int    `yaml:"local_as"`
		PeerAS      int    `yaml:"peer_as"`
		PeerIP      string `yaml:"peer_ip"`
		Communities []string
		Origin      string
		AddrFamily  string `yaml:"addr_family"`
	}
	Apps []struct {
		Name     string
		Vip      string
		Monitors []string
		Nats     []string
	}
}

func GetConfig(file string) *Config {
	absPath, _ := filepath.Abs(file)
	data, err := ioutil.ReadFile(absPath)
	if err != nil {
		glog.Exitf("FATAL: Unable to read config file: %v", err)
	}
	config := &Config{}
	if err := yaml.Unmarshal(data, config); err != nil {
		glog.Exitf("FATAL: Unable to decode yaml: %v", err)
	}
	return config
}
