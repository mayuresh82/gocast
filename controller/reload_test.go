package controller

import (
	"io/ioutil"
	"os"
	"testing"
	"time"

	config "github.com/mayuresh82/gocast/config"
	"github.com/stretchr/testify/assert"
)

func TestBgpConfigChanged(t *testing.T) {
	a := assert.New(t)

	mon := &MonitorMgr{}

	// Test 1: No changes
	cfg1 := config.BgpConfig{
		LocalAS: 12345,
		LocalIP: "192.168.1.100",
		Origin:  "igp",
		Peers: []config.PeerConfig{
			{PeerIP: "10.10.10.1", PeerAS: 6789},
		},
		Communities: []string{"100:100"},
	}
	cfg2 := cfg1
	a.False(mon.bgpConfigChanged(cfg1, cfg2), "Identical configs should not be considered changed")

	// Test 2: LocalAS changed
	cfg3 := cfg1
	cfg3.LocalAS = 54321
	a.True(mon.bgpConfigChanged(cfg1, cfg3), "LocalAS change should be detected")

	// Test 3: Peer IP changed
	cfg4 := cfg1
	cfg4.Peers = []config.PeerConfig{
		{PeerIP: "10.10.10.2", PeerAS: 6789},
	}
	a.True(mon.bgpConfigChanged(cfg1, cfg4), "Peer IP change should be detected")

	// Test 4: MD5 password changed
	cfg5 := cfg1
	cfg5.Peers = []config.PeerConfig{
		{PeerIP: "10.10.10.1", PeerAS: 6789, MD5Password: "secret"},
	}
	a.True(mon.bgpConfigChanged(cfg1, cfg5), "MD5 password change should be detected")

	// Test 5: Community added
	cfg6 := cfg1
	cfg6.Communities = []string{"100:100", "200:200"}
	a.True(mon.bgpConfigChanged(cfg1, cfg6), "Community addition should be detected")

	// Test 6: Peer added
	cfg7 := cfg1
	cfg7.Peers = []config.PeerConfig{
		{PeerIP: "10.10.10.1", PeerAS: 6789},
		{PeerIP: "10.10.10.2", PeerAS: 6789},
	}
	a.True(mon.bgpConfigChanged(cfg1, cfg7), "Peer addition should be detected")

	// Test 7: MultiHop changed
	multiHopTrue := true
	cfg8 := cfg1
	cfg8.Peers = []config.PeerConfig{
		{PeerIP: "10.10.10.1", PeerAS: 6789, MultiHop: &multiHopTrue},
	}
	a.True(mon.bgpConfigChanged(cfg1, cfg8), "MultiHop change should be detected")
}

func TestEqualStringSlices(t *testing.T) {
	a := assert.New(t)

	a.True(equalStringSlices([]string{}, []string{}), "Empty slices should be equal")
	a.True(equalStringSlices([]string{"a", "b"}, []string{"a", "b"}), "Identical slices should be equal")
	a.False(equalStringSlices([]string{"a"}, []string{"a", "b"}), "Different length slices should not be equal")
	a.False(equalStringSlices([]string{"a", "b"}, []string{"a", "c"}), "Different content should not be equal")
}

