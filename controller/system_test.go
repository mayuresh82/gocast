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
	gw, err := gateway(4)
	assert.Nil(t, err)
	assert.Equal(t, "10.1.1.1", gw.String())

	os.Setenv("test_name", "test_gateway_v6")
	gw, err = gateway(6)
	assert.Nil(t, err)
	assert.Equal(t, "2001:dead:beef::1", gw.String())
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

	os.Setenv("test_name", "test_via_v6")
	ip, err = via(net.ParseIP("2001:dead:beef::100"))
	assert.Nil(t, err)
	assert.Equal(t, "2001:dead:beef::1", ip.String())
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

	os.Setenv("test_name", "test_add_v6")
	_, ipnet, _ = net.ParseCIDR("2001:dead:beef:1001::100/64")
	err = addLoopback("test_app", ipnet)
	assert.Nil(t, err)
}

func TestMain(m *testing.M) {
	switch os.Getenv("test_name") {
	case "test_gateway":
		fmt.Println("10.1.1.1")
	case "test_gateway_v6":
		fmt.Println("2001:dead:beef::1")
	case "test_via":
		fmt.Println("10.1.2.1")
	case "test_via_v6":
		fmt.Println("2001:dead:beef::1")
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
