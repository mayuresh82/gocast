package config

import (
	"io/ioutil"
	"path/filepath"
	"time"

	"github.com/golang/glog"
	"gopkg.in/yaml.v2"
)

type AgentConfig struct {
	ListenAddr          string        `yaml:"listen_addr"`
	MonitorInterval     time.Duration `yaml:"monitor_interval"`
	CleanupTimer        time.Duration `yaml:"cleanup_timer"`
	ConsulAddr          string        `yaml:"consul_addr"`
	ConsulQueryInterval time.Duration `yaml:"consul_query_interval"`
	ConsulToken			string		  `yaml:"consul_token"`
}

type BgpConfig struct {
	LocalAS     int    `yaml:"local_as"`
	PeerAS      int    `yaml:"peer_as"`
	LocalIP     string `yaml:"local_ip"`
	PeerIP      string `yaml:"peer_ip"`
	Communities []string
	Origin      string
}

type VipConfig struct {
	// per VIP BGP communities to announce. This is in addition to the
	// global config
	BgpCommunities []string `yaml:"bgp_communities"`
}

type AppConfig struct {
	Name      string
	Vip       string
	VipConfig VipConfig `yaml:"vip_config"`
	Monitors  []string
	Nats      []string
}

type Config struct {
	Agent AgentConfig
	Bgp   BgpConfig
	Apps  []AppConfig
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
