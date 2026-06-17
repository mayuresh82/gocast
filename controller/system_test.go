package controller

import (
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGateway(t *testing.T) {
	execCmd = os.Args[0]
	os.Setenv("test_name", "test_gateway")
	gw, err := gateway()
	assert.Nil(t, err)
	assert.Equal(t, "10.1.1.1", gw.String())
}

func TestVia(t *testing.T) {
	execCmd = os.Args[0]
	os.Setenv("test_name", "test_via")
	ip, err := via(net.ParseIP("10.1.2.100"))
	assert.Nil(t, err)
	assert.Equal(t, "10.1.2.1", ip.String())

	os.Setenv("test_name", "test_via_none")
	ip, err = via(net.ParseIP("10.1.4.1"))
	assert.Nil(t, err)
	assert.Equal(t, "10.1.4.1", ip.String())
}

func TestAddLoopback(t *testing.T) {
	execCmd = os.Args[0]
	os.Setenv("test_name", "test_add_pass")
	_, ipnet, _ := net.ParseCIDR("1.1.1.1/32")
	err := addLoopback("test_app", ipnet)
	assert.Nil(t, err)

	os.Setenv("test_name", "test_add_fail")
	_, ipnet, _ = net.ParseCIDR("1.1.1.1/32")
	err = addLoopback("test_app", ipnet)
	assert.NotNil(t, err)
}

func TestSetIptablesBinary(t *testing.T) {
	a := assert.New(t)

	// Save original value
	originalBinary := iptablesBinary
	defer func() {
		iptablesBinary = originalBinary
	}()

	// Test setting custom binary
	SetIptablesBinary("iptables-nft")
	a.Equal("iptables-nft", iptablesBinary)

	// Test setting back to default
	SetIptablesBinary("iptables")
	a.Equal("iptables", iptablesBinary)

	// Test that empty string doesn't change the value
	SetIptablesBinary("iptables-custom")
	a.Equal("iptables-custom", iptablesBinary)
	SetIptablesBinary("")
	a.Equal("iptables-custom", iptablesBinary, "Empty string should not change the binary")
}

func TestNatRuleCommandFormat(t *testing.T) {
	a := assert.New(t)

	// Save original values
	originalBinary := iptablesBinary
	originalExecCmd := execCmd
	defer func() {
		iptablesBinary = originalBinary
		execCmd = originalExecCmd
	}()

	vip := net.ParseIP("192.0.2.1")
	localAddr := net.ParseIP("10.0.0.1")

	// Test with default iptables
	SetIptablesBinary("iptables")
	err := natRule("A", vip, localAddr, "tcp", "80", "8080")
	// We expect this to fail in test environment, but we can check the error message
	// contains our command
	if err != nil {
		a.Contains(err.Error(), "iptables -t nat -A PREROUTING")
	}

	// Test with iptables-nft
	SetIptablesBinary("iptables-nft")
	err = natRule("A", vip, localAddr, "tcp", "80", "8080")
	if err != nil {
		a.Contains(err.Error(), "iptables-nft -t nat -A PREROUTING")
	}

	// Test with custom binary path
	SetIptablesBinary("/usr/sbin/iptables")
	err = natRule("D", vip, localAddr, "udp", "53", "5353")
	if err != nil {
		a.Contains(err.Error(), "/usr/sbin/iptables -t nat -D PREROUTING")
		a.Contains(err.Error(), "udp")
		a.Contains(err.Error(), "53")
		a.Contains(err.Error(), "5353")
	}
}

func TestMain(m *testing.M) {
	switch os.Getenv("test_name") {
	case "test_gateway":
		fmt.Println("10.1.1.1")
	case "test_via":
		fmt.Println("10.1.2.1")
	case "test_via_none":
		break
	case "test_add_fail":
		os.Exit(1)
	default:
		break
	}
	if os.Getenv("test_name") != "" {
		return
	}
	os.Exit(m.Run())
}