func TestReload(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping reload test in CI environment")
	}

	a := assert.New(t)

	// Create initial config file
	initialConfig := `
agent:
  listen_addr: :8080
  monitor_interval: 10s
  cleanup_timer: 15m

bgp:
  local_as: 12345
  peer_as: 6789
  local_ip: 192.168.1.100
  origin: igp
  communities:
    - 100:100

apps:
  - name: test-app
    vip: 1.1.1.1/32
    monitors:
      - exec:echo
`

	// Create temporary config file
	tmpfile, err := ioutil.TempFile("", "gocast-test-*.yaml")
	a.NoError(err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write([]byte(initialConfig))
	a.NoError(err)
	tmpfile.Close()

	// Initialize monitor with initial config
	conf := config.GetConfig(tmpfile.Name())
	mon := NewMonitor(conf)
	defer mon.CloseAll()

	// Wait a bit for initialization
	time.Sleep(100 * time.Millisecond)

	// Verify initial state
	a.Equal(12345, mon.ctrl.localAS)
	m := mon.monitors["test-app"]
	a.NotNil(m, "Initial app should be loaded")

	// Update config file with new BGP AS and remove app
	updatedConfig := `
agent:
  listen_addr: :8080
  monitor_interval: 10s
  cleanup_timer: 15m

bgp:
  local_as: 54321
  peer_as: 6789
  local_ip: 192.168.1.100
  origin: igp
  communities:
    - 200:200
`

	err = ioutil.WriteFile(tmpfile.Name(), []byte(updatedConfig), 0644)
	a.NoError(err)

	// Reload configuration
	err = mon.Reload(tmpfile.Name())
	a.NoError(err)

	// Wait for reload to complete
	time.Sleep(200 * time.Millisecond)

	// Verify new state
	a.Equal(54321, mon.ctrl.localAS)
	a.Equal([]string{"200:200"}, mon.ctrl.communities)

	// Verify app was removed
	mon.monMu.Lock()
	_, exists := mon.monitors["test-app"]
	mon.monMu.Unlock()
	a.False(exists, "App should be removed after reload")
}

func TestReloadAddApp(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping reload test in CI environment")
	}

	a := assert.New(t)

	// Create initial config without apps
	initialConfig := `
agent:
  listen_addr: :8080
  monitor_interval: 10s

bgp:
  local_as: 12345
  peer_as: 6789
  local_ip: 192.168.1.100
  origin: igp
`

	tmpfile, err := ioutil.TempFile("", "gocast-test-*.yaml")
	a.NoError(err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write([]byte(initialConfig))
	a.NoError(err)
	tmpfile.Close()

	conf := config.GetConfig(tmpfile.Name())
	mon := NewMonitor(conf)
	defer mon.CloseAll()

	time.Sleep(100 * time.Millisecond)

	// Verify no apps initially
	mon.monMu.Lock()
	initialCount := len(mon.monitors)
	mon.monMu.Unlock()
	a.Equal(0, initialCount)

	// Add app to config
	updatedConfig := `
agent:
  listen_addr: :8080
  monitor_interval: 10s

bgp:
  local_as: 12345
  peer_as: 6789
  local_ip: 192.168.1.100
  origin: igp

apps:
  - name: new-app
    vip: 2.2.2.2/32
    monitors:
      - exec:echo
`

	err = ioutil.WriteFile(tmpfile.Name(), []byte(updatedConfig), 0644)
	a.NoError(err)

	// Reload
	err = mon.Reload(tmpfile.Name())
	a.NoError(err)

	time.Sleep(200 * time.Millisecond)

	// Verify app was added
	mon.monMu.Lock()
	_, exists := mon.monitors["new-app"]
	mon.monMu.Unlock()
	a.True(exists, "New app should be added after reload")
}

func TestReloadMD5Change(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("Skipping reload test in CI environment")
	}

	a := assert.New(t)

	// Set environment variable for MD5 password
	os.Setenv("BGP_TEST_PASSWORD", "initial_secret")
	defer os.Unsetenv("BGP_TEST_PASSWORD")

	initialConfig := `
agent:
  listen_addr: :8080

bgp:
  local_as: 12345
  local_ip: 192.168.1.100
  peers:
    - peer_ip: 10.10.10.1
      peer_as: 6789
      md5_env_var: BGP_TEST_PASSWORD
  origin: igp
`

	tmpfile, err := ioutil.TempFile("", "gocast-test-*.yaml")
	a.NoError(err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write([]byte(initialConfig))
	a.NoError(err)
	tmpfile.Close()

	conf := config.GetConfig(tmpfile.Name())
	mon := NewMonitor(conf)
	defer mon.CloseAll()

	time.Sleep(100 * time.Millisecond)

	// Update environment variable
	os.Setenv("BGP_TEST_PASSWORD", "updated_secret")

	// Reload (MD5 env var change should trigger BGP reload)
	err = mon.Reload(tmpfile.Name())
	a.NoError(err)

	// Note: We can't easily verify the MD5 password changed without
	// actually establishing BGP sessions, but we can verify reload succeeded
	a.NotNil(mon.ctrl)
}
